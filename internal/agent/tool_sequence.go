package agent

import (
	"encoding/json"
	"fmt"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/provider"
)

// Tool Call Sequence Validator — Anti-Pattern Detection Across Iterations
//
// Research basis: Tool-use optimization literature (ToolGen, Gorilla, ToolLLM)
// shows that LLM agents waste 15-30% of iterations on suboptimal tool call
// sequences. The waste comes from patterns where the agent uses a BROAD tool
// call first and then NARROWS down, instead of starting with a targeted call.
//
// This is different from existing systems:
//   - Loop detector: catches exact duplicates (same tool + same args)
//   - Memoization: prevents re-execution of identical calls
//   - Repetition tracker: catches read-edit-fail cycles
//   - Empty search spiral: catches futile searches
//
// This validator catches CROSS-TOOL anti-patterns: sequences of DIFFERENT tool
// calls that are suboptimal together. Examples:
//
//  1. Full read → targeted re-read: read_file(X) then read_file(X, offset, limit)
//     — the agent should have started with targeted reads.
//  2. Sequential individual reads: read_file(A), read_file(B), read_file(C)
//     — should use multi_file_read for batch efficiency.
//  3. Directory listing then glob: list_directory(X) then glob(pattern, X)
//     — redundant exploration of the same directory.
//  4. Read then grep same file: read_file(X) then grep(pattern, path=X)
//     — the agent already has the content, should search inline.
//  5. Broad search then narrow: search_files("TODO") then search_files("TODO", dir=X)
//     — should have narrowed the scope first.
//
// Each anti-pattern fires at most once per run to avoid nagging. Hints are
// concise and actionable, suggesting the specific more efficient alternative.

const (
	// seqWindow: number of recent tool calls to keep for pattern matching.
	// Must be large enough to catch sequential individual reads (3+ calls)
	// but small enough to avoid false positives from unrelated interleaving.
	seqWindow = 12

	// seqConsecutiveReads: number of individual read_file calls in sequence
	// (within seqWindow) that triggers a batch suggestion.
	seqConsecutiveReads = 3
)

type seqEntry struct {
	tool     string
	filePath string // primary file/path argument, if any
	args     map[string]interface{}
	iter     int // iteration number when this call was made
}

type toolSequenceValidator struct {
	history    []seqEntry
	hintsGiven map[string]bool // anti-pattern type → fired flag
}

func newToolSequenceValidator() *toolSequenceValidator {
	return &toolSequenceValidator{
		hintsGiven: make(map[string]bool),
	}
}

func (v *toolSequenceValidator) reset() {
	v.history = v.history[:0]
	// Clear the map by re-allocating (reset is called per-run)
	v.hintsGiven = make(map[string]bool)
}

// record adds a tool call to the sequence history and returns guidance text
// if an anti-pattern is detected. The guidance is injected into the tool result.
func (v *toolSequenceValidator) record(tc provider.ToolCallDelta, iter int) string {
	entry := seqEntry{
		tool: tc.Name,
		args: parseArgsMap(tc.Arguments),
		iter: iter,
	}
	entry.filePath = extractPrimaryPath(entry.tool, entry.args)

	// Check anti-patterns BEFORE adding to history (current call vs recent history)
	guidance := v.checkAntiPatterns(entry)

	// Add to history
	v.history = append(v.history, entry)
	if len(v.history) > seqWindow {
		v.history = v.history[len(v.history)-seqWindow:]
	}

	if guidance != "" {
		gLen := len(guidance)
		if gLen > 80 {
			gLen = 80
		}
		debug.Log("tool-sequence", "anti-pattern detected: %s (iter=%d tool=%s)", guidance[:gLen], iter, tc.Name)
	}

	return guidance
}

func (v *toolSequenceValidator) checkAntiPatterns(curr seqEntry) string {
	// Pattern 1: Full read → targeted re-read of same file
	if guidance := v.checkFullReadThenTargeted(curr); guidance != "" {
		return guidance
	}

	// Pattern 2: Sequential individual reads (should use multi_file_read)
	if guidance := v.checkSequentialReads(curr); guidance != "" {
		return guidance
	}

	// Pattern 3: list_directory → glob on same directory
	if guidance := v.checkDirThenGlob(curr); guidance != "" {
		return guidance
	}

	// Pattern 4: read_file → grep on same file
	if guidance := v.checkReadThenGrep(curr); guidance != "" {
		return guidance
	}

	// Pattern 5: Broad search → narrow search
	if guidance := v.checkBroadThenNarrowSearch(curr); guidance != "" {
		return guidance
	}

	return ""
}

// Pattern 1: read_file(X) [no offset/limit] → read_file(X, offset, limit)
// The agent should have used targeted reads from the start.
func (v *toolSequenceValidator) checkFullReadThenTargeted(curr seqEntry) string {
	if v.hintsGiven["full_read_then_targeted"] {
		return ""
	}
	if curr.tool != "read_file" {
		return ""
	}
	if curr.filePath == "" {
		return ""
	}
	// Current call has offset or limit → it's a targeted read
	hasOffset := hasIntArg(curr.args, "offset")
	hasLimit := hasIntArg(curr.args, "limit")
	if !hasOffset && !hasLimit {
		return ""
	}
	// Check if there was a full read (no offset/limit) of the same file earlier
	for i := len(v.history) - 1; i >= 0; i-- {
		e := v.history[i]
		if e.tool == "read_file" && e.filePath == curr.filePath {
			if !hasIntArg(e.args, "offset") && !hasIntArg(e.args, "limit") {
				v.hintsGiven["full_read_then_targeted"] = true
				return fmt.Sprintf(
					"[tool-sequence] You previously read %s in full, now reading a subset with offset/limit. "+
						"For future reads of unfamiliar files, start with offset/limit to explore specific sections — "+
						"full reads of large files waste context budget.",
					curr.filePath,
				)
			}
		}
	}
	return ""
}

// Pattern 2: Multiple sequential read_file calls should use multi_file_read
func (v *toolSequenceValidator) checkSequentialReads(curr seqEntry) string {
	if v.hintsGiven["sequential_reads"] {
		return ""
	}
	if curr.tool != "read_file" {
		return ""
	}
	// Count consecutive read_file calls in recent history (not including current)
	consecutive := 0
	for i := len(v.history) - 1; i >= 0; i-- {
		if v.history[i].tool == "read_file" {
			consecutive++
		} else {
			break
		}
	}
	// Current call is the (consecutive+1)th read_file in a row
	if consecutive+1 < seqConsecutiveReads {
		return ""
	}
	// Check that these reads are of DIFFERENT files (same file reads are handled by memoization)
	files := make(map[string]bool)
	files[curr.filePath] = true
	for i := len(v.history) - 1; i >= 0 && i >= len(v.history)-consecutive; i-- {
		if v.history[i].tool == "read_file" && v.history[i].filePath != "" {
			files[v.history[i].filePath] = true
		}
	}
	if len(files) < seqConsecutiveReads {
		return "" // re-reads of same file, not a batch scenario
	}
	v.hintsGiven["sequential_reads"] = true
	return fmt.Sprintf(
		"[tool-sequence] You've made %d sequential read_file calls. "+
			"Use multi_file_read to batch multiple file reads in a single call — "+
			"this saves iterations and reduces context overhead.",
		consecutive+1,
	)
}

// Pattern 3: list_directory(X) → glob(pattern, X) on the same directory
func (v *toolSequenceValidator) checkDirThenGlob(curr seqEntry) string {
	if v.hintsGiven["dir_then_glob"] {
		return ""
	}
	if curr.tool != "glob" {
		return ""
	}
	globDir, _ := curr.args["directory"].(string)
	if globDir == "" {
		return ""
	}
	for i := len(v.history) - 1; i >= 0; i-- {
		e := v.history[i]
		if e.tool == "list_directory" && e.filePath == globDir {
			v.hintsGiven["dir_then_glob"] = true
			return fmt.Sprintf(
				"[tool-sequence] You already ran list_directory on %s. "+
					"If you need specific files, use glob or grep directly instead of "+
					"listing then searching — it saves an iteration.",
				globDir,
			)
		}
	}
	return ""
}

// Pattern 4: read_file(X) → grep(pattern, path=X)
// The agent already has the file content, should search within it.
func (v *toolSequenceValidator) checkReadThenGrep(curr seqEntry) string {
	if v.hintsGiven["read_then_grep"] {
		return ""
	}
	if curr.tool != "grep" && curr.tool != "search_files" {
		return ""
	}
	searchPath, _ := curr.args["path"].(string)
	if searchPath == "" {
		return ""
	}
	for i := len(v.history) - 1; i >= 0; i-- {
		e := v.history[i]
		if e.tool == "read_file" && e.filePath == searchPath {
			v.hintsGiven["read_then_grep"] = true
			return fmt.Sprintf(
				"[tool-sequence] You already read %s in this session. "+
					"Avoid grepping a file you've already read — search within the content "+
					"you already have, or use offset/limit to re-read a specific section.",
				searchPath,
			)
		}
	}
	return ""
}

// Pattern 5: search_files(pattern, no dir) → search_files(pattern, dir=X)
// The agent should have narrowed the scope from the start.
func (v *toolSequenceValidator) checkBroadThenNarrowSearch(curr seqEntry) string {
	if v.hintsGiven["broad_then_narrow"] {
		return ""
	}
	if curr.tool != "search_files" && curr.tool != "grep" {
		return ""
	}
	currDir, _ := curr.args["directory"].(string)
	if curr.tool == "grep" || curr.tool == "search_files" {
		currDir, _ = curr.args["path"].(string)
	}
	if currDir == "" {
		return ""
	}
	currPattern, _ := curr.args["pattern"].(string)
	if currPattern == "" {
		return ""
	}
	for i := len(v.history) - 1; i >= 0; i-- {
		e := v.history[i]
		if (e.tool == "search_files" || e.tool == "grep") && e.tool == curr.tool {
			eDir, _ := e.args["directory"].(string)
			if e.tool == "grep" || e.tool == "search_files" {
				eDir, _ = e.args["path"].(string)
			}
			ePattern, _ := e.args["pattern"].(string)
			// Same pattern, but previous call had no directory (broad search)
			if ePattern == currPattern && eDir == "" {
				v.hintsGiven["broad_then_narrow"] = true
				return fmt.Sprintf(
					"[tool-sequence] You searched for %q broadly, then narrowed to %q. "+
						"In future searches, specify the directory upfront to get more relevant results faster.",
					currPattern, currDir,
				)
			}
		}
	}
	return ""
}

// parseArgsMap safely parses tool call arguments JSON into a map.
func parseArgsMap(args []byte) map[string]interface{} {
	if len(args) == 0 {
		return nil
	}
	var m map[string]interface{}
	if err := json.Unmarshal(args, &m); err != nil {
		return nil
	}
	return m
}

// extractPrimaryPath extracts the primary file path argument from common tool calls.
func extractPrimaryPath(toolName string, args map[string]interface{}) string {
	if args == nil {
		return ""
	}
	// Most tools use "path" as the primary file argument
	if p, ok := args["path"].(string); ok && p != "" {
		return p
	}
	// read_file uses "path", glob uses "directory"
	if p, ok := args["file_path"].(string); ok && p != "" {
		return p
	}
	if p, ok := args["directory"].(string); ok && p != "" {
		return p
	}
	return ""
}

// hasIntArg checks if an integer argument exists and has a non-zero value.
func hasIntArg(args map[string]interface{}, key string) bool {
	if args == nil {
		return false
	}
	v, ok := args[key]
	if !ok {
		return false
	}
	switch n := v.(type) {
	case float64:
		return n > 0
	case int:
		return n > 0
	case json.Number:
		i, err := n.Int64()
		return err == nil && i > 0
	default:
		return false
	}
}
