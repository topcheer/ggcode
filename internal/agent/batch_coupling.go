package agent

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/topcheer/ggcode/internal/debug"
)

// Batch Tool Call Coupling Detector -- Parallel Call Hidden Dependency Awareness
//
// Research basis:
//   - "Parallel Tool Calls in LLM Agents: The Coupling Test You Didn't Know"
//     (tianpan.co, 2026-04): analyzes hidden order dependencies between parallel
//     function calls -- when agents batch tool calls, some have implicit sequential
//     dependencies that silently fail if executed concurrently or out of order.
//   - "Parallel Tool Calling and Execution Optimization in AI Agent Systems"
//     (zylos.ai, 2026-04): comprehensive analysis of scheduling strategies and
//     coupling hazards in tool-using AI agents.
//   - SICA (arXiv:2504.15228, NeurIPS 2025): trajectory waste from ill-sequenced
//     actions accounts for significant iteration loss.
//
// Problem: Modern LLMs can emit multiple tool calls in a single response for
// parallel execution. However, some pairs of calls have hidden sequential
// dependencies -- the second call depends on the side effect of the first.
// Examples:
//
//	mkdir("dir") + write_file("dir/file.go")   // write needs dir to exist
//	git_checkout("branch") + read_file(path)    // read depends on new branch state
//	edit_file("f.go") + read_file("f.go")       // read expects edited content
//	file_ops(mkdir) + edit_file("dir/new.go")   // edit needs dir to exist
//	git_add("f.go") + git_commit("msg")         // commit needs staged files
//
// If these calls execute in parallel or in reverse order, the dependent call
// will fail or produce stale results, wasting an iteration.
//
// What it detects: When a single LLM response batch contains 2+ tool calls
// where one call's target depends on another call's side effect (path prefix,
// same file, mutation->read ordering). It injects a guidance nudge to sequence
// the dependent call after the prerequisite completes.
//
// Distinct from existing detectors:
//   - action_annihilate.go: tracks cross-call CANCELLATION (net-zero). This
//     detector tracks cross-call DEPENDENCY (ordering matters).
//   - tool_sequence.go: tracks suboptimal ordering across iterations. This
//     tracks ordering within a SINGLE batch.
//   - parallel_tool_exec (agentruntime): handles execution mechanics. This
//     detector provides SEMANTIC awareness of coupling to the LLM.

// couplingRule defines a hidden dependency between two tool calls in a batch.
type couplingRule struct {
	// prereqTool is the tool whose side effect the dependent call needs.
	prereqTool string
	// dependentTool is the tool that depends on the prerequisite's effect.
	dependentTool string
	// matchFn returns true if the two calls' args confirm a dependency.
	// If nil, any prereq->dependent pair within the batch matches.
	matchFn func(prereqArgs, depArgs json.RawMessage) bool
	// description for the guidance message.
	description string
}

// batchCouplingState tracks whether the coupling detector has fired this run.
type batchCouplingState struct {
	mu sync.Mutex

	// max warnings per run.
	maxWarns int
	// warns issued so far.
	warnsIssued int
}

func newBatchCouplingState() *batchCouplingState {
	return &batchCouplingState{
		maxWarns: 2,
	}
}

func (s *batchCouplingState) reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.warnsIssued = 0
}

// couplingRules defines known hidden dependency patterns within batches.
var couplingRules = []couplingRule{
	{
		prereqTool:    "file_ops",
		dependentTool: "write_file",
		description:   "file_ops(mkdir) + write_file to that directory (write needs the directory to exist first)",
		matchFn:       matchMkdirToFilePath,
	},
	{
		prereqTool:    "file_ops",
		dependentTool: "edit_file",
		description:   "file_ops(mkdir) + edit_file in that directory (edit needs the directory to exist first)",
		matchFn:       matchMkdirToFilePath,
	},
	{
		prereqTool:    "file_ops",
		dependentTool: "multi_edit_file",
		description:   "file_ops(mkdir) + multi_edit_file in that directory (edit needs the directory to exist first)",
		matchFn:       matchMkdirToFilePath,
	},
	{
		prereqTool:    "edit_file",
		dependentTool: "read_file",
		description:   "edit_file + read_file on the same file (read depends on edited content -- sequence them)",
		matchFn:       matchSameFilePath,
	},
	{
		prereqTool:    "multi_edit_file",
		dependentTool: "read_file",
		description:   "multi_edit_file + read_file on the same file (read depends on edited content)",
		matchFn:       matchSameFilePath,
	},
	{
		prereqTool:    "write_file",
		dependentTool: "read_file",
		description:   "write_file + read_file on the same file (read would get the content you just wrote -- consider skipping)",
		matchFn:       matchSameFilePath,
	},
	{
		prereqTool:    "git_checkout",
		dependentTool: "read_file",
		description:   "git_checkout + read_file (file content may differ on the target branch -- read AFTER checkout completes)",
		matchFn:       nil, // any checkout + read in same batch is suspect
	},
	{
		prereqTool:    "git_add",
		dependentTool: "git_commit",
		description:   "git_add + git_commit in the same batch (commit needs the staged files -- run add first)",
		matchFn:       nil,
	},
}

// couplingToolCall is a simplified view of a tool call for coupling analysis.
type couplingToolCall struct {
	name string
	args json.RawMessage
}

// checkBatchCoupling analyzes a batch of tool calls for hidden sequential
// dependencies. Returns a guidance message if coupling is detected, "" otherwise.
func (s *batchCouplingState) checkBatchCoupling(toolCalls []couplingToolCall) string {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.warnsIssued >= s.maxWarns {
		return ""
	}
	if len(toolCalls) < 2 {
		return ""
	}

	var findings []string
	seen := make(map[string]bool)

	for i, prereq := range toolCalls {
		for j, dep := range toolCalls {
			if i == j {
				continue
			}
			// #150: flag BOTH orderings. Parallel execution of the pair is
			// hazardous regardless of LLM listing order — if the dependent
			// call runs before the prerequisite completes, it operates on
			// stale/pre-change state (e.g. read_file placed before edit_file
			// still returns pre-edit content if the executor runs them
			// out of order). LLM emission order does not guarantee execution
			// order in parallel batches.
			for _, rule := range couplingRules {
				if rule.prereqTool != prereq.name || rule.dependentTool != dep.name {
					continue
				}
				if rule.matchFn != nil && !rule.matchFn(prereq.args, dep.args) {
					continue
				}

				key := rule.description
				if seen[key] {
					continue
				}
				seen[key] = true
				findings = append(findings, rule.description)
			}
		}
	}

	if len(findings) == 0 {
		return ""
	}

	s.warnsIssued++
	debug.Log("batch-coupling", "detected %d coupling pattern(s) in batch of %d calls (warning %d/%d)",
		len(findings), len(toolCalls), s.warnsIssued, s.maxWarns)

	var sb strings.Builder
	sb.WriteString("## Parallel Tool Call Coupling Warning\n\n")
	sb.WriteString("Your current batch of tool calls contains hidden sequential dependencies.\n")
	sb.WriteString("These calls may fail or produce stale results if executed in parallel\n")
	sb.WriteString("(regardless of the order you listed them in — sequence them explicitly):\n\n")
	for _, f := range findings {
		sb.WriteString(fmt.Sprintf("  - %s\n", f))
	}
	sb.WriteString("\n")
	sb.WriteString("**Guidance**: Sequence the dependent call AFTER the prerequisite completes.\n")
	sb.WriteString("Issue the prerequisite tool call first, wait for its result, then issue the dependent call.\n")
	if s.warnsIssued >= s.maxWarns {
		sb.WriteString("\n*(This is the last coupling warning for this run.)*\n")
	}

	return sb.String()
}

// matchMkdirToFilePath checks if a file_ops(mkdir) call creates a directory
// that is a prefix of a file path in the dependent call.
func matchMkdirToFilePath(prereqArgs, depArgs json.RawMessage) bool {
	dirPath := extractMkdirPath(prereqArgs)
	if dirPath == "" {
		return false
	}
	filePath := couplingExtractPath(depArgs)
	if filePath == "" {
		return false
	}
	dirPath = strings.TrimSuffix(dirPath, "/")
	return strings.HasPrefix(filePath, dirPath+"/")
}

// matchSameFilePath checks if two calls reference the same file path.
func matchSameFilePath(prereqArgs, depArgs json.RawMessage) bool {
	prior := couplingExtractPath(prereqArgs)
	later := couplingExtractPath(depArgs)
	if prior == "" || later == "" {
		return false
	}
	return prior == later
}

// extractMkdirPath extracts the directory path from a file_ops call with action=mkdir.
func extractMkdirPath(args json.RawMessage) string {
	var parsed struct {
		Operations []struct {
			Action string `json:"action"`
			Source string `json:"source"`
		} `json:"operations"`
	}
	if err := json.Unmarshal(args, &parsed); err != nil {
		return ""
	}
	for _, op := range parsed.Operations {
		if op.Action == "mkdir" {
			return op.Source
		}
	}
	return ""
}

// couplingExtractPath extracts the file_path or path from tool call arguments.
func couplingExtractPath(args json.RawMessage) string {
	var parsed struct {
		FilePath string `json:"file_path"`
		Path     string `json:"path"`
	}
	if err := json.Unmarshal(args, &parsed); err != nil {
		return ""
	}
	if parsed.FilePath != "" {
		return parsed.FilePath
	}
	return parsed.Path
}
