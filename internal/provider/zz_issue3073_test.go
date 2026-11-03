package provider

import "testing"

func TestSetServerToolsEmptyDisables(t *testing.T) {
	p := &OpenAIResponsesProvider{}

	p.SetServerTools([]ServerToolConfig{{Type: "web_search"}})
	if len(p.serverTools) != 1 {
		t.Fatalf("expected 1 stored tool after web_search, got %d", len(p.serverTools))
	}

	p.SetServerTools(nil)
	if len(p.serverTools) != 0 {
		t.Fatalf("SetServerTools(nil) must clear stored tools, still %d", len(p.serverTools))
	}
	p.SetServerTools([]ServerToolConfig{{Type: "web_search"}})
	p.SetServerTools([]ServerToolConfig{})
	if len(p.serverTools) != 0 {
		t.Fatalf("SetServerTools([]) must clear stored tools, still %d", len(p.serverTools))
	}

	p.SetServerTools([]ServerToolConfig{{Type: "code_interpreter"}})
	p.SetServerTools([]ServerToolConfig{{Type: "definitely_not_a_tool"}})
	if len(p.serverTools) != 1 || p.serverTools[0].Type != "code_interpreter" {
		t.Fatalf("unrecognized-only set must keep previous tools (fail closed), got %v", p.serverTools)
	}

	p.SetServerTools([]ServerToolConfig{{Type: "file_search", VectorStoreIDs: []string{"vs_1"}}, {Type: "nope"}})
	if len(p.serverTools) != 1 || p.serverTools[0].Type != "file_search" {
		t.Fatalf("mixed set must keep only recognized tools, got %v", p.serverTools)
	}
}
