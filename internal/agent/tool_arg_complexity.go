package agent

// Think-Augmented Function Calling (arXiv:2601.18282v2, sa-131 NEW_GAP):
// pre-dispatch argument-complexity scoring that triggers a per-parameter
// granular justification round for high-complexity WRITE calls.
//
// Gap (three-point evidence, sa-131): the existing pre-flight path is a pure
// presence check (tool_required_check.go: schema-driven required keys), the
// adaptive-effort loop is function-level POST-hoc feedback
// (adaptive_effort.go: error-rate -> effort tier), and tool-target-mismatch
// is an after-the-fact detector. Nothing asks the model to JUSTIFY critical
// parameter choices BEFORE a complex write dispatches.
//
// Mechanics (mirrors the preflightRequiredCheck one-retry-round contract):
//   - Deterministic, zero-LLM complexity heuristics per write tool family.
//   - score >= per-tool threshold AND write-class tool AND no rationale
//     attached: reject once with an error that names the heavy parameters;
//     the resent call passes via the intent-key memo.
//   - Read-only tools never trigger (their complexity is cheap to undo).
//
// v2 calibration (gate-recalib round, Oct 2026) fixing two field defects:
//   - Defect A: the gate demanded an `arg_rationale` field that no tool
//     schema declared, so schema-strict models could not comply at all.
//     The field is now injected into write-tool schemas (see
//     injectArgRationaleSchema) and is stripped before dispatch.
//   - Defect B: the fired-once memo keyed on the RAW argument bytes, but an
//     LLM resend is never byte-identical (key order, whitespace, added
//     description), so "identical" retries re-fired forever. The memo now
//     keys on a NORMALIZED INTENT projection (heavy-parameter shapes, not
//     bytes), and a per-run fire budget (3) fails the gate open instead of
//     looping. Gate state moved from a package-level sync.Map to run-scoped
//     state (reset each turn with the other detectors).
//   - run_command threshold raised 4 -> 8: legitimate build/test pipelines
//     commonly carry 4-6 pipes and were being choked. Destructive tokens
//     were re-tiered (+8 hard / +2 soft) so single dangerous commands still
//     fire, and PowerShell dialect scoring was added (backticks are
//     continuation characters there, not substitution; Remove-Item /
//     Format-Volume / reg delete are the destructive forms).
//   - edit_file relaxed: pure large edits now need a 200+ line anchor (was
//     100), with an 80+ buffer tier; the delete-semantics weight is kept so
//     bulk deletions still justify themselves.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"strings"
	"sync"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/tool"
)

// Per-tool complexity thresholds: the score at which a write-class call
// must carry an arg_rationale. run_command needs 8 (pipes/substitution are
// legitimate plumbing); every other write tool stays at 4.
const (
	argComplexityDefaultThreshold = 4
	argComplexityRunCmdThreshold  = 8
)

// argComplexityMaxFires bounds how many times the gate may reject within a
// single run. Fail-open after the budget is spent: a mis-calibrated score
// must cost three round-trips, never an unbounded loop.
const argComplexityMaxFires = 3

// argRationaleField is the optional justification argument. Any non-blank
// value passes: the point is the justification round-trip, not grading the
// prose. It is injected into write-tool schemas (so schema-strict models
// can send it) and stripped before the call dispatches.
const argRationaleField = "arg_rationale"

// argComplexityWriteTools are the write-class tools whose complexity score
// participates. Everything else (reads, searches, git status) is exempt.
var argComplexityWriteTools = map[string]bool{
	"run_command":      true,
	"edit_file":        true,
	"multi_edit_file":  true,
	"multi_file_edit":  true,
	"write_file":       true,
	"multi_file_write": true,
	"batch_replace":    true,
	"file_ops":         true,
	"notebook_edit":    true,
	"git_checkout":     true,
	"git_reset":        true,
	"git_revert":       true,
	"git_add":          true,
	"git_commit":       true,
}

// argComplexityGateState is the run-scoped gate state (defect B/C fix):
// fired holds normalized intent keys that already burned their
// justification round, and fires counts rejections against the budget.
type argComplexityGateState struct {
	mu    sync.Mutex
	fired map[string]struct{}
	fires int
}

func newArgComplexityGateState() *argComplexityGateState {
	return &argComplexityGateState{fired: make(map[string]struct{})}
}

// reset clears the memo and budget (wired next to irrevGate.reset in
// reset_turn.go so every turn starts with a fresh gate).
func (s *argComplexityGateState) reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.fired = make(map[string]struct{})
	s.fires = 0
}

// argComplexityThresholdFor returns the per-tool score threshold.
func argComplexityThresholdFor(name string) int {
	if name == "run_command" {
		return argComplexityRunCmdThreshold
	}
	return argComplexityDefaultThreshold
}

// scoreArgComplexity returns a deterministic complexity score for the call
// plus the parameter names that carried the weight (for the error message).
// Unknown tools score 0.
// editPair is one old/new edit from either multi-tool shape.
type editPair struct {
	OldText string `json:"old_text"`
	NewText string `json:"new_text"`
}

// parseMultiEditsForComplexity aggregates the edit pairs from BOTH
// multi-tool shapes (#4875): multi_edit_file's top-level "edits" array and
// multi_file_edit's per-file "files"[].edits nesting. Returns nil when the
// args match neither shape (unknown tools/unparseable input).
func parseMultiEditsForComplexity(args map[string]json.RawMessage) []editPair {
	if raw, ok := args["edits"]; ok {
		var edits []editPair
		if json.Unmarshal(raw, &edits) == nil {
			return edits
		}
	}
	if raw, ok := args["files"]; ok {
		var files []struct {
			Edits []editPair `json:"edits"`
		}
		if json.Unmarshal(raw, &files) == nil {
			var all []editPair
			for _, f := range files {
				all = append(all, f.Edits...)
			}
			return all
		}
	}
	return nil
}

func scoreArgComplexity(toolName string, args map[string]json.RawMessage) (int, []string) {
	score := 0
	var heavy []string
	add := func(n int, param string) {
		if n > 0 {
			score += n
			heavy = append(heavy, param)
		}
	}
	str := func(key string) string {
		raw, ok := args[key]
		if !ok {
			return ""
		}
		var s string
		if json.Unmarshal(raw, &s) == nil {
			return s
		}
		return ""
	}
	switch toolName {
	case "run_command":
		cmd := str("command")
		if n := scoreShellComplexityChained(cmd); n > 0 {
			add(n, "command")
		}
	case "edit_file":
		old := str("old_text")
		// v2: pure edits need a 200+ line anchor to fire (was 100+4 /
		// 30+2); 80+ is a buffer tier that only fires combined with
		// delete semantics or replace_all below.
		switch {
		case strings.Count(old, "\n") >= 200:
			add(4, "old_text")
		case strings.Count(old, "\n") >= 80:
			add(2, "old_text")
		}
		if strings.TrimSpace(str("new_text")) == "" && strings.TrimSpace(old) != "" {
			add(2, "new_text")
		}
		if b, ok := args["replace_all"]; ok {
			var bval bool
			if json.Unmarshal(b, &bval) == nil && bval {
				add(1, "replace_all")
			}
		}
	case "multi_edit_file", "multi_file_edit":
		// #4875: the two tools have DIFFERENT edits shapes - multi_edit_file
		// carries a top-level "edits" array, multi_file_edit nests per-file
		// edits under "files"[].edits. Reading args["edits"] for both meant
		// every legal multi_file_edit call deterministically missed (!ok ->
		// break): score always 0, delete semantics unreachable - the largest
		// blast-radius write tool was invisible to the complexity gate.
		edits := parseMultiEditsForComplexity(args)
		switch {
		case len(edits) >= 15:
			add(3, "edits")
		case len(edits) >= 5:
			add(2, "edits")
		}
		deletes := 0
		for _, e := range edits {
			if strings.TrimSpace(e.NewText) == "" && strings.TrimSpace(e.OldText) != "" {
				deletes++
			}
		}
		if deletes >= 2 {
			add(2, "edits")
		}
	case "write_file", "multi_file_write":
		content := str("content")
		if strings.Count(content, "\n") >= 200 {
			add(2, "content")
		}
		if toolName == "multi_file_write" {
			if raw, ok := args["files"]; ok {
				var files []json.RawMessage
				if json.Unmarshal(raw, &files) == nil && len(files) >= 5 {
					add(2, "files")
				}
			}
		}
	case "batch_replace":
		if raw, ok := args["files"]; ok {
			var files []json.RawMessage
			if json.Unmarshal(raw, &files) == nil && len(files) >= 10 {
				add(2, "files")
			}
		}
	case "file_ops":
		ops := scoreFileOpsDeletes(args)
		if ops > 0 {
			add(ops, "operations")
		}
	case "git_reset":
		if strings.Contains(str("mode"), "hard") {
			add(4, "mode")
		}
	case "git_revert":
		// #4647: was a dead override-table entry - no case, always 0.
		// no_commit rewrites history in place (like reset --hard): +4.
		// A normal revert creates an undoable commit: +2.
		if b, ok := args["no_commit"]; ok {
			var bval bool
			if json.Unmarshal(b, &bval) == nil && bval {
				add(4, "no_commit")
			}
		}
		if score == 0 {
			add(2, "revert")
		}
	case "git_checkout":
		// Plain branch switches are routine and carry their own dirty-tree
		// guard: 0. Creating from an explicit start_point (arbitrary
		// commit/tag, often mid-rebase surgery) is the heavier shape: +2.
		if b, ok := args["create"]; ok {
			var bval bool
			if json.Unmarshal(b, &bval) == nil && bval && str("start_point") != "" {
				add(2, "start_point")
			}
		}
	case "git_add":
		// Staging the whole tree at once (".", 10+ paths) widens the blast
		// radius of whatever commit follows: +2.
		if raw, ok := args["files"]; ok {
			var files []string
			if json.Unmarshal(raw, &files) == nil && (len(files) >= 10 || (len(files) == 1 && files[0] == ".")) {
				add(2, "files")
			}
		}
	case "git_commit":
		// all=true auto-stages every tracked modification (scope creep into
		// files the caller never named); very long messages often smuggle
		// rationale that belongs in the diff review: +2 each.
		if b, ok := args["all"]; ok {
			var bval bool
			if json.Unmarshal(b, &bval) == nil && bval {
				add(2, "all")
			}
		}
		if len(str("message")) >= 500 {
			add(2, "message")
		}
	case "notebook_edit":
		// #4647: dead entry, no case. Deleting a cell destroys content the
		// next tool result will not show: +3. Replacing a large cell body
		// mirrors write_file's 200-line tier: +2.
		switch str("operation") {
		case "delete":
			add(3, "operation")
		case "replace":
			if strings.Count(str("source"), "\n") >= 200 {
				add(2, "source")
			}
		}
	}
	return score, heavy
}

// PowerShell dialect markers: any of these in a command means backticks are
// line-continuation/escape characters (NOT command substitution) and the PS
// token tables apply (rm/curl still hit as aliases).
func shellLooksPowerShell(cmd string) bool {
	for _, m := range []string{"$env:", "-Recurse", "Invoke-", "Remove-Item", "Set-ExecutionPolicy", "iwr ", "Format-Volume", "Get-ChildItem", "Out-Null"} {
		if strings.Contains(cmd, m) {
			return true
		}
	}
	return false
}

// Hard (destructive, hard-to-undo) tokens: a single occurrence fires the
// run_command gate on its own (+8 each, capped at 16).
var shellHardTokens = []string{
	"rm -rf",
	"git reset --hard",
	"git push --force",
	"Remove-Item -Recurse -Force", // PS rm -rf equivalent
	"Format-Volume",               // PS disk wipe
	"reg delete",
}

// Soft (risky-but-not-destructive) tokens: +2 each, capped at 6. Alone they
// never fire the threshold-8 gate; they combine with structure weight.
var shellSoftTokens = []string{
	"curl",
	"wget",
	"chmod 777",
	"sudo",
	"Invoke-WebRequest", // PS curl equivalent
	"iwr",
	"Set-ExecutionPolicy Bypass",
}

// splitShellSubcommands splits a compound command into its individual
// subcommands on the shell sequencing operators (&& || ; and newlines).
// Sequences inside $() / backticks are left intact within their segment -
// a substitution belongs to the one subcommand that expands it.
func splitShellSubcommands(cmd string) []string {
	if !strings.ContainsAny(cmd, "&;\n") {
		return []string{cmd}
	}
	var parts []string
	var cur strings.Builder
	depth := 0 // inside $(...) nesting
	inBacktick := false
	inSingle, inDouble := false, false
	for i := 0; i < len(cmd); i++ {
		c := cmd[i]
		switch {
		case c == '\'' && !inDouble:
			inSingle = !inSingle
		case c == '"' && !inSingle:
			inDouble = !inDouble
		case (inSingle || inDouble):
			// quoted content: no operator handling
		case c == '`':
			inBacktick = !inBacktick
		case c == '$' && i+1 < len(cmd) && cmd[i+1] == '(' && !inBacktick:
			depth++
			cur.WriteByte(c)
			cur.WriteByte(cmd[i+1])
			i++
			continue
		case c == ')' && depth > 0 && !inBacktick:
			depth--
		case depth > 0 || inBacktick:
			// inside substitution: separators are inert
		case c == '&' && i+1 < len(cmd) && cmd[i+1] == '&', c == ';':
			parts = append(parts, cur.String())
			cur.Reset()
			if c == '&' {
				i++ // consume the second '&'
			}
			continue
		case c == '|' && i+1 < len(cmd) && cmd[i+1] == '|':
			parts = append(parts, cur.String())
			cur.Reset()
			i++
			continue
		case c == '\n':
			parts = append(parts, cur.String())
			cur.Reset()
			continue
		}
		cur.WriteByte(c)
	}
	parts = append(parts, cur.String())
	// Drop empties (trailing separators, blank lines).
	out := parts[:0]
	for _, p := range parts {
		if strings.TrimSpace(p) != "" {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return []string{cmd}
	}
	return out
}

// scoreShellComplexityChained (#4780 / S-09): score a compound command
// PER SUBCOMMAND and take the maximum, instead of scoring the whole string.
// The whole-string aggregation rejected entire multi-action commands
// (merge + comment + patrol) whose combined argv crossed the threshold even
// though every step was individually safe - forcing costly step-splitting.
// Per-sub max keeps the gate's promise (a genuinely heavy subcommand still
// fires: hard tokens, substitution density, and pipeline depth are all
// counted within their own segment) while safe read-only verbs chained
// together no longer aggregate. The destructive-pattern layer
// (reversibility_check) is separate and still matches anywhere in the
// string.
func scoreShellComplexityChained(cmd string) int {
	subs := splitShellSubcommands(cmd)
	if len(subs) <= 1 {
		return scoreShellComplexity(cmd)
	}
	max := 0
	for _, sub := range subs {
		if n := scoreShellComplexity(sub); n > max {
			max = n
		}
	}
	return max
}

// scoreShellComplexity scores a shell command: length, pipelines (capped:
// build/test plumbing legitimately chains 5-8 stages), command substitution
// (dialect-aware for backticks), and destructive-token tiers.
func scoreShellComplexity(cmd string) int {
	ps := shellLooksPowerShell(cmd)
	n := 0
	if len(cmd) > 400 {
		n += 2
	} else if len(cmd) > 200 {
		n += 1
	}
	// #4647: "a || b" contributes 2 '|' characters but 0 pipelines -
	// subtract the logical-or pairs so they are not double-counted.
	pipes := strings.Count(cmd, "|") - 2*strings.Count(cmd, "||")
	if pipes > 4 {
		n += 4
	} else {
		n += pipes
	}
	subs := strings.Count(cmd, "$(")
	if !ps {
		subs += strings.Count(cmd, "`") // PS: backtick is an escape, not substitution
	}
	if subs > 2 {
		n += 3
	} else {
		n += subs
	}
	hard := 0
	for _, tok := range shellHardTokens {
		if hard < 16 && strings.Contains(cmd, tok) {
			hard += 8
		}
	}
	// Bare Remove-Item (no -Force suffix pair) still warrants a mid weight.
	if ps && hard == 0 && strings.Contains(cmd, "Remove-Item") {
		hard += 5
	}
	soft := 0
	for _, tok := range shellSoftTokens {
		if soft < 6 && strings.Contains(cmd, tok) {
			soft += 2
		}
	}
	n += hard + soft
	return n
}

// scoreFileOpsDeletes scores file_ops payloads by recursive deletes.
func scoreFileOpsDeletes(args map[string]json.RawMessage) int {
	raw, ok := args["operations"]
	if !ok {
		return 0
	}
	var ops []struct {
		Action    string `json:"action"`
		Recursive bool   `json:"recursive"`
	}
	if json.Unmarshal(raw, &ops) != nil {
		return 0
	}
	n := 0
	for _, op := range ops {
		if op.Action == "delete" {
			if op.Recursive {
				n += 3
			} else {
				n += 1
			}
		}
	}
	return n
}

// argComplexityIntentKey hashes tool + a NORMALIZED PROJECTION of the heavy
// parameters (defect B fix). The projection depends only on the values that
// drove the score, bucketed where prose jitter is irrelevant, so a resent
// call with different key order / whitespace / added description / reworded
// (but same-intent) arguments still memo-hits and passes.
func argComplexityIntentKey(toolName string, call map[string]json.RawMessage) string {
	str := func(key string) string {
		var s string
		_ = json.Unmarshal(call[key], &s)
		return s
	}
	proj := map[string]any{}
	switch toolName {
	case "run_command":
		proj["command"] = strings.TrimSpace(str("command"))
	case "edit_file":
		old := str("old_text")
		bucket := 0
		if c := strings.Count(old, "\n"); c >= 200 {
			bucket = 2
		} else if c >= 80 {
			bucket = 1
		}
		var replaceAll bool
		_ = json.Unmarshal(call["replace_all"], &replaceAll)
		proj["old_bucket"] = bucket
		proj["new_empty"] = strings.TrimSpace(str("new_text")) == ""
		proj["replace_all"] = replaceAll
		proj["file_path"] = strings.TrimSpace(str("file_path"))
	case "multi_edit_file", "multi_file_edit":
		// #4875: shape-aware aggregation (see parseMultiEditsForComplexity)
		// - the old top-level-only read made every multi_file_edit call share
		// one constant intent key (0 edits, 0 deletes).
		edits := parseMultiEditsForComplexity(call)
		deletes := 0
		for _, e := range edits {
			if strings.TrimSpace(e.NewText) == "" && strings.TrimSpace(e.OldText) != "" {
				deletes++
			}
		}
		proj["edits"] = len(edits)
		proj["deletes"] = deletes
	case "write_file", "multi_file_write":
		bucket := 0
		if strings.Count(str("content"), "\n") >= 200 {
			bucket = 1
		}
		proj["lines_bucket"] = bucket
		if toolName == "multi_file_write" {
			var files []json.RawMessage
			_ = json.Unmarshal(call["files"], &files)
			proj["files"] = len(files)
		}
	case "batch_replace":
		var files []json.RawMessage
		_ = json.Unmarshal(call["files"], &files)
		proj["files"] = len(files)
	case "file_ops":
		var ops []struct {
			Action    string `json:"action"`
			Recursive bool   `json:"recursive"`
		}
		_ = json.Unmarshal(call["operations"], &ops)
		type opShape struct {
			Action    string
			Recursive bool
		}
		shapes := make([]opShape, 0, len(ops))
		for _, op := range ops {
			shapes = append(shapes, opShape{op.Action, op.Recursive})
		}
		proj["ops"] = shapes
	case "git_reset":
		proj["hard"] = strings.Contains(str("mode"), "hard")
	default:
		// No projection: canonicalize the whole call minus volatile fields.
		// json.Marshal of a map sorts keys, giving a stable byte form.
		canon := make(map[string]json.RawMessage, len(call))
		for k, v := range call {
			if k == argRationaleField || k == "description" {
				continue
			}
			canon[k] = v
		}
		proj["_canonical"] = canon
	}
	b, err := json.Marshal(proj)
	if err != nil {
		b = []byte(toolName)
	}
	return hashArgIntent(toolName, b)
}

func hashArgIntent(toolName string, proj []byte) string {
	h := sha256.Sum256(append([]byte(toolName+"\x00"), proj...))
	return hex.EncodeToString(h[:])
}

// preflightArgComplexityCheck runs beside preflightRequiredCheck in
// safeExecute. Returns a non-nil Result when a high-complexity write call
// must first justify its parameters (one round-trip, then passes).
func preflightArgComplexityCheck(gate *argComplexityGateState, t tool.Tool, args json.RawMessage) *tool.Result {
	name := t.Name()
	if !argComplexityWriteTools[name] || len(args) == 0 {
		return nil
	}
	var call map[string]json.RawMessage
	if err := json.Unmarshal(args, &call); err != nil {
		return nil // malformed: the tool's own parse will speak
	}
	if gate == nil {
		return nil // stateless caller (tests of other detectors): pass
	}
	// A justification already attached: pass (and record it for
	// observability). Checked before scoring on purpose.
	var rationale string
	_ = json.Unmarshal(call[argRationaleField], &rationale)
	if strings.TrimSpace(rationale) != "" {
		debug.Log("agent", "arg-complexity gate satisfied by %s rationale (%d chars)", name, len(rationale))
		return nil
	}
	// Same normalized intent already burned its justification round: pass.
	key := argComplexityIntentKey(name, call)
	gate.mu.Lock()
	_, fired := gate.fired[key]
	budgetLeft := gate.fires < argComplexityMaxFires
	gate.mu.Unlock()
	if fired {
		return nil
	}
	score, heavy := scoreArgComplexity(name, call)
	if score < argComplexityThresholdFor(name) {
		return nil
	}
	// Budget exhausted: fail open (log-only). A wrong score must cost
	// round-trips, never trap the run.
	if !budgetLeft {
		debug.Log("agent", "arg-complexity gate budget exhausted (%d fires); passing %s score=%d", argComplexityMaxFires, name, score)
		return nil
	}
	gate.mu.Lock()
	gate.fired[key] = struct{}{}
	gate.fires++
	gate.mu.Unlock()
	debug.Log("agent", "preflight arg-complexity gate fired on %s: score=%d heavy=%v", name, score, heavy)
	r := tool.Result{IsError: true, Content: formatArgRationaleRequest(name, score, heavy)}
	return &r
}

// stripArgRationale removes the gate-only justification field from the call
// before dispatch so the tool never sees it (Go decoding would ignore it,
// but keeping it out of audit payloads and tool logs is cleaner).
func stripArgRationale(args json.RawMessage) json.RawMessage {
	if len(args) == 0 || !strings.Contains(string(args), argRationaleField) {
		return args
	}
	var call map[string]json.RawMessage
	if err := json.Unmarshal(args, &call); err != nil {
		return args
	}
	if _, ok := call[argRationaleField]; !ok {
		return args
	}
	delete(call, argRationaleField)
	b, err := json.Marshal(call)
	if err != nil {
		return args
	}
	return b
}

// formatArgRationaleRequest renders the one-retry-round justification error.
func formatArgRationaleRequest(toolName string, score int, heavy []string) string {
	var sb strings.Builder
	sb.WriteString("high-impact call: complexity score ")
	sb.WriteString(strconv.Itoa(score))
	sb.WriteString(" (threshold ")
	sb.WriteString(strconv.Itoa(argComplexityThresholdFor(toolName)))
	sb.WriteString("), heavy parameters: ")
	if len(heavy) == 0 {
		sb.WriteString("(unspecified)")
	} else {
		sb.WriteString(strings.Join(dedupeStrings(heavy), ", "))
	}
	sb.WriteString(".\n\nA retry of this call is pre-authorized (the gate never blocks the same intent twice). To skip the reject round on future high-impact calls, attach an optional \"")
	sb.WriteString(argRationaleField)
	sb.WriteString("\" string field briefly justifying each heavy parameter choice (why this target, why this scope, why now). This is a one-round think-before-write check (arXiv:2601.18282).")
	return sb.String()
}

func dedupeStrings(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}
