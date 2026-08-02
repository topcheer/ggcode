package tool

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
)

func TestTrimToolDescriptions_LongMCPDescription(t *testing.T) {
	longDesc := strings.Repeat("This is a long description. ", 50) // ~1350 bytes
	defs := []provider.ToolDefinition{
		{Name: "mcp__server__tool1", Description: longDesc, Parameters: json.RawMessage(`{"type":"object"}`)},
		{Name: "mcp__server__tool2", Description: "Short desc.", Parameters: json.RawMessage(`{"type":"object"}`)},
	}
	result := trimToolDescriptions(defs)

	if len(result[0].Description) > maxDescriptionBytes+10 {
		t.Errorf("trimmed description still too long: %d bytes", len(result[0].Description))
	}
	if len(result[1].Description) >= len("Short desc.") {
		// short desc should be unchanged
		if result[1].Description != "Short desc." {
			t.Errorf("short description should not be changed, got: %q", result[1].Description)
		}
	}
	// parameters should be unchanged
	if string(result[0].Parameters) != `{"type":"object"}` {
		t.Errorf("parameters should not be modified")
	}
}

func TestTrimToolDescriptions_BuiltinExempt(t *testing.T) {
	longDesc := strings.Repeat("Built-in description. ", 50)
	defs := []provider.ToolDefinition{
		{Name: "read_file", Description: longDesc},
	}
	result := trimToolDescriptions(defs)
	if result[0].Description != longDesc {
		t.Errorf("built-in tool descriptions should not be trimmed")
	}
}

func TestTrimToolDescriptions_ParagraphCut(t *testing.T) {
	// First paragraph under limit, second paragraph pushes over
	desc := "This tool does X.\n\n" + strings.Repeat("Verbose details. ", 50)
	defs := []provider.ToolDefinition{
		{Name: "mcp__srv__t", Description: desc},
	}
	result := trimToolDescriptions(defs)
	if !strings.HasSuffix(result[0].Description, "…") {
		t.Errorf("expected truncation marker, got: %q", result[0].Description)
	}
	if !strings.HasPrefix(result[0].Description, "This tool does X.") {
		t.Errorf("first paragraph should be preserved, got: %q", result[0].Description)
	}
}

func TestTrimToolDescriptions_SentenceCut(t *testing.T) {
	// No paragraph breaks, but sentence boundaries exist
	desc := strings.Repeat("This is a sentence. ", 50)
	defs := []provider.ToolDefinition{
		{Name: "mcp__srv__t", Description: desc},
	}
	result := trimToolDescriptions(defs)
	if len(result[0].Description) > maxDescriptionBytes+10 {
		t.Errorf("description should be truncated: %d bytes", len(result[0].Description))
	}
}

func TestTrimToolDescriptions_NoOp(t *testing.T) {
	shortDesc := "Short description."
	defs := []provider.ToolDefinition{
		{Name: "mcp__srv__t", Description: shortDesc},
	}
	result := trimToolDescriptions(defs)
	if result[0].Description != shortDesc {
		t.Errorf("short description should be unchanged")
	}
}

func TestTrimDescription_HardCut(t *testing.T) {
	// No paragraph or sentence breaks, just continuous text
	desc := strings.Repeat("a", 1000)
	result := trimDescription(desc)
	if len(result) > maxDescriptionBytes+10 {
		t.Errorf("expected hard cut at maxDescriptionBytes, got %d bytes", len(result))
	}
	if !strings.HasSuffix(result, "…") {
		t.Errorf("expected truncation marker for hard cut")
	}
}

func TestFindParagraphCut(t *testing.T) {
	desc := "First para.\n\nSecond para that is long."
	cut := findParagraphCut(desc, 500)
	// "First para." is 11 chars, then \n\n — cut should be at 11
	if cut != 11 {
		t.Errorf("expected cut at 11, got %d", cut)
	}

	// No paragraph break within limit
	desc = strings.Repeat("a", 300)
	cut = findParagraphCut(desc, 200)
	if cut != 0 {
		t.Errorf("expected 0 for no break, got %d", cut)
	}
}

func TestFindSentenceCut(t *testing.T) {
	desc := "First sentence. Second sentence. Third sentence. Fourth."
	cut := findSentenceCut(desc, 50)
	if cut <= 0 {
		t.Errorf("expected sentence cut > 0, got %d", cut)
	}
	if cut > 50 {
		t.Errorf("sentence cut should be within limit: %d > 50", cut)
	}
}

func TestFilter_AppliesDescriptionBudgetBelowThreshold(t *testing.T) {
	f := NewRelevanceFilter()
	longDesc := strings.Repeat("Long MCP tool description. ", 30)
	defs := []provider.ToolDefinition{
		{Name: "mcp__srv__t", Description: longDesc},
	}
	// Even with fewer than minToolsToActivate, descriptions should be trimmed
	result := f.Filter(defs, "test context")
	if len(result[0].Description) >= len(longDesc) {
		t.Errorf("description should be trimmed even below tool count threshold")
	}
}
