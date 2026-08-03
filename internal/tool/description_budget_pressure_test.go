package tool

import (
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
)

func TestPressureBudget(t *testing.T) {
	tests := []struct {
		name     string
		pressure float64
		want     int
	}{
		{"zero pressure", 0, budgetAtModerate},
		{"low pressure", 0.3, budgetAtModerate},
		{"moderate boundary", 0.50, budgetAtModerate},
		{"high pressure", 0.75, budgetAtHigh},
		{"critical pressure", 0.90, budgetAtCritical},
		{"max pressure", 1.0, budgetAtCritical},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := pressureBudget(tt.pressure)
			if got != tt.want {
				t.Errorf("pressureBudget(%.2f) = %d, want %d", tt.pressure, got, tt.want)
			}
		})
	}
}

func TestTrimToolDescriptionsWithPressure_LowPressure(t *testing.T) {
	// At low pressure, long descriptions should use standard budget (500 bytes)
	// Description has paragraph breaks so paragraph cut triggers (appends marker)
	longDesc := "This is paragraph one.\n\n" + strings.Repeat("More content here. ", 50)
	defs := []provider.ToolDefinition{
		{Name: "mcp__test__tool1", Description: longDesc},
	}
	result := trimToolDescriptionsWithPressure(defs, 0.2)
	if len(result[0].Description) > budgetAtModerate+20 { // +20 for truncation marker
		t.Errorf("low pressure: description not trimmed to budget, got %d bytes", len(result[0].Description))
	}
	if !strings.Contains(result[0].Description, "\u2026") {
		t.Error("expected truncation marker for paragraph cut")
	}
}

func TestTrimToolDescriptionsWithPressure_HighPressure(t *testing.T) {
	// At high pressure (>0.70), descriptions should be trimmed to budgetAtHigh (250)
	longDesc := strings.Repeat("This is a tool description. ", 30)
	defs := []provider.ToolDefinition{
		{Name: "mcp__test__tool1", Description: longDesc},
	}
	result := trimToolDescriptionsWithPressure(defs, 0.75)
	if len(result[0].Description) > budgetAtHigh+20 {
		t.Errorf("high pressure: description not trimmed to %d budget, got %d bytes",
			budgetAtHigh, len(result[0].Description))
	}
}

func TestTrimToolDescriptionsWithPressure_CriticalPressure(t *testing.T) {
	// At critical pressure (>0.85), descriptions should be trimmed to budgetAtCritical (120)
	longDesc := strings.Repeat("This is a tool description. ", 30)
	defs := []provider.ToolDefinition{
		{Name: "mcp__test__tool1", Description: longDesc},
	}
	result := trimToolDescriptionsWithPressure(defs, 0.90)
	if len(result[0].Description) > budgetAtCritical+20 {
		t.Errorf("critical pressure: description not trimmed to %d budget, got %d bytes",
			budgetAtCritical, len(result[0].Description))
	}
}

func TestTrimToolDescriptionsWithPressure_BuiltinExempt(t *testing.T) {
	// Built-in tools should never be trimmed regardless of pressure
	longDesc := strings.Repeat("Built-in tool description. ", 50)
	defs := []provider.ToolDefinition{
		{Name: "read_file", Description: longDesc},
	}
	result := trimToolDescriptionsWithPressure(defs, 0.95)
	if result[0].Description != longDesc {
		t.Error("built-in tool description should not be trimmed")
	}
}

func TestTrimToolDescriptionsWithPressure_ShortDescriptionUntouched(t *testing.T) {
	// Short descriptions should not be modified even at critical pressure
	shortDesc := "Does a thing."
	defs := []provider.ToolDefinition{
		{Name: "mcp__test__tool1", Description: shortDesc},
	}
	result := trimToolDescriptionsWithPressure(defs, 0.95)
	if result[0].Description != shortDesc {
		t.Error("short description should not be modified")
	}
}

func TestTrimDescriptionTo_ParagraphCut(t *testing.T) {
	// Should cut at paragraph boundary when available
	desc := "First paragraph here.\n\nSecond paragraph with more detail.\n\nThird paragraph."
	result := trimDescriptionTo(desc, 50)
	if strings.Contains(result, "Second paragraph") {
		t.Errorf("should have cut before second paragraph, got: %s", result)
	}
}

func TestTrimDescriptionTo_SentenceCut(t *testing.T) {
	// Should cut at sentence boundary when no paragraph boundary fits
	desc := "This is sentence one. This is sentence two. This is sentence three."
	result := trimDescriptionTo(desc, 40)
	// Should contain a sentence boundary cut, not a hard cut
	if !strings.Contains(result, ". ") && !strings.HasSuffix(result, ".") {
		t.Errorf("expected sentence boundary cut, got: %s", result)
	}
}

func TestFilterWithPressure_PreservesBackwardCompat(t *testing.T) {
	// Filter() (without pressure) should behave the same as before
	defs := []provider.ToolDefinition{
		{Name: "read_file", Description: "Read a file."},
		{Name: "mcp__test__tool1", Description: strings.Repeat("desc ", 120)},
	}
	r1 := (&RelevanceFilter{}).Filter(defs, "test context")
	r2 := (&RelevanceFilter{}).FilterWithPressure(defs, "test context", 0)
	if len(r1) != len(r2) {
		t.Errorf("Filter and FilterWithPressure(0) returned different tool counts: %d vs %d",
			len(r1), len(r2))
	}
}
