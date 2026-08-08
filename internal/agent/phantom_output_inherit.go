package agent

import (
	"encoding/json"
	"strconv"
	"strings"
	"sync"

	"github.com/topcheer/ggcode/internal/debug"
)

// Phantom Output Inheritance Detector
//
// Research basis:
//   - Acon: Optimizing Context Compression for Long-horizon LLM Agents
//     (arXiv:2510.00615, Oct 2025): compaction causes agents to lose track of
//     which tool calls failed vs succeeded, building reasoning on "phantom"
//     outputs from calls that actually returned errors or empty results.
//   - Speculative Execution patterns for Hyper-Responsive Agents
//     (GitHub: AutoQAce/agentic_ai_parallelism_patterns, 2025): agents dispatch
//     subsequent operations before confirming prior results succeeded.
//   - ACL 2026 SRW "Multi-Agent Reasoning Improves Compute Efficiency":
//     identifies trajectory waste where agents propagate assumptions from
//     unverified steps as if they were established facts.
//
// The gap: No existing detector catches when the agent references identifiers
// (file paths, symbol names, search patterns) from a FAILED or EMPTY tool call
// in subsequent tool calls -- treating the phantom output as real data.
//
// Example failure modes:
//   1. read_file("/foo/bar.go") → error "no such file"
//      Agent then calls grep for a function it claims was "in the file"
//      (the function never existed; the agent hallucinated expected content)
//   2. search_files returns empty
//      Agent then calls edit_file on a path from the search results that
//      don't exist
//   3. lsp_definition → "no definition found"
//      Agent then calls lsp_references on the (non-existent) symbol
//
// Key distinction from existing detectors:
//   - narrative_evidence: text contradicts output in the SAME step. This
//     detector catches CROSS-STEP phantom propagation.
//   - false_premise: success claims contradict errors. This detector catches
//     OPERATIONAL use of phantom data (subsequent tool calls, not text claims).
//   - phantom_verify: claims verification without calling tools. This detector
//     catches using data FROM failed tools.
//   - agentic_abstain: continuing after negatives. This detector catches the
//     specific pattern of REUSING phantom identifiers.

const (
	maxPhantomWarnings = 1 // fire at most once per run
	phantomLookback    = 6 // how many recent failed tool calls to remember
	phantomEvidenceMin = 2 // minimum phantom-build-on signals before firing
)

// phantomFailedCall records a failed tool call and its extractable identifiers.
type phantomFailedCall struct {
	toolName    string   // e.g. "read_file", "grep", "lsp_definition"
	identifiers []string // file paths, symbol names, patterns from the call
	iteration   int      // when it happened
}

// phantomBuildOn tracks when subsequent tool calls reference identifiers from
// previously failed calls.
type phantomBuildOn struct {
	targetTool string // the tool that referenced phantom identifiers
	matchedIDs []string
	sourceTool string // the failed tool call
	iteration  int
}

// phantomState tracks failed tool calls and detects phantom inheritance.
type phantomState struct {
	mu              sync.Mutex
	fired           bool
	failedCalls     []phantomFailedCall
	buildOnEvidence []phantomBuildOn
	totalScanned    int
}

func newPhantomState() *phantomState {
	return &phantomState{
		failedCalls: make([]phantomFailedCall, 0, phantomLookback),
	}
}

func (s *phantomState) reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.fired = false
	s.failedCalls = s.failedCalls[:0]
	s.buildOnEvidence = s.buildOnEvidence[:0]
	s.totalScanned = 0
}

// extractIdentifiers pulls file paths, symbol names, and patterns from tool
// call arguments. These are the "phantom identifiers" that might be wrongly
// reused if the call failed.
func extractPhantomIdentifiers(toolName string, args map[string]interface{}) []string {
	var ids []string

	// File path arguments (read_file, edit_file, write_file, lsp_*, etc.)
	pathKeys := []string{"path", "file_path", "file", "directory"}
	for _, key := range pathKeys {
		if val, ok := args[key].(string); ok && val != "" {
			// Normalize to basename for matching (full path may differ in subsequent calls)
			ids = append(ids, val)
			if base := pathBasename(val); base != "" && base != val {
				ids = append(ids, base)
			}
		}
	}

	// Pattern/query arguments (grep, search_files, lsp_workspace_symbols, etc.)
	patternKeys := []string{"pattern", "query", "symbol", "name"}
	for _, key := range patternKeys {
		if val, ok := args[key].(string); ok && len(val) > 2 {
			ids = append(ids, val)
		}
	}

	// Nested paths in multi_edit_file edits
	if toolName == "multi_edit_file" || toolName == "multi_file_edit" {
		if files, ok := args["files"].([]interface{}); ok {
			for _, f := range files {
				if fmap, ok := f.(map[string]interface{}); ok {
					if p, ok := fmap["path"].(string); ok && p != "" {
						ids = append(ids, p)
					}
				}
			}
		}
	}

	return ids
}

// pathBasename extracts the last component of a path.
func pathBasename(p string) string {
	// Handle both / and \ separators
	idx := strings.LastIndexAny(p, "/\\")
	if idx >= 0 && idx < len(p)-1 {
		return p[idx+1:]
	}
	return p
}

// isLikelyFailed determines if a tool result represents a failure or empty
// result based on content and error status.
func isLikelyFailed(resultContent string, isError bool) bool {
	if isError {
		return true
	}
	if resultContent == "" {
		return true
	}
	lower := strings.ToLower(resultContent)
	// Short results that indicate empty/nothing
	emptySignals := []string{
		"no results", "no matches", "0 results", "0 matches", "no files found",
		"nothing found", "no such file", "does not exist", "not found",
		"no symbols", "no definitions", "no references", "no implementations",
	}
	// Only check short results (long results with these phrases are likely context)
	if len(resultContent) < 200 {
		for _, sig := range emptySignals {
			if strings.Contains(lower, sig) {
				return true
			}
		}
	}
	return false
}

// parseArgs safely unmarshals json.RawMessage tool arguments into a map.
func parsePhantomArgs(raw json.RawMessage) map[string]interface{} {
	var args map[string]interface{}
	if json.Unmarshal(raw, &args) != nil {
		return nil
	}
	return args
}

// recordFailedCall tracks a tool call that returned an error or empty result.
func (s *phantomState) recordFailedCall(toolName string, rawArgs json.RawMessage, iteration int) {
	s.mu.Lock()
	defer s.mu.Unlock()

	args := parsePhantomArgs(rawArgs)
	ids := extractPhantomIdentifiers(toolName, args)
	if len(ids) == 0 {
		return
	}

	// Keep only recent failed calls
	if len(s.failedCalls) >= phantomLookback {
		s.failedCalls = s.failedCalls[1:]
	}
	s.failedCalls = append(s.failedCalls, phantomFailedCall{
		toolName:    toolName,
		identifiers: ids,
		iteration:   iteration,
	})
}

// recordSubsequentCall checks if a successful tool call references identifiers
// from previously failed calls (phantom inheritance).
func (s *phantomState) recordSubsequentCall(toolName string, rawArgs json.RawMessage, iteration int) {
	s.mu.Lock()
	defer s.mu.Unlock()

	args := parsePhantomArgs(rawArgs)
	s.totalScanned++
	currentIDs := extractPhantomIdentifiers(toolName, args)
	if len(currentIDs) == 0 {
		return
	}

	// Check each current identifier against all failed call identifiers
	currentIDSet := make(map[string]bool)
	for _, id := range currentIDs {
		currentIDSet[strings.ToLower(id)] = true
	}

	for _, failed := range s.failedCalls {
		if failed.iteration == iteration {
			continue // same call, skip
		}
		for _, fid := range failed.identifiers {
			if currentIDSet[strings.ToLower(fid)] {
				// Found phantom inheritance!
				s.buildOnEvidence = append(s.buildOnEvidence, phantomBuildOn{
					targetTool: toolName,
					matchedIDs: []string{fid},
					sourceTool: failed.toolName,
					iteration:  iteration,
				})
				break // one match per failed call is enough
			}
		}
	}
}

// checkPhantomInheritance evaluates accumulated evidence and returns guidance.
func (s *phantomState) checkPhantomInheritance(currentIter int) string {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.fired {
		return ""
	}
	if len(s.buildOnEvidence) < phantomEvidenceMin {
		return ""
	}

	s.fired = true

	// Build a summary of the phantom inheritance pattern
	var examples []string
	for i, ev := range s.buildOnEvidence {
		if i >= 3 {
			break
		}
		matched := strings.Join(ev.matchedIDs, ", ")
		entry := "  " + strconv.Itoa(i+1) + ". " + ev.targetTool + " (iter " + strconv.Itoa(ev.iteration) +
			") used identifier '" + matched + "' from a failed " + ev.sourceTool
		examples = append(examples, entry)
	}

	debug.Log("phantom-output", "phantom output inheritance: %d evidence signals at iter %d",
		len(s.buildOnEvidence), currentIter)

	return "[Phantom Output Inheritance] You are making tool calls that reference " +
		"identifiers (file paths, symbols, patterns) from PREVIOUSLY FAILED tool calls. " +
		"A tool call that returned an error or empty result does not produce usable " +
		"data -- referencing its identifiers as if it succeeded builds reasoning on " +
		"phantom output.\n\n" +
		"Evidence:\n" + strings.Join(examples, "\n") + "\n" +
		"Recommended actions:\n" +
		"1. Verify which prior tool calls actually SUCCEEDED before building on their results.\n" +
		"2. If a read/search failed, re-execute it with correct parameters before proceeding.\n" +
		"3. Do not assume the content of files or search results you never successfully obtained.\n" +
		"4. If a target genuinely doesn't exist, stop trying to reference it and acknowledge the gap."
}
