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
//   - Deterministic, zero-LLM complexity heuristics per write tool family
//     (long/piped/substituting shell commands, huge edit anchors, bulk
//     multi-edit batches, destructive delete forms).
//   - score >= threshold AND write-class tool AND no rationale attached:
//     reject with an error that names the heavy parameters and asks the
//     model to resend with an `arg_rationale` field explaining each.
//   - The very next resend passes unconditionally for the SAME argument
//     hash (fired-once map): the model either justifies or insists, but it
//     can never be trapped in a retry loop on identical arguments.
//   - Read-only tools never trigger (their complexity is cheap to undo).

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

// argComplexityThreshold is the score at which a write-class call must carry
// an arg_rationale. Calibrated so ordinary single-file edits and short
// commands pass untouched while bulk deletes / long piped chains / huge
// anchors trigger.
const argComplexityThreshold = 4

// argRationaleField is the extra argument the resent call must carry. Any
// non-blank value passes: the point is the justification round-trip, not
// grading the prose.
const argRationaleField = "arg_rationale"

// argComplexityFired tracks argument hashes that already burned their
// justification round. Package-level (like the schema cache) so retries from
// any goroutine share the memo. Bounded in practice by the per-run fire
// budget below.
var argComplexityFired sync.Map // map[string]struct{}

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

// scoreArgComplexity returns a deterministic complexity score for the call
// plus the parameter names that carried the weight (for the error message).
// Unknown tools score 0.
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
		if n := scoreShellComplexity(cmd); n > 0 {
			add(n, "command")
		}
	case "edit_file":
		old := str("old_text")
		switch {
		case strings.Count(old, "\n") >= 100:
			add(4, "old_text")
		case strings.Count(old, "\n") >= 30:
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
		raw, ok := args["edits"]
		if !ok {
			break
		}
		var edits []struct {
			OldText string `json:"old_text"`
			NewText string `json:"new_text"`
		}
		if json.Unmarshal(raw, &edits) != nil {
			break
		}
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
	}
	return score, heavy
}

// scoreShellComplexity scores a shell command: length, pipelines, command
// substitution, and known-destructive tokens.
func scoreShellComplexity(cmd string) int {
	n := 0
	if len(cmd) > 400 {
		n += 2
	} else if len(cmd) > 200 {
		n += 1
	}
	n += strings.Count(cmd, "|")
	if subs := strings.Count(cmd, "$(") + strings.Count(cmd, "`"); subs > 2 {
		n += 3
	} else {
		n += subs
	}
	danger := 0
	for _, tok := range []string{"rm -rf", "git reset --hard", "git push --force", "curl", "wget", "chmod 777", "sudo"} {
		if danger < 6 && strings.Contains(cmd, tok) {
			danger += 3
		}
	}
	n += danger
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

// argComplexityKey hashes tool+args so only the IDENTICAL call is memoized.
func argComplexityKey(toolName string, args json.RawMessage) string {
	h := sha256.Sum256(append([]byte(toolName+"\x00"), args...))
	return hex.EncodeToString(h[:])
}

// preflightArgComplexityCheck runs beside preflightRequiredCheck in
// safeExecute. Returns a non-nil Result when a high-complexity write call
// must first justify its parameters (one round-trip, then passes).
func preflightArgComplexityCheck(t tool.Tool, args json.RawMessage) *tool.Result {
	name := t.Name()
	if !argComplexityWriteTools[name] || len(args) == 0 {
		return nil
	}
	var call map[string]json.RawMessage
	if err := json.Unmarshal(args, &call); err != nil {
		return nil // malformed: the tool's own parse will speak
	}
	// A justification already attached (or the memoized second identical
	// resend): pass.
	key := argComplexityKey(name, args)
	if _, fired := argComplexityFired.Load(key); fired {
		return nil
	}
	var rationale string
	_ = json.Unmarshal(call[argRationaleField], &rationale)
	if strings.TrimSpace(rationale) != "" {
		return nil
	}
	score, heavy := scoreArgComplexity(name, call)
	if score < argComplexityThreshold {
		return nil
	}
	argComplexityFired.Store(key, struct{}{})
	debug.Log("agent", "preflight arg-complexity gate fired on %s: score=%d heavy=%v", name, score, heavy)
	r := tool.Result{IsError: true, Content: formatArgRationaleRequest(name, score, heavy)}
	return &r
}

// formatArgRationaleRequest renders the one-retry-round justification error.
func formatArgRationaleRequest(toolName string, score int, heavy []string) string {
	var sb strings.Builder
	sb.WriteString("high-impact call: complexity score ")
	sb.WriteString(strconv.Itoa(score))
	sb.WriteString(" (threshold ")
	sb.WriteString(strconv.Itoa(argComplexityThreshold))
	sb.WriteString("), heavy parameters: ")
	if len(heavy) == 0 {
		sb.WriteString("(unspecified)")
	} else {
		sb.WriteString(strings.Join(dedupeStrings(heavy), ", "))
	}
	sb.WriteString(".\n\nBefore dispatching, resend the SAME call with an added \"")
	sb.WriteString(argRationaleField)
	sb.WriteString("\" string field that briefly justifies each heavy parameter choice (why this target, why this scope, why now). The justified resend executes immediately.\n\nThis is a one-round think-before-write check (arXiv:2601.18282); it never blocks the same call twice.")
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
