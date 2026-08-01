package agent

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
)

// Tool Argument Size Guard.
//
// Research basis: Context engineering studies (Anthropic 2025, ACE Zhang et al.
// ICLR 2026) identify tool call arguments as a major source of context waste.
// When models paste entire file contents as old_text anchors in edit_file
// (instead of using concise line-number anchors), or send massive search
// patterns, they consume disproportionate context tokens — the arguments live
// in the assistant message's tool_use blocks and persist until compaction.
//
// Claude Code's diff-based editing and Cursor's selection-based edits address
// this implicitly. ggcode's system prompt already recommends line-number
// anchors, but there is no runtime guard to detect and correct this anti-pattern.
//
// This guard analyzes tool arguments for known bloat patterns:
//   - edit_file/multi_edit_file: oversized old_text (should use line-number anchors)
//   - write_file: oversized content (should use edit_file for targeted changes)
//   - grep/search_files: oversized patterns
//   - Any tool: total argument payload exceeding a threshold
//
// The guard fires AT MOST ONCE per run to avoid repetition. It injects a
// guidance hint into the tool result — it does NOT block execution.
// No LLM cost — purely mechanical JSON inspection.

const (
	// argSizeWarnTotal is the total argument size (bytes) that triggers a warning.
	// 8KB of JSON arguments is already substantial (~2K tokens in tool_use blocks).
	argSizeWarnTotal = 8 * 1024

	// argSizeWarnField is the per-field size that triggers a warning for known
	// content fields. 4KB for a single field is a strong signal of bloat.
	argSizeWarnField = 4 * 1024

	// argSizeSevereField: fields larger than this are almost certainly wasteful.
	argSizeSevereField = 16 * 1024

	// maxArgSizeGuardFires limits the number of times the guard injects guidance
	// in a single run. After the first fire, the model should adjust. This
	// prevents repeated nagging.
	maxArgSizeGuardFires = 1
)

// argSizeFields maps tool names to fields that are commonly oversized.
var argSizeFields = map[string][]string{
	"edit_file":       {"old_text", "new_text"},
	"multi_edit_file": {"old_text", "new_text"},
	"multi_file_edit": {}, // nested structure, handled separately
	"write_file":      {"content"},
	"grep":            {"pattern"},
	"search_files":    {"pattern"},
}

// checkArgSizeGuard inspects tool arguments for excessive size and returns
// a guidance message if oversized arguments are detected. Returns empty string
// when arguments are within reasonable bounds.
func (a *Agent) checkArgSizeGuard(toolName string, args []byte) string {
	if a.argSizeGuardFires >= maxArgSizeGuardFires {
		return ""
	}

	guidance := analyzeArgSize(toolName, args)
	if guidance == "" {
		return ""
	}

	a.argSizeGuardFires++
	debug.Log("arg-size-guard", "oversized args detected: tool=%s totalArgs=%d", toolName, len(args))
	return guidance
}

// analyzeArgSize performs the actual size analysis. Separated from
// checkArgSizeGuard for testability.
func analyzeArgSize(toolName string, args []byte) string {
	totalLen := len(args)
	if totalLen < argSizeWarnTotal {
		// Quick check: if total payload is small, skip field analysis.
		// Still check known fields for individual bloat (rare but possible).
	}

	var argMap map[string]json.RawMessage
	if err := json.Unmarshal(args, &argMap); err != nil {
		// Can't parse — let the tool handle the error.
		return ""
	}

	var hints []string

	// Check total payload size.
	if totalLen >= argSizeWarnTotal {
		hints = append(hints, fmt.Sprintf(
			"total argument size is %s — consider using more concise parameters to save context tokens",
			formatArgBytes(totalLen),
		))
	}

	// Check known content fields for this tool.
	fields, known := argSizeFields[toolName]
	if known {
		for _, field := range fields {
			raw, ok := argMap[field]
			if !ok {
				continue
			}
			fieldLen := len(raw)
			if fieldLen < argSizeWarnField {
				continue
			}

			switch toolName {
			case "edit_file", "multi_edit_file":
				if field == "old_text" || field == "new_text" {
					if fieldLen >= argSizeSevereField {
						hints = append(hints, fmt.Sprintf(
							"%s is %s — use concise line-number anchors from read_file instead of pasting large code blocks. "+
								"Copy only the specific lines you need to change (e.g., 5-10 lines around the edit target).",
							field, formatArgBytes(fieldLen),
						))
					} else {
						hints = append(hints, fmt.Sprintf(
							"%s is %s — consider using shorter line-number anchors from read_file to reduce context usage",
							field, formatArgBytes(fieldLen),
						))
					}
				}
			case "write_file":
				hints = append(hints, fmt.Sprintf(
					"content is %s — if this is an edit to an existing file, use edit_file with targeted old_text anchors instead of rewriting the entire file",
					formatArgBytes(fieldLen),
				))
			case "grep", "search_files":
				hints = append(hints, fmt.Sprintf(
					"pattern is %s — consider using a shorter regex pattern to reduce context waste",
					formatArgBytes(fieldLen),
				))
			}
		}
	}

	// Handle multi_file_edit with nested edits array.
	if toolName == "multi_file_edit" {
		if editsHint := analyzeMultiFileEditSize(argMap); editsHint != "" {
			hints = append(hints, editsHint)
		}
	}

	if len(hints) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("[Context efficiency hint] ")
	for i, h := range hints {
		if i > 0 {
			sb.WriteString("; ")
		}
		sb.WriteString(h)
	}
	return sb.String()
}

// analyzeMultiFileEditSize checks the files[].edits[] structure for oversized
// old_text or new_text values.
func analyzeMultiFileEditSize(argMap map[string]json.RawMessage) string {
	filesRaw, ok := argMap["files"]
	if !ok {
		return ""
	}

	var files []struct {
		Edits []struct {
			OldText string `json:"old_text"`
			NewText string `json:"new_text"`
		} `json:"edits"`
	}
	if err := json.Unmarshal(filesRaw, &files); err != nil {
		return ""
	}

	var maxFieldLen int
	var maxFieldName string
	for _, f := range files {
		for _, e := range f.Edits {
			if len(e.OldText) > maxFieldLen {
				maxFieldLen = len(e.OldText)
				maxFieldName = "old_text"
			}
			if len(e.NewText) > maxFieldLen {
				maxFieldLen = len(e.NewText)
				maxFieldName = "new_text"
			}
		}
	}

	if maxFieldLen < argSizeSevereField {
		return ""
	}

	return fmt.Sprintf(
		"%s in a file edit is %s — use concise line-number anchors from read_file instead of pasting large code blocks",
		maxFieldName, formatArgBytes(maxFieldLen),
	)
}

func formatArgBytes(n int) string {
	switch {
	case n >= 1024:
		return fmt.Sprintf("%.1fKB", float64(n)/1024)
	default:
		return fmt.Sprintf("%dB", n)
	}
}
