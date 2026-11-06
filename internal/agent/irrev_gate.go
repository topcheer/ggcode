package agent

import (
	"encoding/json"
	"strconv"
	"strings"
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
		"web_search", "web_fetch", "code_execution", "runtime", "clipboard":
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

var irrevReadOnlyBinaries = map[string]bool{
	"ls": true, "cat": true, "head": true, "tail": true, "grep": true,
	"rg": true, "find": true, "which": true, "whereis": true, "pwd": true,
	"date": true, "env": true, "printenv": true, "uname": true,
	"whoami": true, "hostname": true, "wc": true, "sort": true,
	"uniq": true, "diff": true, "cmp": true, "file": true, "stat": true,
	"du": true, "df": true, "free": true, "ps": true, "true": true,
	"cd": true, "shasum": true, "sha256sum": true, "md5sum": true,
	"cksum": true, "basename": true, "dirname": true, "realpath": true,
	"readlink": true, "jq": true, "awk": true, "gawk": true,
	"tr": true, "cut": true, "xxd": true, "od": true, "strings": true,
	"echo": true, "printf": true,
}

var irrevReadOnlyGitSubcommands = map[string]bool{
	"fetch": true, "status": true, "log": true, "diff": true,
	"show": true, "blame": true, "branch": true, "remote": true,
	"rev-parse": true, "rev-list": true, "ls-files": true,
	"describe": true, "shortlog": true, "reflog": true, "grep": true,
	"tag": true, "merge-base": true, "cat-file": true,
	// Probe-class subcommands (read-only remote/repo inspection):
	// #5129 - ls-remote absence misclassified pure read-only patrols as
	// medium, which gated the very grounding the gate demands.
	"ls-remote": true, "name-rev": true, "for-each-ref": true,
	"check-attr": true,
}

var irrevReadOnlyGoSubcommands = map[string]bool{
	"build": true, "test": true, "vet": true, "version": true,
	"env": true, "list": true, "doc": true,
}

var irrevReadOnlyGhSubcommands = map[string]map[string]bool{
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
	return strings.FieldsFunc(cmd, func(r rune) bool {
		return r == ';' || r == '|' || r == '&' || r == '\n'
	})
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
	if len(fields) == 0 {
		return false
	}
	bin := fields[0]
	rest := fields[1:]
	switch bin {
	case "git":
		if len(rest) == 0 {
			return true
		}
		return irrevReadOnlyGitSubcommands[rest[0]]
	case "go":
		if len(rest) == 0 {
			return true
		}
		return irrevReadOnlyGoSubcommands[rest[0]]
	case "gh":

		if len(rest) == 0 {
			return true
		}
		resource, ok := irrevReadOnlyGhSubcommands[rest[0]]
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
		return irrevReadOnlyBinaries[bin]
	}
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
	firstAction := len(s.grounding) == 0

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
