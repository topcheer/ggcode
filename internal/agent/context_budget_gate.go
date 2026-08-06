package agent

// Context Budget Awareness Gate -- Context Engineering
//
// Research basis: Anthropic's "Context Engineering" (2025) and the ACE framework
// (ICLR 2026) identify "context-blind tool calls" as a top efficiency anti-pattern.
// Chroma's 2025 empirical study shows all frontier models degrade significantly
// past ~50% context fill, with tool output being the primary "silent context killer."
//
// Competitor analysis:
//   - Claude Code: no context-awareness guidance for tool calls
//   - Cursor: relies on editor context, not applicable to CLI agents
//   - OpenHands: no budget-aware tool gating
//   - Aider: minimal tool surface, context management is manual
//   - Windsurf: proprietary cascade system, no transparent budget gating
//
// Gap: ggcode already has:
//   - tool_output_guard.go: truncates large results AFTER execution
//   - search_param_guard.go: checks parameter quality but is CONTEXT-BLIND
//   - context_footprint.go: tracks per-tool attribution but doesn't gate
//
// None of these PROACTIVELY warn when an agent makes expensive tool calls while
// context is already near compaction. This gate detects the pattern:
//   1. Context fill is above the "danger zone" (e.g., 70%+)
//   2. Agent calls an expensive tool (read_file without limit, broad grep, etc.)
//   3. Gate injects guidance to use targeted parameters or defer the call
//
// Design:
//   - Computes estimated context fill from context manager state
//   - Checks tool call against cost heuristics (zero LLM cost)
//   - Non-blocking: guidance appended to result, execution proceeds
//   - Fires at most 3 times per run (avoids nagging)
//   - Does not fire for tools that are inherently cheap or have explicit limits

import (
	"encoding/json"
	"fmt"
	"strings"
)

const (
	cbgMaxFires          = 3
	cbgDangerFill        = 0.70 // 70% of compaction threshold
	cbgCriticalFill      = 0.85 // 85% -- severe context pressure
	cbgExpensiveReadSize = 2000 // read_file with offset+limit > this is expensive
)

// cbgExpensiveCmds are command substrings that typically produce large output.
var cbgExpensiveCmds = []string{"make", "go test", "go build", "npm ", "cargo ", "docker build", "kubectl get"}

// contextBudgetGate tracks context-budget-aware tool call guidance.
type contextBudgetGate struct {
	fires int
}

func newContextBudgetGate() *contextBudgetGate {
	return &contextBudgetGate{}
}

func (g *contextBudgetGate) reset() {
	g.fires = 0
}

// checkBudgetAwareness returns a non-empty guidance string if the agent is
// making an expensive tool call while context pressure is high.
// contextFill is the ratio of current tokens to compaction threshold (0.0-1.0+).
func (g *contextBudgetGate) checkBudgetAwareness(toolName string, args []byte, contextFill float64) string {
	if g.fires >= cbgMaxFires {
		return ""
	}
	if contextFill < cbgDangerFill {
		return ""
	}

	var hint string
	critical := contextFill >= cbgCriticalFill
	switch toolName {
	case "read_file":
		hint = cbgCheckReadFile(args, critical)
	case "grep":
		hint = cbgCheckGrep(args, critical)
	case "glob":
		hint = cbgCheckGlob(args, critical)
	case "search_files":
		hint = cbgCheckSearchFiles(args, critical)
	case "run_command", "start_command":
		hint = cbgCheckCommand(args, critical)
	case "code_search":
		hint = cbgCheckCodeSearch(args, critical)
	case "multi_file_read":
		hint = cbgCheckMultiFileRead(args, critical)
	default:
		return ""
	}

	if hint != "" {
		g.fires++
	}
	return hint
}

// cbgCheckReadFile detects expensive read_file calls (no limit or very large limit).
func cbgCheckReadFile(args []byte, critical bool) string {
	path := cbgExtractString(args, "path")
	if path == "" {
		return ""
	}

	limit := cbgExtractInt(args, "limit")

	// No limit set -- read_file defaults to 2000 lines, which is very expensive
	if limit == 0 {
		if critical {
			return fmt.Sprintf(
				"Context Alert: Context window is critically full. You are reading '%s' without a line limit -- this could add ~8K+ tokens. Use offset/limit to read only the relevant section (e.g., offset=100&limit=50), or use grep to find the exact lines first.",
				cbgShortPath(path),
			)
		}
		return fmt.Sprintf(
			"Context Hint: Context is near compaction. Reading '%s' without limit will consume significant budget. Consider using offset/limit to read only relevant lines, or use grep to locate the target first.",
			cbgShortPath(path),
		)
	}

	// Large limit in danger zone
	if limit > cbgExpensiveReadSize {
		return fmt.Sprintf(
			"Context Hint: Reading %d lines of '%s' while context is near compaction. Consider narrowing to the specific section you need (use grep to find line numbers first, then read with tight offset/limit).",
			limit, cbgShortPath(path),
		)
	}

	return ""
}

// cbgCheckGrep detects expensive grep calls (content mode without file type filter).
func cbgCheckGrep(args []byte, critical bool) string {
	outputMode := cbgExtractString(args, "output_mode")
	globFilter := cbgExtractString(args, "glob")
	typeFilter := cbgExtractString(args, "type")

	// If using content mode with no filters, this can produce massive output
	if outputMode == "content" && globFilter == "" && typeFilter == "" {
		if critical {
			return "Context Alert: grep in content mode without glob/type filter at critical context fill. Add a glob or type filter (e.g., type=go) and consider output_mode=files_with_matches to minimize output."
		}
		return "Context Hint: grep in content mode without file type filter. Consider adding a type or glob filter to reduce output size."
	}

	return ""
}

// cbgCheckGlob detects broad glob patterns under context pressure.
func cbgCheckGlob(args []byte, critical bool) string {
	pattern := cbgExtractString(args, "pattern")

	// ** recursive globs can return hundreds of paths
	if strings.Contains(pattern, "**") && critical {
		return "Context Alert: Recursive glob '**' at critical context fill may return many paths. Consider a more specific pattern or use grep with output_mode=files_with_matches."
	}

	return ""
}

// cbgCheckSearchFiles detects broad search_files calls.
func cbgCheckSearchFiles(args []byte, critical bool) string {
	maxResults := cbgExtractInt(args, "max_results")
	if maxResults == 0 {
		// Default max_results is 50 in search_files -- that's a lot of context
		if critical {
			return "Context Alert: search_files with default max_results (50) at critical context fill. Consider reducing max_results or using grep with output_mode=files_with_matches."
		}
		return ""
	}
	if maxResults > 20 && critical {
		return fmt.Sprintf("Context Alert: search_files max_results=%d at critical context fill. Consider reducing to 5-10 results to conserve context budget.", maxResults)
	}
	return ""
}

// cbgCheckCodeSearch detects expensive code_search calls.
func cbgCheckCodeSearch(args []byte, critical bool) string {
	maxResults := cbgExtractInt(args, "max_results")
	if maxResults == 0 && critical {
		return "Context Alert: code_search with default max_results at critical context fill. Consider reducing max_results to 3-5 to conserve context budget."
	}
	if maxResults > 5 && critical {
		return fmt.Sprintf("Context Alert: code_search max_results=%d is high at critical context fill. Each result includes code context -- consider reducing to 3-5.", maxResults)
	}
	return ""
}

// cbgCheckCommand detects commands likely to produce large output.
func cbgCheckCommand(args []byte, critical bool) string {
	cmd := cbgExtractString(args, "command")
	if cmd == "" {
		return ""
	}

	lower := strings.ToLower(cmd)
	for _, ec := range cbgExpensiveCmds {
		if strings.Contains(lower, ec) {
			if critical {
				return fmt.Sprintf("Context Alert: Command '%s' at critical context fill may produce large output. Consider piping to head/tail, using -count=1 for tests, or splitting the operation.", cbgShortCmd(cmd))
			}
			return fmt.Sprintf("Context Hint: Command '%s' at high context fill may produce verbose output. Consider limiting output with head/tail or grep to reduce context consumption.", cbgShortCmd(cmd))
		}
	}

	return ""
}

// cbgCheckMultiFileRead detects multi_file_read with many files under pressure.
func cbgCheckMultiFileRead(args []byte, critical bool) string {
	// Count files in the JSON array
	var m struct {
		Files []json.RawMessage `json:"files"`
	}
	if err := json.Unmarshal(args, &m); err != nil {
		return ""
	}
	fileCount := len(m.Files)
	if fileCount == 0 {
		return ""
	}

	if fileCount > 3 && critical {
		return fmt.Sprintf("Context Alert: multi_file_read with %d files at critical context fill. Each file adds significant tokens. Consider reading only 1-2 most relevant files now, or use grep to narrow down first.", fileCount)
	}
	if fileCount > 5 {
		return fmt.Sprintf("Context Hint: multi_file_read with %d files near compaction. Consider reducing to 2-3 most relevant files to conserve context budget.", fileCount)
	}

	return ""
}

// cbgExtractString extracts a string field from JSON args.
func cbgExtractString(args []byte, field string) string {
	if len(args) == 0 {
		return ""
	}
	var m map[string]interface{}
	if err := json.Unmarshal(args, &m); err != nil {
		return ""
	}
	v, ok := m[field]
	if !ok {
		return ""
	}
	s, ok := v.(string)
	if !ok {
		return ""
	}
	return s
}

// cbgExtractInt extracts an integer field from JSON args.
func cbgExtractInt(args []byte, field string) int {
	if len(args) == 0 {
		return 0
	}
	var m map[string]interface{}
	if err := json.Unmarshal(args, &m); err != nil {
		return 0
	}
	v, ok := m[field]
	if !ok {
		return 0
	}
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	}
	return 0
}

// cbgShortPath truncates long file paths for display.
func cbgShortPath(path string) string {
	if len(path) <= 60 {
		return path
	}
	parts := strings.Split(path, "/")
	if len(parts) <= 2 {
		return path
	}
	return ".../" + strings.Join(parts[max(0, len(parts)-3):], "/")
}

// cbgShortCmd truncates long commands for display.
func cbgShortCmd(cmd string) string {
	if len(cmd) <= 80 {
		return cmd
	}
	return cmd[:77] + "..."
}
