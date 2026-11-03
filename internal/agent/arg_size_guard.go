package agent

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
)

const (
	argSizeWarnTotal = 8 * 1024

	argSizeWarnField = 4 * 1024

	argSizeSevereField = 16 * 1024

	maxArgSizeGuardFires = 1
)

var argSizeFields = map[string][]string{
	"edit_file":    {"old_text", "new_text"},
	"write_file":   {"content"},
	"grep":         {"pattern"},
	"search_files": {"pattern"},

	"multi_edit_file": {},
	"multi_file_edit": {},
}

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

func analyzeArgSize(toolName string, args []byte) string {
	totalLen := len(args)
	if totalLen < argSizeWarnTotal {

	}

	var argMap map[string]json.RawMessage
	if err := json.Unmarshal(args, &argMap); err != nil {

		return ""
	}

	var hints []string

	if totalLen >= argSizeWarnTotal {
		hints = append(hints, fmt.Sprintf(
			"total argument size is %s — consider using more concise parameters to save context tokens",
			formatArgBytes(totalLen),
		))
	}

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
			case "edit_file":
				if field == "old_text" || field == "new_text" {
					hints = append(hints, argSizeEditFieldHint(field, fieldLen)...)
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

	if toolName == "multi_file_edit" {
		if editsHint := analyzeMultiFileEditSize(argMap); editsHint != "" {
			hints = append(hints, editsHint)
		}
	}

	if toolName == "multi_edit_file" {
		if editsHint := analyzeMultiEditFileSize(argMap); editsHint != "" {
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

	maxWarn := ""
	var maxWarnLen int
	for _, f := range files {
		for _, e := range f.Edits {
			for _, fld := range []struct {
				name string
				val  string
			}{{"old_text", e.OldText}, {"new_text", e.NewText}} {
				if len(fld.val) < argSizeWarnField || len(fld.val) <= maxWarnLen {
					continue
				}
				maxWarnLen = len(fld.val)
				maxWarn = strings.Join(argSizeEditFieldHint(fld.name, len(fld.val)), "")
			}
		}
	}
	return maxWarn
}

func analyzeMultiEditFileSize(argMap map[string]json.RawMessage) string {
	editsRaw, ok := argMap["edits"]
	if !ok {
		return ""
	}

	var edits []struct {
		OldText string `json:"old_text"`
		NewText string `json:"new_text"`
	}
	if err := json.Unmarshal(editsRaw, &edits); err != nil {
		return ""
	}

	maxWarn := ""
	var maxWarnLen int
	for _, e := range edits {
		for _, f := range []struct {
			name string
			val  string
		}{{"old_text", e.OldText}, {"new_text", e.NewText}} {
			if len(f.val) < argSizeWarnField || len(f.val) <= maxWarnLen {
				continue
			}
			maxWarnLen = len(f.val)
			maxWarn = strings.Join(argSizeEditFieldHint(f.name, len(f.val)), "")
		}
	}
	return maxWarn
}

func argSizeEditFieldHint(field string, fieldLen int) []string {
	if fieldLen >= argSizeSevereField {
		return []string{fmt.Sprintf(
			"%s is %s — use concise line-number anchors from read_file instead of pasting large code blocks. "+
				"Copy only the specific lines you need to change (e.g., 5-10 lines around the edit target).",
			field, formatArgBytes(fieldLen),
		)}
	}
	return []string{fmt.Sprintf(
		"%s is %s — consider using shorter line-number anchors from read_file to reduce context usage",
		field, formatArgBytes(fieldLen),
	)}
}

func formatArgBytes(n int) string {
	switch {
	case n >= 1024:
		return fmt.Sprintf("%.1fKB", float64(n)/1024)
	default:
		return fmt.Sprintf("%dB", n)
	}
}
