package provider

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
)

// OpenAI Responses built-in (server-side) tools (sa-63).
//
// The Responses API hosts certain tools inside OpenAI's infrastructure: the
// model invokes them and the result is folded into the response in-band, so
// the agent loop never sees a client-side function_call for them. This file
// wires the declarative `server_tools` endpoint configuration (shared with
// the Anthropic and Gemini legs) into the Responses request envelope and
// surfaces the server-executed artifacts (code interpreter transcripts,
// file search hits, generated-file citations) back into the conversation as
// text, mirroring the Gemini grounding leg.

// responsesIncludeFileSearchResults asks the API to inline retrieved chunk
// text in file_search_call items so the agent can quote its sources.
const responsesIncludeFileSearchResults = "file_search_call.results"

// SetServerTools implements provider.ServerToolsSetter: declarative OpenAI
// Responses built-in tools executed inside the API. Recognized types:
//   - "code_interpreter" — sandboxed Python (auto container with optional
//     memory_limit tier and seed file_ids)
//   - "file_search"      — semantic search over vector stores (requires
//     vector_store_ids)
//
// Unknown declarations are ignored (fail closed), mirroring the Anthropic
// and Gemini legs.
func (p *OpenAIResponsesProvider) SetServerTools(tools []ServerToolConfig) {
	// Union of the sa-62 hosted set (web_search family) and the sa-63
	// built-in set; unrecognized sets leave the stored value untouched.
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
	if len(kept) > 0 {
		p.serverTools = kept
	}
}

// responsesBuiltinTools converts server_tools declarations into Responses
// API tool entries. Malformed declarations (file_search without vector
// stores) are dropped rather than sent and rejected by the API.
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

// responsesCodeInterpreterContainer builds the `container` property. Auto
// mode is always used (the API creates or reuses a sandboxed VM); optional
// memory_limit and seed file_ids are attached when declared.
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

// responseAnnotation is the subset of message output_text annotations this
// adapter surfaces. Code interpreter files arrive as container_file_citation
// entries; file_search sources arrive as file_citation entries.
type responseAnnotation struct {
	Type        string `json:"type"`
	FileID      string `json:"file_id"`
	Filename    string `json:"filename"`
	ContainerID string `json:"container_id"`
	Quote       string `json:"quote"`
}

// appendResponsesCitations appends one compact line per annotation so
// generated files and retrieval sources survive into the agent transcript
// (raw annotations would otherwise be dropped by the message mapping).
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

// formatResponsesServerItem renders a completed server-executed output item
// (code_interpreter_call / file_search_call) as an inspectable text block.
// The model already consumed the result server-side, so nothing is replayed
// into the tool loop; this is observability only, mirroring the Gemini
// grounding text blocks.
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
	// Output entries come in two shapes across API revisions: a console
	// string directly, or a {console:{outputs:[{text:...}]}} object. Parse
	// each entry tolerantly; unknown shapes contribute nothing.
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
