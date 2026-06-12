package main

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/topcheer/ggcode/internal/harness"
)

func TestParseContextSpecsSupportsNamesAndPaths(t *testing.T) {
	got := harness.ParseContextSpecs("checkout, payments=services/payments, ops:infra/ops")
	want := []harness.ContextConfig{
		{Name: "checkout", Description: "User-provided bounded context"},
		{Name: "ops", Path: filepath.Join("infra", "ops"), Description: "User-provided bounded context", RequireAgent: true},
		{Name: "payments", Path: filepath.Join("services", "payments"), Description: "User-provided bounded context", RequireAgent: true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseContextSpecs() = %#v, want %#v", got, want)
	}
}

func TestSelectSuggestedContextsUsesOneBasedIndexes(t *testing.T) {
	suggestions := []harness.ContextConfig{
		{Name: "checkout"},
		{Name: "payments"},
		{Name: "ops"},
	}
	got := selectSuggestedContexts(suggestions, "2, 3")
	want := []harness.ContextConfig{
		{Name: "ops"},
		{Name: "payments"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("selectSuggestedContexts() = %#v, want %#v", got, want)
	}
}

func TestResolveExistingContextInputPrefersKnownContext(t *testing.T) {
	cfg := &harness.Config{
		Contexts: []harness.ContextConfig{
			{Name: "payments", Path: filepath.Join("services", "payments"), RequireAgent: true},
		},
	}
	parsed := harness.ParseContextSpecs("payments")[0]
	got := resolveExistingContextInput(cfg, "payments", parsed)
	if got == nil || got.Name != "payments" || got.Path != filepath.Join("services", "payments") {
		t.Fatalf("resolveExistingContextInput() = %#v", got)
	}
}
