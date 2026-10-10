package agent

// Reproducer Lifecycle Tracker
//
// Research basis:
//   - Anthropic SWE-bench Sonnet engineering blog (2025): the recommended
//     workflow is explicitly "reproduce the error -> edit -> rerun the
//     reproduce script to confirm the error is fixed." The #1 cited reason
//     agents fail SWE-bench is that they "think they have succeeded when the
//     task actually is a failure" -- because they skip re-running the
//     reproducer after editing.
//   - SWE-agent / OpenHands analysis (2025): a significant fraction of failed
//     agent runs involve edits that are never validated against the original
//     reproduction steps, leaving silent regressions.
//   - Test-gaming / phantom-verify research: agents that claim success without
//     re-running the actual reproducer are a known failure class. Existing
//     detectors (phantom_verify, test_gaming) check generic test claims, but
//     NONE specifically track the reproduce-edit-rerun lifecycle.
//
// Problem: AI coding agents frequently:
//   1. Create or run a reproducer script demonstrating a bug
//   2. Edit the source code to fix the bug
//   3. Forget to re-run the reproducer, OR claim the bug is fixed without
//      ever re-running it
//
// This detector tracks that lifecycle across iterations and injects guidance
// when the agent edits code after running a reproducer but never re-runs it.
//
// Existing ggcode detectors that are RELATED but do NOT cover this:
//   - phantom_verify.go: checks generic "I tested this" claims without evidence
//   - test_gaming.go: checks if tests were actually run
//   - fulfillment_gate.go: checks if stated goals were met
//   - edit_fail_recovery.go: checks for failed edits needing retries
//   NONE of these track the specific reproduce->edit->rerun lifecycle.
//
// Design:
//   - Phase 1 (REPRO): detects when the agent runs/creates a reproducer
//     (run_command with python/node/go test script, or text mentioning
//     "reproduce", "reproducer", "repro script", "demonstrate the error")
//   - Phase 2 (EDIT): detects when the agent edits source files AFTER a
//     reproducer has been established
//   - Phase 3 (RERUN): detects when the agent re-runs a command after the edit
//   - If we reach EDIT but never RERUN before the run ends, inject guidance
//   - Zero LLM cost -- pure deterministic state machine
//   - Fires at most once per run (advisory, non-blocking)

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"sync"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/provider"
)

const (
	reproLifecycleMaxWarnings = 1 // max warnings per run

	reproducerRerunGraceIterations = 2 // iterations to wait after edit before warning

	// commandTokenMinLen: minimum length of a command token to count for
	// overlap matching (filters out short generic words).
	commandTokenMinLen = 3
)

// reproducerLifecycleState tracks the reproduce->edit->rerun lifecycle.
type reproducerLifecycleState struct {
	mu sync.Mutex

	// hasReproducer: true once the agent has run/created a reproducer.
	hasReproducer bool
	// reproducerIteration: the iteration where the reproducer was established.
	reproducerIteration int
	// reproducerCommand: the command/text used to run the reproducer.
	reproducerSnippet string
	// editedAfterReproducer: true if the agent edited source files after
	// establishing a reproducer.
	editedAfterReproducer bool
	// editIteration: the iteration where the post-reproducer edit happened.
	editIteration int
	// reranAfterEdit: true if the agent re-ran a command after the edit.
	reranAfterEdit bool
	// warned: whether we've already injected a warning this run.
	warned bool
	// intentSeenIter: latest iteration whose assistant text matched
	// reproducerIntentRe (#3795-3). The command-establishment path requires
	// intent in the same iteration so one-shot scripts (setup.py,
	// version_sync.sh) without any reproduce wording no longer count as
	// "establishing a reproducer". 0 = no intent seen yet.
	intentSeenIter int
}

func newReproducerLifecycleState() *reproducerLifecycleState {
	return &reproducerLifecycleState{}
}

func (s *reproducerLifecycleState) reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.hasReproducer = false
	s.reproducerIteration = 0
	s.reproducerSnippet = ""
	s.editedAfterReproducer = false
	s.editIteration = 0
	s.intentSeenIter = 0
	s.reranAfterEdit = false
	s.warned = false
}

// reproducerIntentRe detects the agent describing a reproducer in its text.
// Matches phrases like:
//
//	"Let me create a reproducer script"
//	"I'll write a script to reproduce the error"
//	"running the reproduction"
//	"repro script to demonstrate the bug"
var reproducerIntentRe = regexp.MustCompile(
	`(?i)\b(?:reproduc(?:e[rd]?|ing|tion)|repro\s+script|reproducer|demonstrate\s+(?:the\s+)?(?:error|bug|issue|crash)|minimal\s+(?:repro|example|test\s+case))\b`,
)

// reproducerCommandRe detects run_command invocations that look like they run
// a standalone script (the most common reproducer pattern).
var reproducerCommandRe = regexp.MustCompile(
	`(?:^|[\s:"])(?:python3?|node|go\s+run|ruby|cargo\s+run|bash|sh)\s+\S+\.(?:py|js|ts|go|rb|rs|sh)`,
)

// reproducerTestRunnerRe detects run_command invocations that execute a test
// runner (go test / cargo test / make test / npm test / pytest) rather than a
// standalone script (#2805). The "write a test to reproduce" workflow has no
// script path for reproducerCommandRe to anchor on, so text-established
// reproducers whose same-iteration run is test-runner-shaped used to leave
// the snippet empty and could never discharge the re-run obligation.
var reproducerTestRunnerRe = regexp.MustCompile(
	`(?:^|[\s:"])(?:go\s+test|cargo\s+test|make\s+test|npm\s+test|pytest)\b`,
)

// reproducerEditToolNames identifies tools that modify source files.
// Aliased to the canonical sourceMutatingTools superset (#738).
var reproducerEditToolNames = sourceMutatingTools

// runToolNames identifies tools that execute commands (potential re-runs).
var reproducerRunToolNames = map[string]bool{
	"run_command":   true,
	"start_command": true,
}

// reproducerRerunMatches reports whether a run tool input qualifies as a
// re-run of the reproducer itself (#2752). When the reproducer was
// established via an explicit command (snippet non-empty), only a command
// that shares a distinctive token with the recorded snippet qualifies —
// running an UNRELATED script (e.g. `node test/unit/foo.test.js`) must not
// discharge the re-run obligation (#2802). The loose script-shape match is
// kept only for text-established reproducers, where no command was recorded
// to compare against.
func reproducerRerunMatches(inp, snippet string) bool {
	if inp == "" {
		return false
	}
	if snippet == "" {
		// #3795-2: text-established reproducers whose command shape is a
		// test runner (`go test ./... -run TestX`, #2805's exact case) also
		// land here; the script-only fallback could never discharge them.
		return reproducerCommandRe.MatchString(inp) || reproducerTestRunnerRe.MatchString(inp)
	}
	return reproCommandTokenOverlap(inp, snippet)
}

// reproCommandTokenOverlap checks whether the two command strings share a
// distinctive script/path token (e.g. both reference `repro.py`).
func reproCommandTokenOverlap(a, b string) bool {
	tokensA := reproCommandTokens(a)
	tokensB := reproCommandTokens(b)
	if len(tokensA) == 0 || len(tokensB) == 0 {
		return false
	}
	for ta := range tokensA {
		if tokensB[ta] {
			return true
		}
	}
	return false
}

// reproCommandTokens splits a command string into lowercase tokens suitable
// for overlap matching. Fields are additionally split on path separators so
// `./cmd/reprogo/main.go` and `go run ./cmd/reprogo` share `reprogo`.
// Generic shell verbs, flags, and common directory names are dropped so
// overlap means script/argument identity rather than generic words.
func reproCommandTokens(s string) map[string]bool {
	// #2827: run_command tool inputs arrive as a raw JSON envelope
	// ("command":"python3 x.py"). The cutset below has no braces, so the
	// first token of a space-less envelope head became the pseudo-token
	// `{"command":"python3` -- shared by EVERY python3 command, letting any
	// unrelated run discharge the re-run obligation (#2802 bypass). Unwrap
	// the envelope first, mirroring reversibility_check.commandTokens.
	if t, handled := reproUnwrapCommandEnvelope(s); handled {
		s = t
	}
	generic := map[string]bool{
		"and": true, "the": true, "run": true, "bash": true, "sh": true,
		"python": true, "python3": true, "node": true, "ruby": true,
		"cargo": true, "go": true, "test": true, "tests": true, "cd": true,
		"echo": true, "make": true, "cmd": true, "src": true, "pkg": true,
		"internal": true, "desktop": true, "main": true, "github.com": true,
		"github": true, "www": true, "head": true, "git": true, "diff": true,
	}
	tokens := make(map[string]bool)
	for _, field := range strings.Fields(strings.ToLower(s)) {
		for _, comp := range strings.Split(field, "/") {
			comp = strings.Trim(comp, "\"'`$();|&~.:{}[],")
			if len(comp) < commandTokenMinLen || strings.HasPrefix(comp, "-") {
				continue
			}
			if generic[comp] {
				continue
			}
			tokens[comp] = true
		}
	}
	return tokens
}

// reproUnwrapCommandEnvelope extracts the embedded command value when s is a
// JSON envelope like {"command":"python3 reproduce_bug.py"} (or a JSON array
// wrapper). Returns ("", false) when s is not an envelope (or is not
// parseable JSON) and the raw string should be tokenized as-is. Returns
// ("", true) for a parsed envelope carrying no usable command: argument
// keys ("timeout", "path", ...) must never become distinctive tokens
// (#2827 CI residual), so the caller tokenizes an empty string instead.
func reproUnwrapCommandEnvelope(s string) (string, bool) {
	trimmed := strings.TrimSpace(s)
	if !strings.HasPrefix(trimmed, "{") && !strings.HasPrefix(trimmed, "[") {
		return "", false
	}
	var env struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal([]byte(trimmed), &env); err != nil {
		return "", false
	}
	if env.Command != "" {
		return env.Command, true
	}
	// Parsed envelope without a command field: fall back to its string
	// values only (e.g. a description/comment envelope); if none, the
	// caller yields an empty token set rather than resurrecting keys.
	var m map[string]any
	if err := json.Unmarshal([]byte(trimmed), &m); err != nil {
		return "", true
	}
	var vals []string
	for _, v := range m {
		if str, ok := v.(string); ok && str != "" {
			vals = append(vals, str)
		}
	}
	return strings.Join(vals, " "), true
}

// observeToolCalls updates the lifecycle state based on the tools the agent
// invoked this iteration.
func (s *reproducerLifecycleState) observeToolCalls(iteration int, toolNames []string, toolInputs []string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for ti, tn := range toolNames {
		inp := ""
		if ti < len(toolInputs) {
			inp = toolInputs[ti]
		}

		// Phase 1: detect reproducer establishment.
		if !s.hasReproducer {
			// #3795-3: intent gate. The command shape alone classified ANY
			// script execution - `python setup.py`, `bash scripts/version_sync.sh`
			// (this repo's own release flow!) - as "established a reproducer",
			// and any later source edit + no re-run of that one-shot script
			// produced a systematic false "Re-run: bash scripts/version_sync.sh"
			// warning. Require reproducer intent in the SAME iteration's text
			// (observeText runs before observeToolCalls each iteration and
			// stamps s.intentSeenIter) - mirroring the text path, which was
			// already intent-gated by construction.
			if reproducerRunToolNames[tn] && s.intentSeenIter == iteration && reproducerCommandRe.MatchString(inp) {
				s.hasReproducer = true
				s.reproducerIteration = iteration
				s.reproducerSnippet = firstLine(inp)
				debug.Log("agent", "reproducer-lifecycle: reproducer established at iter %d (%s)", iteration, s.reproducerSnippet)
			}
		}

		// Phase 2: detect edits after reproducer was established.
		if s.hasReproducer && !s.editedAfterReproducer {
			if reproducerEditToolNames[tn] {
				s.editedAfterReproducer = true
				s.editIteration = iteration
				debug.Log("agent", "reproducer-lifecycle: edit after reproducer at iter %d", iteration)
			}
		}

		// Phase 3: detect re-run of the reproducer itself after edit (#2752).
		// A bare run_command (e.g. `git diff`, `ls`) must NOT discharge the
		// re-run obligation: the command must either match the reproducer
		// script shape or resemble the recorded reproducer snippet.
		if s.editedAfterReproducer && !s.reranAfterEdit {
			if reproducerRunToolNames[tn] && reproducerRerunMatches(inp, s.reproducerSnippet) {
				s.reranAfterEdit = true
				debug.Log("agent", "reproducer-lifecycle: re-run after edit at iter %d", iteration)
			}
		}
	}
}

// observeText scans the assistant text for reproducer intent (Phase 1 alt path).
// runInput is the raw input of the iteration's first command-executing tool
// call ("" when none ran); #2805 uses it to record a snippet for
// test-runner-shaped reproducers so the token-overlap discharge channel works
// and the "Re-run:" hint tail is not blank.
func (s *reproducerLifecycleState) observeText(iteration int, text string, hasRunTool bool, runInput string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// #3795-3: stamp intent regardless of establishment state so the command
	// path in observeToolCalls (same iteration) can require it.
	if reproducerIntentRe.MatchString(text) {
		s.intentSeenIter = iteration
	}

	if !s.hasReproducer && reproducerIntentRe.MatchString(text) && hasRunTool {
		s.hasReproducer = true
		s.reproducerIteration = iteration
		// #2805: `go test ./pkg/ -run TestX` (and friends) never match
		// reproducerCommandRe, so the text path used to leave the snippet
		// empty and an identical re-run after an edit was reported as
		// "not re-run" with a blank "Re-run:" tail. Record the extracted
		// command as the snippet so token overlap discharges an identical
		// (or same-package) re-run while unrelated scripts still do not.
		// Script-shaped runs keep prior behavior: they establish via the
		// command path in observeToolCalls, which records the snippet there.
		if cmd := extractStringField(json.RawMessage(runInput), "command"); cmd != "" && reproducerTestRunnerRe.MatchString(cmd) {
			s.reproducerSnippet = firstLine(cmd)
		}
		debug.Log("agent", "reproducer-lifecycle: reproducer established via text at iter %d", iteration)
	}
}

// checkIncomplete is called near the end of the run (or when the agent claims
// completion). If the agent established a reproducer, edited code, but never
// re-ran the reproducer, it injects guidance.
func (s *reproducerLifecycleState) checkIncomplete(iteration int) string {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.warned {
		return ""
	}
	// Only warn if: reproducer established, code edited after, and the
	// reproducer itself has NOT been re-run. Wait a grace period after the
	// edit so the agent has a chance to re-run it.
	if !s.hasReproducer || !s.editedAfterReproducer || s.reranAfterEdit {
		return ""
	}
	if iteration-s.editIteration < reproducerRerunGraceIterations {
		return "" // give the agent a chance to re-run
	}

	s.warned = true
	hint := "[reproducer-lifecycle] You established a reproducer (iteration " +
		fmt.Sprintf("%d", s.reproducerIteration) + ") and then " +
		"edited source code (iteration " + fmt.Sprintf("%d", s.editIteration) + "), but the reproducer has not been re-run " +
		"to confirm the fix. Per SWE-bench best practices (Anthropic, 2025), always " +
		"re-run your reproducer script after editing to verify the error is actually " +
		"resolved before claiming success. Re-run: " + s.reproducerSnippet
	return hint
}

// extractToolNamesAndInputs extracts tool names and their raw argument text
// from a list of tool calls, for lifecycle observation.
func extractToolNamesAndInputs(toolCalls []provider.ToolCallDelta) ([]string, []string) {
	names := make([]string, 0, len(toolCalls))
	inputs := make([]string, 0, len(toolCalls))
	for _, tcd := range toolCalls {
		names = append(names, tcd.Name)
		inputs = append(inputs, string(tcd.Arguments))
	}
	return names, inputs
}

// firstLine returns the first line of a (possibly multi-line) string that
// is not blank and not a `#` comment, trimmed. #3795-1: run_command inputs
// routinely start with a `# purpose` line (the #2244 convention that
// causal_attribution already follows); returning that comment as the
// snippet made the "Re-run:" hint show a bare comment and broke rerun token
// overlap (comment words share no token with the actual command).
func firstLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if len(line) > 80 {
			line = line[:77] + "..."
		}
		return line
	}
	return ""
}
