package provider

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/config"
)

func TestResponsesBuiltinToolsJSON(t *testing.T) {
	tools := responsesBuiltinTools([]config.ServerToolConfig{
		{Type: "code_interpreter", MemoryLimit: "4g", FileIDs: []string{"file-1", " file-2 ", ""}},
		{Type: "file_search", VectorStoreIDs: []string{"vs_1"}, MaxNumResults: 5},
		{Type: "definitely_not_real_20260101"},               // unknown: dropped
		{Type: "file_search", VectorStoreIDs: []string{" "}}, // malformed: dropped
	})
	if len(tools) != 2 {
		t.Fatalf("expected 2 builtin tools, got %d: %+v", len(tools), tools)
	}
	b, _ := json.Marshal(tools)
	s := string(b)
	if !strings.Contains(s, `"type":"code_interpreter"`) || !strings.Contains(s, `"memory_limit":"4g"`) {
		t.Errorf("code_interpreter envelope wrong: %s", s)
	}
	if !strings.Contains(s, `"file_ids":["file-1","file-2"]`) {
		t.Errorf("file_ids trimming failed: %s", s)
	}
	if !strings.Contains(s, `"vector_store_ids":["vs_1"],"max_num_results":5`) {
		t.Errorf("file_search envelope wrong: %s", s)
	}
	if strings.Contains(s, `"definitely_not_real`) {
		t.Errorf("unknown tool leaked: %s", s)
	}
}

func TestResponsesFunctionToolNameOmittedOnlyWhenEmpty(t *testing.T) {
	fn, _ := json.Marshal(responsesTool{Type: "function", Name: "bash"})
	if !strings.Contains(string(fn), `"name":"bash"`) {
		t.Errorf("function name dropped: %s", fn)
	}
	builtin, _ := json.Marshal(responsesTool{Type: "code_interpreter"})
	if strings.Contains(string(builtin), `"name"`) {
		t.Errorf("builtin tool must not carry a name field: %s", builtin)
	}
}

func TestResponsesSetServerToolsFailClosed(t *testing.T) {
	p := NewOpenAIResponsesProvider("k", "gpt-5.2", 1024, "http://127.0.0.1:9/v1")
	p.SetServerTools([]config.ServerToolConfig{
		{Type: "code_interpreter"},
		{Type: "FILE_SEARCH", VectorStoreIDs: []string{"vs_1"}}, // case-insensitive
		{Type: "web_search_preview"},                            // hosted family: kept (union semantics)
		{Type: "bogus_tool"},                                    // unsupported: dropped
		{Type: ""},
	})
	if len(p.serverTools) != 3 {
		t.Fatalf("expected 3 kept tools, got %d: %+v", len(p.serverTools), p.serverTools)
	}
	// A reload with no recognized entries must not wipe the previous set.
	p.SetServerTools([]config.ServerToolConfig{{Type: "bogus_tool"}})
	if len(p.serverTools) != 3 {
		t.Fatalf("unrecognized reload wiped stored set: %+v", p.serverTools)
	}
}

func TestResponsesBuildRequestBuiltinInclude(t *testing.T) {
	p := NewOpenAIResponsesProvider("k", "gpt-5.2", 1024, "http://127.0.0.1:9/v1")
	p.SetServerTools([]config.ServerToolConfig{{Type: "file_search", VectorStoreIDs: []string{"vs_x"}}})
	req, err := p.buildRequest(nil, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, inc := range req.Include {
		if inc == responsesIncludeFileSearchResults {
			found = true
		}
	}
	if !found {
		t.Errorf("file_search include missing: %+v", req.Include)
	}
	var toolTypes []string
	for _, bt := range req.Tools {
		toolTypes = append(toolTypes, bt.Type)
	}
	if len(toolTypes) != 1 || toolTypes[0] != "file_search" {
		t.Errorf("expected only file_search tool entry, got %v", toolTypes)
	}

	plain := NewOpenAIResponsesProvider("k", "gpt-5.2", 1024, "http://127.0.0.1:9/v1")
	req2, err := plain.buildRequest(nil, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(req2.Include) != 0 {
		for _, inc := range req2.Include {
			if inc == responsesIncludeFileSearchResults {
				t.Errorf("file_search include must be absent without server tools: %+v", req2.Include)
			}
		}
	}
}

func TestFormatResponsesServerItem(t *testing.T) {
	ci, ok := formatResponsesServerItem(json.RawMessage(`{
		"type":"code_interpreter_call","status":"completed","container_id":"cntr_1",
		"code":"print(sum(range(10)))",
		"outputs":[{"type":"console","console":"45\n"}]}`))
	if !ok || !strings.Contains(ci, "status=completed") || !strings.Contains(ci, "cntr_1") ||
		!strings.Contains(ci, "```python") || !strings.Contains(ci, "45") {
		t.Errorf("code_interpreter formatting wrong: ok=%v %q", ok, ci)
	}
	fs, ok := formatResponsesServerItem(json.RawMessage(`{
		"type":"file_search_call","status":"completed","queries":["ggcode","responses"],
		"results":[{"filename":"design.md","score":0.91,"text":"` + strings.Repeat("x", 300) + `"}]}`))
	if !ok || !strings.Contains(fs, `"ggcode", "responses"`) || !strings.Contains(fs, "design.md (0.91)") {
		t.Errorf("file_search formatting wrong: ok=%v %q", ok, fs)
	}
	if strings.Count(fs, "x") > 201 {
		t.Errorf("snippet not truncated: %q", fs)
	}
	if _, ok := formatResponsesServerItem(json.RawMessage(`{"type":"message"}`)); ok {
		t.Error("non-server item must not be formatted")
	}
	// object-shaped console output (alternate API revision) must parse
	ci2, ok := formatResponsesServerItem(json.RawMessage(`{
		"type":"code_interpreter_call","status":"completed","container_id":"c2","code":"1",
		"outputs":[{"type":"console","console":{"outputs":[{"type":"logs","text":"hello"}]}}]}`))
	// object console is not a plain string: tolerated, no crash, no console text
	if !ok || !strings.Contains(ci2, "c2") {
		t.Errorf("object console shape handling wrong: ok=%v %q", ok, ci2)
	}
}

func TestAppendResponsesCitations(t *testing.T) {
	got := appendResponsesCitations("Chart generated.", []responseAnnotation{
		{Type: "container_file_citation", FileID: "f1", Filename: "chart.png", ContainerID: "c1"},
		{Type: "file_citation", FileID: "f2", Filename: "notes.md", Quote: strings.Repeat("q", 100)},
		{Type: "container_file_citation", FileID: "f1", Filename: "chart.png"}, // duplicate: collapsed
		{Type: "unknown_kind"}, // ignored
	})
	want := "Chart generated.\n[container file: chart.png (f1)]\n[file: notes.md (f2) \"" + strings.Repeat("q", 80) + "...\"]"
	if got != want {
		t.Errorf("citations wrong:\n got: %q\nwant: %q", got, want)
	}
	if same := appendResponsesCitations("plain", nil); same != "plain" {
		t.Errorf("no-annotation passthrough broken: %q", same)
	}
}
