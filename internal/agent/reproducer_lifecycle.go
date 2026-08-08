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
	"fmt"
	"regexp"
	"strings"
	"sync"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/provider"
)

const (
	reproLifecycleMaxWarnings = 1 // max warnings per run

	// reproducerFertilityWindow: how many iterations after a reproducer run
	// we consider the agent "in the edit phase" and expect a re-run.
	reproducerFertilityWindow = 8
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

// editToolNames identifies tools that modify source files.
var reproducerEditToolNames = map[string]bool{
	"edit_file":       true,
	"multi_edit_file": true,
	"write_file":      true,
	"multi_file_edit": true,
}

// runToolNames identifies tools that execute commands (potential re-runs).
var reproducerRunToolNames = map[string]bool{
	"run_command":   true,
	"start_command": true,
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
			if reproducerRunToolNames[tn] && reproducerCommandRe.MatchString(inp) {
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

		// Phase 3: detect re-run after edit.
		if s.editedAfterReproducer && !s.reranAfterEdit {
			if reproducerRunToolNames[tn] {
				s.reranAfterEdit = true
				debug.Log("agent", "reproducer-lifecycle: re-run after edit at iter %d", iteration)
			}
		}
	}
}

// observeText scans the assistant text for reproducer intent (Phase 1 alt path).
func (s *reproducerLifecycleState) observeText(iteration int, text string, hasRunTool bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.hasReproducer && reproducerIntentRe.MatchString(text) && hasRunTool {
		s.hasReproducer = true
		s.reproducerIteration = iteration
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
	// Only warn if: reproducer established, code edited after, NOT re-run,
	// and we're past the fertility window from the edit.
	if !s.hasReproducer || !s.editedAfterReproducer || s.reranAfterEdit {
		return ""
	}
	if iteration-s.editIteration < 2 {
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

// firstLine returns the first line of a (possibly multi-line) string, trimmed.
func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if pos := strings.IndexByte(s, '\n'); pos >= 0 {
		s = s[:pos]
	}
	if len(s) > 80 {
		s = s[:77] + "..."
	}
	return s
}
