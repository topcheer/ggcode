package agent

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

const (
	argComplexityDefaultThreshold = 4
	argComplexityRunCmdThreshold  = 18
)

const argComplexityMaxFires = 3

const argRationaleField = "arg_rationale"

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

type argComplexityGateState struct {
	mu    sync.Mutex
	fired map[string]struct{}
	fires int
}

func newArgComplexityGateState() *argComplexityGateState {
	return &argComplexityGateState{fired: make(map[string]struct{})}
}

func (s *argComplexityGateState) reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.fired = make(map[string]struct{})
	s.fires = 0
}

func argComplexityThresholdFor(name string) int {
	if name == "run_command" {
		return argComplexityRunCmdThreshold
	}
	return argComplexityDefaultThreshold
}

type editPair struct {
	OldText string `json:"old_text"`
	NewText string `json:"new_text"`
}

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

		if b, ok := args["create"]; ok {
			var bval bool
			if json.Unmarshal(b, &bval) == nil && bval && str("start_point") != "" {
				add(2, "start_point")
			}
		}
	case "git_add":

		if raw, ok := args["files"]; ok {
			var files []string
			if json.Unmarshal(raw, &files) == nil && (len(files) >= 10 || (len(files) == 1 && files[0] == ".")) {
				add(2, "files")
			}
		}
	case "git_commit":

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

func shellLooksPowerShell(cmd string) bool {
	for _, m := range []string{"$env:", "-Recurse", "Invoke-", "Remove-Item", "Set-ExecutionPolicy", "iwr ", "Format-Volume", "Get-ChildItem", "Out-Null"} {
		if strings.Contains(cmd, m) {
			return true
		}
	}
	return false
}

var shellHardTokens = []string{
	"rm -rf",
	"git reset --hard",

	"Remove-Item -Recurse -Force",
	"Format-Volume",
	"reg delete",
}

var shellSoftTokens = []string{
	"curl",
	"wget",
	"chmod 777",
	"sudo",
	"Invoke-WebRequest",
	"iwr",
	"Set-ExecutionPolicy Bypass",
}

func splitShellSubcommands(cmd string) []string {
	if !strings.ContainsAny(cmd, "&;\n") {
		return []string{cmd}
	}
	var parts []string
	var cur strings.Builder
	depth := 0
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

		case c == '&' && i+1 < len(cmd) && cmd[i+1] == '&', c == ';':
			parts = append(parts, cur.String())
			cur.Reset()
			if c == '&' {
				i++
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

func pushForceScore(cmd string) (hard, mid int) {
	fields := strings.Fields(cmd)
	for i := 1; i < len(fields); i++ {
		if fields[i] != "push" || fields[i-1] != "git" {
			continue
		}
		for _, t := range fields[i+1:] {
			if t == "&&" || t == "||" || t == ";" {
				break
			}
			if t == "--force" {
				return 8, 0
			}
			if t == "--force-with-lease" || strings.HasPrefix(t, "--force-with-lease=") {
				return 0, 4
			}
			if strings.HasPrefix(t, "+") {
				return 8, 0
			}
			if strings.HasPrefix(t, "-") && !strings.HasPrefix(t, "--") && strings.Contains(t, "f") {
				return 8, 0
			}
		}
	}
	return 0, 0
}

func scoreShellComplexity(cmd string) int {
	ps := shellLooksPowerShell(cmd)
	n := 0
	if len(cmd) > 400 {
		n += 2
	} else if len(cmd) > 200 {
		n += 1
	}

	pipes := strings.Count(cmd, "|") - 2*strings.Count(cmd, "||")
	if pipes > 4 {
		n += 4
	} else {
		n += pipes
	}
	subs := strings.Count(cmd, "$(")
	if !ps {
		subs += strings.Count(cmd, "`")
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

	if pfHard, pfMid := pushForceScore(cmd); pfHard > 0 || pfMid > 0 {
		if hard < 16 {
			hard += pfHard
		}
		hard += pfMid
	}

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

func preflightArgComplexityCheck(gate *argComplexityGateState, t tool.Tool, args json.RawMessage) *tool.Result {
	name := t.Name()
	if !argComplexityWriteTools[name] || len(args) == 0 {
		return nil
	}
	var call map[string]json.RawMessage
	if err := json.Unmarshal(args, &call); err != nil {
		return nil
	}
	if gate == nil {
		return nil
	}
	var rationale string
	_ = json.Unmarshal(call[argRationaleField], &rationale)
	if strings.TrimSpace(rationale) != "" {
		debug.Log("agent", "arg-complexity gate satisfied by %s rationale (%d chars)", name, len(rationale))
		return nil
	}

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

// extractArgRationale pulls the arg_rationale text out of raw tool-call
// args (pre-strip) for persistence into the audit ledger (#decision-audit).
// Capped at 500 runes; malformed JSON or a missing field yields "".
func extractArgRationale(args json.RawMessage) string {
	if len(args) == 0 || !strings.Contains(string(args), argRationaleField) {
		return ""
	}
	var call map[string]json.RawMessage
	if err := json.Unmarshal(args, &call); err != nil {
		return ""
	}
	raw, ok := call[argRationaleField]
	if !ok {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return ""
	}
	runes := []rune(s)
	if len(runes) > 500 {
		runes = runes[:500]
	}
	return strings.TrimSpace(string(runes))
}

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
