package provider

import (
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/config"
	"google.golang.org/genai"
)

// TestGeminiServerToolsDeclaration verifies configured built-in tools render
// as separate genai.Tool entries, disjoint from FunctionDeclarations, and
// unknown declarations are ignored (fail closed).
func TestGeminiServerToolsDeclaration(t *testing.T) {
	p := &GeminiProvider{}
	p.SetServerTools([]config.ServerToolConfig{
		{Type: "google_search"},
		{Type: "url_context"},
		{Type: "web_search"},                   // alias → second GoogleSearch entry
		{Type: "definitely_not_real_20260101"}, // ignored
	})

	tools := p.convertTools(nil)
	if len(tools) != 3 {
		t.Fatalf("expected 3 builtin tool entries, got %d", len(tools))
	}
	var googleSearch, urlContext int
	for _, tool := range tools {
		if tool.FunctionDeclarations != nil {
			t.Fatalf("builtin-only config must not emit FunctionDeclarations entry")
		}
		if tool.GoogleSearch != nil {
			googleSearch++
		}
		if tool.URLContext != nil {
			urlContext++
		}
	}
	if googleSearch != 2 || urlContext != 1 {
		t.Fatalf("unexpected builtin mix: googleSearch=%d urlContext=%d", googleSearch, urlContext)
	}
}

// TestGeminiServerToolsMixedWithFunctions verifies function declarations and
// built-in tools coexist in separate Tool entries (Gemini API contract).
func TestGeminiServerToolsMixedWithFunctions(t *testing.T) {
	p := &GeminiProvider{}
	p.SetServerTools([]config.ServerToolConfig{{Type: "google_search"}})

	tools := p.convertTools([]ToolDefinition{
		{Name: "read_file", Description: "read a file"},
	})
	if len(tools) != 2 {
		t.Fatalf("expected 2 tool entries (builtin + funcs), got %d", len(tools))
	}
	if tools[0].GoogleSearch == nil {
		t.Fatalf("first entry should be the builtin GoogleSearch tool")
	}
	if len(tools[1].FunctionDeclarations) != 1 || tools[1].FunctionDeclarations[0].Name != "read_file" {
		t.Fatalf("second entry should carry the function declaration")
	}
}

// TestGeminiToolChoiceAnyDowngrade verifies FunctionCallingConfigMode=ANY is
// degraded to AUTO when built-in tools are configured (Gemini API rejects
// the combination).
func TestGeminiToolChoiceAnyDowngrade(t *testing.T) {
	p := &GeminiProvider{}
	p.SetToolChoice("required")
	p.SetServerTools([]config.ServerToolConfig{{Type: "google_search"}})

	cfg := &genai.GenerateContentConfig{}
	p.applyToolChoice(cfg, []ToolDefinition{{Name: "read_file"}})
	if cfg.ToolConfig == nil || cfg.ToolConfig.FunctionCallingConfig == nil {
		t.Fatalf("expected ToolConfig to be set")
	}
	if got := cfg.ToolConfig.FunctionCallingConfig.Mode; got != genai.FunctionCallingConfigModeAuto {
		t.Fatalf("ANY must downgrade to AUTO with builtin tools, got %v", got)
	}

	// Without builtin tools, ANY stays ANY.
	p2 := &GeminiProvider{}
	p2.SetToolChoice("required")
	cfg2 := &genai.GenerateContentConfig{}
	p2.applyToolChoice(cfg2, []ToolDefinition{{Name: "read_file"}})
	if got := cfg2.ToolConfig.FunctionCallingConfig.Mode; got != genai.FunctionCallingConfigModeAny {
		t.Fatalf("ANY should be preserved without builtin tools, got %v", got)
	}
}

// TestGeminiGroundingAccum verifies query/source dedup across stream chunks.
func TestGeminiGroundingAccum(t *testing.T) {
	a := newGeminiGroundingAccum()
	a.add(&genai.GroundingMetadata{
		WebSearchQueries: []string{"ggcode release", "ggcode release"},
		GroundingChunks: []*genai.GroundingChunk{
			{Web: &genai.GroundingChunkWeb{Title: "Example", URI: "https://example.com/a"}},
		},
	})
	a.add(&genai.GroundingMetadata{
		GroundingChunks: []*genai.GroundingChunk{
			{Web: &genai.GroundingChunkWeb{URI: "https://example.com/a"}}, // dup URI
			{Web: &genai.GroundingChunkWeb{URI: "https://example.com/b"}}, // no title
		},
	})

	s := a.summary()
	if !strings.Contains(s, "[grounding] search queries: ggcode release") {
		t.Fatalf("summary missing deduped query: %q", s)
	}
	if n := strings.Count(s, "https://example.com/a"); n != 1 {
		t.Fatalf("source URI not deduped (%d occurrences): %q", n, s)
	}
	if !strings.Contains(s, "  - https://example.com/b") {
		t.Fatalf("titleless source should render as bare URI: %q", s)
	}

	// nil metadata and empty accumulator are no-ops.
	if newGeminiGroundingAccum().add(nil).summary() != "" {
		t.Fatalf("empty accum should summarize to empty string")
	}
}

// TestGeminiConvertResponseGroundingBlock verifies Chat responses carry a
// grounding sources text block when GroundingMetadata is present.
func TestGeminiConvertResponseGroundingBlock(t *testing.T) {
	p := &GeminiProvider{}
	resp := &genai.GenerateContentResponse{
		Candidates: []*genai.Candidate{{
			Content: &genai.Content{Parts: []*genai.Part{{Text: "The answer is 42."}}},
			GroundingMetadata: &genai.GroundingMetadata{
				WebSearchQueries: []string{"meaning of life"},
				GroundingChunks: []*genai.GroundingChunk{
					{Web: &genai.GroundingChunkWeb{Title: "Wikipedia", URI: "https://en.wikipedia.org/wiki/42"}},
				},
			},
		}},
	}

	blocks, _ := p.convertResponse(resp)
	if len(blocks) != 2 {
		t.Fatalf("expected text + grounding blocks, got %d", len(blocks))
	}
	last := blocks[len(blocks)-1]
	if last.Type != "text" || !strings.Contains(last.Text, "en.wikipedia.org/wiki/42") || !strings.Contains(last.Text, "meaning of life") {
		t.Fatalf("grounding block missing source/query info: %+v", last)
	}

	// No grounding metadata → no extra block.
	resp.Candidates[0].GroundingMetadata = nil
	blocks, _ = p.convertResponse(resp)
	if len(blocks) != 1 {
		t.Fatalf("expected single text block without grounding metadata, got %d", len(blocks))
	}
}

// TestGeminiRegistryInjectsServerTools verifies the registry gemini leg wires
// resolved.ServerTools into the provider end-to-end, dropping unsupported
// declarations along the way.
func TestGeminiRegistryInjectsServerTools(t *testing.T) {
	prov, err := NewProvider(&config.ResolvedEndpoint{
		Protocol:    "gemini",
		APIKey:      "dummy",
		Model:       "gemini-2.5-flash",
		MaxTokens:   128,
		ServerTools: []config.ServerToolConfig{{Type: "google_search"}, {Type: "bogus_tool"}},
	})
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	gp, ok := prov.(*GeminiProvider)
	if !ok {
		t.Fatalf("expected *GeminiProvider, got %T", prov)
	}
	tools := gp.convertTools(nil)
	if len(tools) != 1 || tools[0].GoogleSearch == nil {
		t.Fatalf("expected single GoogleSearch builtin entry (bogus_tool dropped), got %+v", tools)
	}
}
