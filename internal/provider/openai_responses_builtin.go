package provider

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
)

const responsesIncludeFileSearchResults = "file_search_call.results"

func (p *OpenAIResponsesProvider) SetServerTools(tools []ServerToolConfig) {

	kept := make([]ServerToolConfig, 0, len(tools))
	for _, t := range tools {
		if _, ok := responsesHostedTool(t); ok {
			kept = append(kept, t)
			continue
		}
		switch strings.ToLower(strings.TrimSpace(t.Type)) {
		case "code_interpreter", "file_search":
			kept = append(kept, t)
		case "":
		default:
			debug.Log("openai", "responses: ignoring unsupported server tool %q", t.Type)
		}
	}
	if len(kept) > 0 || len(tools) == 0 {
		p.serverTools = kept
	}
}

func responsesBuiltinTools(tools []ServerToolConfig) []responsesTool {
	var out []responsesTool
	for _, t := range tools {
		switch strings.ToLower(strings.TrimSpace(t.Type)) {
		case "code_interpreter":
			out = append(out, responsesTool{
				Type:      "code_interpreter",
				Container: mustJSON(responsesCodeInterpreterContainer(t)),
			})
		case "file_search":
			ids := nonEmptyStrings(t.VectorStoreIDs)
			if len(ids) == 0 {
				continue
			}
			out = append(out, responsesTool{
				Type:           "file_search",
				VectorStoreIDs: ids,
				MaxNumResults:  t.MaxNumResults,
			})
		}
	}
	return out
}

func responsesCodeInterpreterContainer(t ServerToolConfig) map[string]any {
	auto := map[string]any{"type": "auto"}
	if t.MemoryLimit != "" {
		auto["memory_limit"] = t.MemoryLimit
	}
	if ids := nonEmptyStrings(t.FileIDs); len(ids) > 0 {
		auto["file_ids"] = ids
	}
	return auto
}

func nonEmptyStrings(in []string) []string {
	var out []string
	for _, s := range in {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

type responseAnnotation struct {
	Type        string `json:"type"`
	FileID      string `json:"file_id"`
	Filename    string `json:"filename"`
	ContainerID string `json:"container_id"`
	Quote       string `json:"quote"`
}

func appendResponsesCitations(text string, anns []responseAnnotation) string {
	if len(anns) == 0 {
		return text
	}
	var b strings.Builder
	b.WriteString(strings.TrimRight(text, "\n"))
	seen := map[string]bool{}
	for _, a := range anns {
		line := responsesCitationLine(a)
		if line == "" || seen[line] {
			continue
		}
		seen[line] = true
		b.WriteString("\n")
		b.WriteString(line)
	}
	return b.String()
}

func responsesCitationLine(a responseAnnotation) string {
	switch a.Type {
	case "container_file_citation":
		return fmt.Sprintf("[container file: %s (%s)]", citationName(a), a.FileID)
	case "file_citation":
		if a.Quote != "" {
			return fmt.Sprintf("[file: %s (%s) %q]", citationName(a), a.FileID, truncateRunes(a.Quote, 80))
		}
		return fmt.Sprintf("[file: %s (%s)]", citationName(a), a.FileID)
	}
	return ""
}

func citationName(a responseAnnotation) string {
	if a.Filename != "" {
		return a.Filename
	}
	return a.FileID
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "..."
}

func formatResponsesServerItem(raw json.RawMessage) (string, bool) {
	var probe struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(raw, &probe) != nil {
		return "", false
	}
	switch probe.Type {
	case "code_interpreter_call":
		return formatCodeInterpreterCall(raw)
	case "file_search_call":
		return formatFileSearchCall(raw)
	}
	return "", false
}

func formatCodeInterpreterCall(raw json.RawMessage) (string, bool) {
	var item struct {
		Status      string            `json:"status"`
		ContainerID string            `json:"container_id"`
		Code        string            `json:"code"`
		Outputs     []json.RawMessage `json:"outputs"`
	}
	if err := json.Unmarshal(raw, &item); err != nil {
		return "", false
	}
	var b strings.Builder
	fmt.Fprintf(&b, "[code_interpreter status=%s container=%s]", item.Status, item.ContainerID)
	if item.Code != "" {
		b.WriteString("\n```python\n")
		b.WriteString(strings.TrimRight(item.Code, "\n"))
		b.WriteString("\n```")
	}

	for _, rawOut := range item.Outputs {
		var asString string
		if json.Unmarshal(rawOut, &asString) == nil && strings.TrimSpace(asString) != "" {
			b.WriteString("\n")
			b.WriteString(strings.TrimSpace(asString))
			continue
		}
		var obj struct {
			Console string `json:"console"`
			Output  string `json:"output"`
			Logs    []struct {
				Text string `json:"text"`
			} `json:"logs"`
		}
		if json.Unmarshal(rawOut, &obj) != nil {
			continue
		}
		text := strings.TrimSpace(obj.Console)
		if text == "" {
			text = strings.TrimSpace(obj.Output)
		}
		if text == "" {
			var lines []string
			for _, l := range obj.Logs {
				if strings.TrimSpace(l.Text) != "" {
					lines = append(lines, l.Text)
				}
			}
			text = strings.Join(lines, "\n")
		}
		if text != "" {
			b.WriteString("\n")
			b.WriteString(text)
		}
	}
	return b.String(), true
}

func formatFileSearchCall(raw json.RawMessage) (string, bool) {
	var item struct {
		Status  string   `json:"status"`
		Queries []string `json:"queries"`
		Results []struct {
			Filename string  `json:"filename"`
			Score    float64 `json:"score"`
			Text     string  `json:"text"`
		} `json:"results"`
	}
	if err := json.Unmarshal(raw, &item); err != nil {
		return "", false
	}
	var b strings.Builder
	fmt.Fprintf(&b, "[file_search status=%s", item.Status)
	if len(item.Queries) > 0 {
		quoted := make([]string, 0, len(item.Queries))
		for _, q := range item.Queries {
			quoted = append(quoted, fmt.Sprintf("%q", q))
		}
		fmt.Fprintf(&b, " queries: %s", strings.Join(quoted, ", "))
	}
	b.WriteString("]")
	for _, r := range item.Results {
		name := r.Filename
		if name == "" {
			name = "(unnamed)"
		}
		fmt.Fprintf(&b, "\n- %s (%.2f)", name, r.Score)
		if snippet := strings.TrimSpace(r.Text); snippet != "" {
			fmt.Fprintf(&b, ": %s", truncateRunes(snippet, 200))
		}
	}
	return b.String(), true
}
