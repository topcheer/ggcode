package agent

import (
	"encoding/json"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/permission"
)

const (
	irrevTierNone   = 0
	irrevTierLow    = 1
	irrevTierMedium = 2
	irrevTierHigh   = 3

	irrevMaxWarnings     = 3
	irrevGroundingWindow = 5
	irrevMaxHistory      = 40
)

type irrevGateState struct {
	grounding         []bool
	warnings          int
	totalGrounded     int
	lastGroundingTool string
}

func newIrrevGateState() *irrevGateState {
	return &irrevGateState{
		grounding: make([]bool, 0, irrevMaxHistory),
	}
}

func (s *irrevGateState) reset() {
	s.grounding = s.grounding[:0]
	s.warnings = 0
	s.totalGrounded = 0
}

func irrevClassifyTool(toolName, args string) int {
	switch toolName {

	case "read_file", "multi_file_read", "search_files", "grep", "glob",
		"list_directory", "code_search", "lsp_definition", "lsp_references",
		"lsp_symbols", "lsp_hover", "lsp_workspace_symbols", "lsp_implementation",
		"lsp_incoming_calls", "lsp_outgoing_calls", "lsp_prepare_call_hierarchy",
		"lsp_diagnostics", "lsp_document_highlights", "lsp_code_actions",
		"git_show", "git_diff", "git_blame", "git_log", "git_status",
		"git_branch_list", "git_remote", "git_stash_list",
		"web_search", "web_fetch", "code_execution", "runtime", "clipboard",
		"merge_preflight":
		return irrevTierNone

	case "git_tag":
		var a struct {
			Action string `json:"action"`
		}
		if json.Unmarshal([]byte(args), &a) == nil && a.Action == "delete" {
			return irrevTierMedium
		}
		return irrevTierNone

	case "edit_file", "write_file", "multi_edit_file", "multi_file_edit",
		"multi_file_write", "notebook_edit":
		return irrevTierLow
	case "undo_edit":
		return irrevTierLow

	case "git_stash":
		var a struct {
			Action string `json:"action"`
		}
		if json.Unmarshal([]byte(args), &a) == nil && a.Action == "drop" {
			return irrevTierHigh
		}
		return irrevTierLow
	case "git_add", "git_checkout":
		return irrevTierLow

	case "git_commit", "git_revert":
		return irrevTierMedium
	case "file_ops", "batch_replace", "lsp_rename":

		if toolName == "file_ops" {
			var a struct {
				Action    string `json:"action"`
				Recursive bool   `json:"recursive"`
			}
			if json.Unmarshal([]byte(args), &a) == nil && a.Action == "delete" && a.Recursive {
				return irrevTierHigh
			}
			// #1798 true implementation: file_ops real payloads nest the
			// operations as [{action, recursive, ...}] - the flat top-level
			// shape above never fires for them. A recursive delete must be
			// High regardless of payload shape.
			var ops struct {
				Operations []struct {
					Action    string `json:"action"`
					Recursive bool   `json:"recursive"`
				} `json:"operations"`
			}
			if json.Unmarshal([]byte(args), &ops) == nil {
				for _, op := range ops.Operations {
					if op.Action == "delete" && op.Recursive {
						return irrevTierHigh
					}
				}
			}
		}
		return irrevTierMedium
	case "start_command", "run_command":

		if irrevIsDestructiveCommand(args) {
			return irrevTierHigh
		}

		if irrevIsReadOnlyCommand(args) {
			return irrevTierNone
		}
		return irrevTierMedium

	case "git_push":
		return irrevTierHigh
	case "git_reset":
		var resetArgs struct {
			Mode  string   `json:"mode"`
			Files []string `json:"files"`
		}
		modeHard := false
		if err := json.Unmarshal([]byte(args), &resetArgs); err == nil && strings.EqualFold(strings.TrimSpace(resetArgs.Mode), "hard") {
			modeHard = true
		}

		if len(resetArgs.Files) > 0 {
			return irrevTierLow
		}
		if modeHard || strings.Contains(strings.ToLower(args), "--hard") {
			return irrevTierHigh
		}
		return irrevTierLow

	case "desktop_control", "browser", "mobile_device":
		var a struct {
			Action string `json:"action"`
		}
		if json.Unmarshal([]byte(args), &a) != nil {
			return irrevTierLow
		}
		switch strings.ToLower(a.Action) {

		case "snapshot_ui", "find_element", "list_windows", "list_apps",
			"active_app", "display_info", "mouse_position",
			"extract", "screenshot", "links", "status", "content",
			"wait", "wait_not", "devices", "snapshot", "logs":
			return irrevTierNone

		case "quit_app", "uninstall":
			return irrevTierHigh

		case "evaluate", "upload", "install":
			return irrevTierMedium
		}
		return irrevTierLow
	default:

		if extractCommandFromArgs(json.RawMessage(args)) != "" && irrevIsDestructiveCommand(args) {
			return irrevTierHigh
		}
		return irrevTierLow
	}
}

func irrevIsDestructiveCommand(args string) bool {
	var payload struct {
		Command string `json:"command"`
	}
	cmdStr := args
	if err := json.Unmarshal([]byte(args), &payload); err == nil && payload.Command != "" {
		cmdStr = payload.Command
	}
	lower := strings.ToLower(stripLeadingShellComment(cmdStr))
	patterns := []string{
		"push --force", "push -f", "push --force-with-lease",
		"reset --hard", "checkout -- .", "clean -fd", "clean -f",
		"rm -rf", "rm -r -f", "rmdir /s",
		"drop table", "drop database", "truncate ",
		"dd if=", "mkfs", "fdisk",
		":(){:|:&};:",
		"chmod -r 777", "chown -r",
		"git push origin --delete", "git branch -d",
		"git tag -d", "git remote remove",

		"git stash drop", "git stash clear",
		"sudo rm", "shutdown", "reboot", "halt",
	}
	for _, p := range patterns {
		if strings.Contains(lower, p) {
			return true
		}
	}
	return false
}

// ReadOnlyCommandBinaries is the read-only command whitelist used by the
// irreversibility gate: a shell segment whose first effective token (after
// env-assignment and wrapper prefixes like sudo/nohup are stripped) is one
// of these binaries is classified as read-only (no gate). Every entry only
// observes state - it cannot modify files, network, or process state when
// invoked without redirects/pipes into mutating commands (those cases are
// rejected before this table is consulted).
var ReadOnlyCommandBinaries = map[string]bool{
	// File/directory inspection - read bytes, never write them.
	"ls": true, "cat": true, "head": true, "tail": true, "grep": true,
	"rg": true, "find": true, "file": true, "stat": true,
	// Identity/system introspection - print, do not mutate.
	"which": true, "whereis": true, "pwd": true,
	"date": true, "env": true, "printenv": true, "uname": true,
	"whoami": true, "hostname": true, "ps": true, "free": true,
	// Disk usage inspection.
	"du": true, "df": true,
	// Pure text filters - stream transforms with no side effects.
	"wc": true, "sort": true, "uniq": true, "diff": true, "cmp": true,
	"shasum": true, "sha256sum": true, "md5sum": true,
	"cksum": true, "basename": true, "dirname": true, "realpath": true,
	"readlink": true, "jq": true, "awk": true, "gawk": true,
	"tr": true, "cut": true, "xxd": true, "od": true, "strings": true,
	// stdout-only producers - write nothing unless redirected (and any
	// redirect blocks read-only classification earlier).
	"echo": true, "printf": true, "true": true, "cd": true,
}

// ReadOnlyGitSubcommands lists git subcommands that only inspect repository
// or remote state (no commit/ref/index mutation), so a git <sub> segment is
// exempt from the irreversibility gate.
var ReadOnlyGitSubcommands = map[string]bool{
	"fetch": true, "status": true, "log": true, "diff": true,
	"show": true, "blame": true, "branch": true, "remote": true,
	"rev-parse": true, "rev-list": true, "ls-files": true,
	"describe": true, "shortlog": true, "reflog": true, "grep": true,
	"tag": true, "merge-base": true, "cat-file": true,
	// Probe-class subcommands (read-only remote/repo inspection):
	// #5129 - ls-remote absence misclassified pure read-only patrols as
	// medium, which gated the very grounding the gate demands.
	"ls-remote": true, "name-rev": true, "for-each-ref": true,
	"check-attr": true, "show-ref": true, "var": true,
}

// ReadOnlyGoSubcommands lists go tool subcommands that build or inspect
// without publishing: build/test/vet write only to the build cache, and the
// rest are pure queries.
var ReadOnlyGoSubcommands = map[string]bool{
	"build": true, "test": true, "vet": true, "version": true,
	"env": true, "list": true, "doc": true,
}

// ReadOnlyGhSubcommands maps gh CLI resources to their read-only view/list
// subcommands (gh api is handled separately with GET-only detection).
var ReadOnlyGhSubcommands = map[string]map[string]bool{
	"issue":   {"view": true, "list": true},
	"pr":      {"view": true, "list": true, "diff": true, "checks": true},
	"run":     {"view": true, "list": true, "watch": true},
	"release": {"view": true, "list": true},
	"search":  {"issues": true, "prs": true, "repos": true, "code": true, "commits": true},
	"label":   {"list": true, "view": true},
	"org":     {"list": true},
}

func irrevIsReadOnlyCommand(args string) bool {
	var payload struct {
		Command string `json:"command"`
	}
	cmdStr := args
	if err := json.Unmarshal([]byte(args), &payload); err == nil && payload.Command != "" {
		cmdStr = payload.Command
	}
	cmdStr = stripLeadingShellComment(cmdStr)
	cmdStr = irrevNeutralizeSafeRedirects(cmdStr)
	if strings.TrimSpace(cmdStr) == "" {
		return false
	}
	for _, seg := range irrevSplitShellSegments(cmdStr) {
		if !irrevSegmentIsReadOnly(seg) {
			return false
		}
	}
	return true
}

func irrevNeutralizeSafeRedirects(cmd string) string {
	for _, sink := range []string{"2>&1", "1>&2", "2>>/dev/null", "2>/dev/null", ">>/dev/null", ">/dev/null"} {
		cmd = strings.ReplaceAll(cmd, sink, " ")
	}
	return cmd
}

func irrevSplitShellSegments(cmd string) []string {
	// #5269: the splitter was quote-blind, so a quoted jq program like
	// `gh api ... -q '.[] | select(.x) | .number]'` was shredded on the pipes
	// INSIDE the quotes; the fragment then failed the read-only table and the
	// whole GET-class patrol call gated (the "0 grounding" FP). Split on
	// separators only OUTSIDE single/double quotes.
	var segs []string
	var cur strings.Builder
	var quote rune
	for _, r := range cmd {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			}
			cur.WriteRune(r)
		case r == '\'' || r == '"':
			quote = r
			cur.WriteRune(r)
		case r == ';' || r == '|' || r == '&' || r == '\n':
			if cur.Len() > 0 {
				segs = append(segs, cur.String())
			}
			cur.Reset()
		default:
			cur.WriteRune(r)
		}
	}
	if cur.Len() > 0 {
		segs = append(segs, cur.String())
	}
	return segs
}

// #5339: shell control keywords that open compound statements. A bare
// keyword segment ("do", "done", "{", ...) is transparent (read-only);
// a keyword-leading segment with a body command is classified by the
// body command instead (#5339).
func irrevIsShellControlKeyword(tok string) bool {
	switch tok {
	case "for", "do", "done", "while", "until", "{", "}",
		"if", "then", "fi", "elif", "else", "case", "esac", "in":
		return true
	}
	return false
}

func irrevSegmentIsReadOnly(seg string) bool {
	seg = strings.TrimSpace(seg)
	if seg == "" {
		return true
	}
	if strings.Contains(seg, ">") || strings.Contains(seg, "$(") ||
		strings.Contains(seg, "`") || strings.Contains(seg, "<(") {

		return false
	}
	fields := strings.Fields(seg)

	i := 0
	for i < len(fields)-1 && i < 4 && isEnvAssignment(fields[i]) {
		i++
	}
	fields = fields[i:]
	// Wrapper prefixes do not change what the command DOES: the first
	// effective token is what follows sudo/nohup, so `sudo cat /etc/hosts`
	// or `nohup grep -rn x .` is classified by cat/grep, not the wrapper.
	// Mutating payloads behind a wrapper still gate: destructive-command
	// detection runs on the raw command BEFORE this classifier, so
	// `sudo rm -rf ...` is already tier-HIGH and never reaches here, and
	// wrapper-prefixed non-whitelisted commands simply stay medium.
	for len(fields) > 0 && isReadOnlyWrapperPrefix(fields[0]) {
		fields = fields[1:]
	}
	if len(fields) == 0 {
		return false
	}
	bin := fields[0]
	rest := fields[1:]
	// #5269: bare shell control keywords (for/do/done/while/case ...) are
	// transparent - the loop BODY is already split into its own segments and
	// classified there, so `for n in 1 2; do gh api ...; done` patrols no
	// longer gate on the keyword segments while a mutating body still does.
	// #5339: transparent only for BARE keyword segments. A segment like
	// `do rm -rf x` (same-line body, no separators between keyword and
	// command) previously returned true unconditionally and the mutating
	// body escaped classification entirely. Strip leading control keywords
	// and fall through to normal classification of what remains.
	if irrevIsShellControlKeyword(fields[0]) {
		if len(rest) == 0 {
			return true
		}
		for len(fields) > 0 && irrevIsShellControlKeyword(fields[0]) {
			fields = fields[1:]
		}
		if len(fields) == 0 {
			return true
		}
		joined := strings.Join(fields, " ")
		// Value list ("for n in 1 2" -> "n 1 2") or test condition
		// ("if [ -d x ]") - structural forms, not command bodies.
		if strings.Contains(joined, " in ") || fields[0] == "[" {
			return true
		}
		bin = fields[0]
		rest = fields[1:]
	}
	switch bin {
	case "git":
		if len(rest) == 0 {
			return true
		}
		// #5199: bare patrol inventory variant (list is read-only;
		// add/remove/prune are stateful and stay gated).
		if rest[0] == "worktree" && len(rest) > 1 && rest[1] == "list" {
			return true
		}
		return ReadOnlyGitSubcommands[rest[0]]
	case "go":
		if len(rest) == 0 {
			return true
		}
		return ReadOnlyGoSubcommands[rest[0]]
	case "gh":

		if len(rest) == 0 {
			return true
		}
		// #5199: `gh api` is the dominant patrol signature (check-run
		// annotations, merge-state probes) but was absent from the read-only
		// table, so every round's first batched call tripped the
		// zero-grounding flag. GET-by-default is read-only UNLESS an
		// explicit mutating method or body/field flag is present
		// (conservative: field flags imply a write payload even without -X).
		if rest[0] == "api" {
			return ghApiArgsAreReadOnly(rest[1:])
		}
		resource, ok := ReadOnlyGhSubcommands[rest[0]]
		if !ok {
			return false
		}
		if len(rest) == 1 {
			return false
		}
		return resource[rest[1]]
	case "sed":

		for _, a := range rest {
			if a == "-i" || strings.HasPrefix(a, "-i") && len(a) > 2 || a == "--in-place" {
				return false
			}
		}
		return true
	default:
		return ReadOnlyCommandBinaries[bin]
	}
}

// ghApiArgsAreReadOnly reports whether a `gh api` invocation (args = the
// tokens after "gh api") is a read-only call. GET-by-default is read-only
// UNLESS an explicit mutating method or body/field flag is present
// (conservative: field flags imply a write payload even without -X).
func ghApiArgsAreReadOnly(args []string) bool {
	mutating := false
	for i := 0; i < len(args); i++ {
		a := args[i]
		// #5289: pflag accepts equals forms (--method=DELETE,
		// --field=k=v, --input=f) and glued shorthand values
		// (-Fk=v, -XDELETE). Previous exact-equality matching
		// missed every equals form, judging mutating calls
		// read-only. Normalize each token to name[=value] first.
		name, val := a, ""
		hasVal := false
		if eq := strings.IndexByte(a, '='); eq >= 0 {
			name, val, hasVal = a[:eq], a[eq+1:], true
		}
		switch name {
		case "-X", "--method":
			m := val
			if !hasVal {
				if i+1 >= len(args) {
					continue
				}
				m = args[i+1]
				i++
			}
			m = strings.ToUpper(m)
			if m != "GET" && m != "HEAD" {
				mutating = true
			}
		case "-f", "--field", "-F", "--field-file", "--input", "--slurp":
			// conservative: field/body flags imply a write
			// payload even without -X (values inline or next token)
			mutating = true
		default:
			// glued shorthand values: -XDELETE, -Fk=v, -fk=v
			if strings.HasPrefix(name, "-X") && len(name) > 2 {
				m := strings.ToUpper(name[2:])
				if m != "GET" && m != "HEAD" {
					mutating = true
				}
			} else if (strings.HasPrefix(name, "-F") || strings.HasPrefix(name, "-f")) && len(name) > 2 {
				mutating = true
			}
		}
	}
	return !mutating
}

// isReadOnlyWrapperPrefix reports whether tok is a command wrapper that
// never changes the payload command's read-only-ness: sudo elevates
// privileges and nohup detaches from the terminal, but neither turns an
// inspection command into a mutating one.
func isReadOnlyWrapperPrefix(tok string) bool {
	switch tok {
	case "sudo", "nohup":
		return true
	}
	return false
}

func isEnvAssignment(tok string) bool {
	eq := strings.Index(tok, "=")
	if eq <= 0 {
		return false
	}
	for j, r := range tok[:eq] {
		if !(r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (j > 0 && r >= '0' && r <= '9')) {
			return false
		}
	}
	return true
}

func irrevIsGroundingAction(toolName string) bool {
	switch toolName {
	case "read_file", "multi_file_read", "search_files", "grep", "glob",
		"list_directory", "code_search", "lsp_definition", "lsp_references",
		"lsp_symbols", "lsp_hover", "lsp_workspace_symbols",
		"lsp_diagnostics", "lsp_implementation", "lsp_incoming_calls",
		"lsp_outgoing_calls", "git_show", "git_diff", "git_blame",
		"git_log", "git_status", "review_changes", "code_health",
		"scan_todos", "dep_graph":
		return true

	case "run_command":
		return true
	case "merge_preflight":
		// The completion gate demands a preflight before protected merges;
		// counting it as grounding keeps the two gates from demanding
		// conflicting evidence.
		return true
	}
	return false
}

func (s *irrevGateState) recordOutcome(toolName string, isError bool) {
	if !isError || len(s.grounding) == 0 {
		return
	}
	if !s.grounding[len(s.grounding)-1] || s.lastGroundingTool != toolName {
		return
	}
	s.grounding[len(s.grounding)-1] = false
	if s.totalGrounded > 0 {
		s.totalGrounded--
	}
	s.lastGroundingTool = ""
}

func (s *irrevGateState) recordAction(toolName, args string) string {
	tier := irrevClassifyTool(toolName, args)
	isGrounding := irrevIsGroundingAction(toolName)

	// #5129 (S-70): the grounding window starts empty, so a session's FIRST
	// medium-tier command would always gate (structurally unavoidable - there
	// is no way to accumulate grounding before any tool call exists). Exempt
	// the very first action from the MEDIUM threshold; HIGH still gates.
	// #1798 guard: the exemption must NOT re-open the self-grounding hole
	// ("current call counts as its own grounding") - run_command stays gated
	// even as a first action, exactly as #1798 pinned. Only non-shell medium
	// tools (git_commit / file_ops / batch_replace / lsp_rename ...) get the
	// first-action exemption.
	firstAction := len(s.grounding) == 0 && toolName != "run_command" && toolName != "start_command"

	recentGrounding := 0
	start := len(s.grounding) - irrevGroundingWindow
	if start < 0 {
		start = 0
	}
	for i := start; i < len(s.grounding); i++ {
		if s.grounding[i] {
			recentGrounding++
		}
	}

	if len(s.grounding) >= irrevMaxHistory {
		s.grounding = s.grounding[1:]
	}
	s.grounding = append(s.grounding, isGrounding)
	if isGrounding {
		s.totalGrounded++
		s.lastGroundingTool = toolName
	}

	if tier < irrevTierMedium {
		return ""
	}

	var threshold int
	switch tier {
	case irrevTierMedium:
		threshold = 1
		if firstAction {
			return ""
		}
	case irrevTierHigh:
		threshold = 2
	default:
		threshold = 0
	}

	if recentGrounding >= threshold {
		return ""
	}

	if s.warnings >= irrevMaxWarnings {
		return ""
	}

	s.warnings++

	tierLabel := "medium-impact"
	if tier >= irrevTierHigh {
		tierLabel = "HIGH-IMPACT (hard to reverse)"
	}

	return "[irreversibility-gate] You are about to execute a " + tierLabel +
		" action (" + toolName + ") with only " + strconv.Itoa(recentGrounding) +
		" grounding action(s) in the last " + strconv.Itoa(irrevGroundingWindow) +
		" iterations. Calibrated abstention principle: the more " +
		"irreversible the action, the more certain you should be. " +
		"VERIFY: check repository state (git status/diff), confirm " +
		"the operation is correct and necessary, and ensure you " +
		"have sufficient information before proceeding with this " +
		"difficult-to-reverse action."
}

// --- completion gates: lifecycle merge gate (B-side wiring) ---
//
// The merge gate upgrades protected lifecycle commands (gates.merge.commands,
// prefix + PR number form like "gh pr merge 5356") to a gate-require level
// in supervised mode: instead of gating grounding like the tier system
// above, it injects a REQUIREMENT prompt telling the model to run the
// merge_preflight tool first and pin the merge with --match-head-commit.
// It never hard-blocks; bypass/cruise permission modes pass through with
// no prompt. A PREFLIGHT: PASS snapshot for the same PR stays "fresh" for
// gates.merge.preflight_ttl (default 60s) and satisfies the requirement.

// mergeGateState holds PREFLIGHT: PASS snapshots keyed by PR number.
type mergeGateState struct {
	mu        sync.Mutex
	snapshots map[int]mergePassSnapshot
	cfgFn     func() config.MergeGateConfig
	now       func() time.Time
}

type mergePassSnapshot struct {
	headSHA string
	at      time.Time
}

func newMergeGateState() *mergeGateState {
	return &mergeGateState{
		snapshots: make(map[int]mergePassSnapshot),
		now:       time.Now,
	}
}

// SetMergeGateConfigProvider wires a live gates config reader (set by the
// runtime once the config access layer exists). Nil/never-set means the
// built-in defaults: enabled, ["gh pr merge", "gh pr close"], 60s TTL.
func (s *mergeGateState) SetMergeGateConfigProvider(fn func() config.MergeGateConfig) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cfgFn = fn
}

func (s *mergeGateState) mergeGateConfig() config.MergeGateConfig {
	s.mu.Lock()
	fn := s.cfgFn
	s.mu.Unlock()
	if fn == nil {
		return config.MergeGateConfig{}
	}
	return fn()
}

// RecordPreflightResult parses a merge_preflight tool result and, when the
// verdict is PASS, records a freshness snapshot for that PR and head.
// The first output line of the tool is the machine-readable report JSON.
func (s *mergeGateState) RecordPreflightResult(toolName, content string) {
	if toolName != "merge_preflight" {
		return
	}
	first := content
	if i := strings.IndexByte(content, '\n'); i >= 0 {
		first = content[:i]
	}
	first = strings.TrimSpace(first)
	if !strings.HasPrefix(first, "{") {
		return
	}
	var report struct {
		PR      int    `json:"pr"`
		HeadSHA string `json:"head_sha"`
		Verdict string `json:"verdict"`
	}
	if err := json.Unmarshal([]byte(first), &report); err != nil {
		return
	}
	if report.Verdict != "PASS" || report.PR <= 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.snapshots[report.PR] = mergePassSnapshot{headSHA: report.HeadSHA, at: s.now()}
}

// preflightFresh reports whether a PASS snapshot for pr is younger than
// ttl, plus the head SHA it pinned.
func (s *mergeGateState) preflightFresh(pr int, ttl time.Duration) (bool, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	snap, ok := s.snapshots[pr]
	if !ok {
		return false, ""
	}
	if s.now().Sub(snap.at) > ttl {
		return false, ""
	}
	return true, snap.headSHA
}

// checkCommand returns the REQUIREMENT prompt for a protected lifecycle
// command without a fresh PASS snapshot, or "" when no requirement
// applies (non-protected command, gate disabled, cruise/bypass mode, or
// a fresh snapshot). Reuses the B-side first-token/subcommand extraction
// infra (extractCommandFromArgs + stripLeadingShellComment).
func (s *mergeGateState) checkCommand(mode permission.PermissionMode, toolName, args string) string {
	switch toolName {
	case "run_command", "start_command":
	default:
		return ""
	}
	if mode.IsCruiseMode() {
		// bypass/cruise: pass through, never prompt, never block
		return ""
	}
	cmd := extractCommandFromArgs(json.RawMessage(args))
	if cmd == "" {
		return ""
	}
	cfg := s.mergeGateConfig()
	if !cfg.EffectiveEnabled() {
		return ""
	}
	pr, hit := config.MatchMergeGateCommand(cmd, cfg.EffectiveCommands())
	if !hit {
		return ""
	}
	if fresh, _ := s.preflightFresh(pr, cfg.EffectivePreflightTTL()); fresh {
		return ""
	}
	return mergeGateRequirement(cmd, pr, cfg.EffectivePreflightTTL())
}

func mergeGateRequirement(cmd string, pr int, ttl time.Duration) string {
	// #5401: selector-less/branch forms ("gh pr merge", "gh pr merge <branch>")
	// gate with PR 0 - the agent must resolve the target PR first instead of
	// preflighting PR number 0.
	step1 := "1. Call the merge_preflight tool with {\"pr_number\":" + strconv.Itoa(pr) +
		"} and confirm the result line is PREFLIGHT: PASS.\n"
	if pr <= 0 {
		step1 = "1. Identify the target PR (the command carries no explicit PR number - " +
			"e.g. from the current branch), then call the merge_preflight tool with that " +
			"{\"pr_number\":N} and confirm the result line is PREFLIGHT: PASS.\n"
	}
	return "[completion-gate] The command \"" + cmd + "\" is a gated lifecycle action " +
		"(gates.merge). REQUIREMENT before executing it:\n" +
		step1 +
		"2. Pin the action to the head SHA preflight reports by adding " +
		"--match-head-commit <head_sha>.\n" +
		"A PASS snapshot for this PR stays valid for " + ttl.String() +
		" (preflight_ttl); while it is fresh this gate does not re-trigger. " +
		"This gate states requirements only; it does not block execution."
}

// SetMergeGateConfigProvider wires a live gates.merge config reader into
// the agent's completion gate (called by the runtime after startup and on
// config reload).
func (a *Agent) SetMergeGateConfigProvider(fn func() config.MergeGateConfig) {
	if a.mergeGate == nil {
		return
	}
	a.mergeGate.SetMergeGateConfigProvider(fn)
}

// MergeGatePreflightStatus reports whether a fresh PREFLIGHT: PASS
// snapshot exists for the PR (within the configured TTL) and the head SHA
// it pinned. Exported for the merge_preflight tool's test action.
func (a *Agent) MergeGatePreflightStatus(prNumber int) (fresh bool, headSHA string) {
	if a.mergeGate == nil || prNumber <= 0 {
		return false, ""
	}
	cfg := a.mergeGate.mergeGateConfig()
	return a.mergeGate.preflightFresh(prNumber, cfg.EffectivePreflightTTL())
}
