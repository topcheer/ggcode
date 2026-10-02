package provider

import "testing"

// TestSetServerToolsEmptyDisables (#3073): setting tools then an empty set
// must CLEAR the stored server tools - the disable path previously did not
// exist because `len(kept) > 0` skipped the empty assignment, leaving
// web_search/code_interpreter active on provider reuse after the user
// removed server_tools from the config. Also pins the fail-closed contract:
// an unrecognized-only set keeps the previous value.
func TestSetServerToolsEmptyDisables(t *testing.T) {
	p := &OpenAIResponsesProvider{}

	p.SetServerTools([]ServerToolConfig{{Type: "web_search"}})
	if len(p.serverTools) != 1 {
		t.Fatalf("expected 1 stored tool after web_search, got %d", len(p.serverTools))
	}

	// Disable: empty set clears (the #3073 fix).
	p.SetServerTools(nil)
	if len(p.serverTools) != 0 {
		t.Fatalf("SetServerTools(nil) must clear stored tools, still %d", len(p.serverTools))
	}
	p.SetServerTools([]ServerToolConfig{{Type: "web_search"}})
	p.SetServerTools([]ServerToolConfig{})
	if len(p.serverTools) != 0 {
		t.Fatalf("SetServerTools([]) must clear stored tools, still %d", len(p.serverTools))
	}

	// Fail-closed: an unrecognized-only set keeps the previous value.
	p.SetServerTools([]ServerToolConfig{{Type: "code_interpreter"}})
	p.SetServerTools([]ServerToolConfig{{Type: "definitely_not_a_tool"}})
	if len(p.serverTools) != 1 || p.serverTools[0].Type != "code_interpreter" {
		t.Fatalf("unrecognized-only set must keep previous tools (fail closed), got %v", p.serverTools)
	}

	// Mixed set applies only the recognized entries.
	p.SetServerTools([]ServerToolConfig{{Type: "file_search", VectorStoreIDs: []string{"vs_1"}}, {Type: "nope"}})
	if len(p.serverTools) != 1 || p.serverTools[0].Type != "file_search" {
		t.Fatalf("mixed set must keep only recognized tools, got %v", p.serverTools)
	}
}
