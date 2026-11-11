package tool

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

type stubSymbolsProvider struct {
	pkgs []PackageSymbols
	err  error
}

func (s stubSymbolsProvider) Symbols(lang, query string, limit int) ([]PackageSymbols, error) {
	return s.pkgs, s.err
}

// P3 (#5446): the symbols tool is the on-demand retrieval path that replaced
// the prompt-inlined symbol maps.
func TestSymbolsToolServesPackageExports(t *testing.T) {
	tt := SymbolsTool{Provider: stubSymbolsProvider{pkgs: []PackageSymbols{
		{Language: "go", Path: "internal/agent", Symbols: []string{"Agent", "Run()"}},
	}}}
	res, err := tt.Execute(context.Background(), json.RawMessage(`{"query":"agent","language":"go"}`))
	if err != nil || res.IsError {
		t.Fatalf("execute: err=%v isError=%v", err, res.IsError)
	}
	for _, want := range []string{"[go] internal/agent", "Agent", "Run()", "1 packages"} {
		if !strings.Contains(res.Content, want) {
			t.Fatalf("output missing %q:\n%s", want, res.Content)
		}
	}
}

func TestSymbolsToolInvalidLanguage(t *testing.T) {
	tt := SymbolsTool{Provider: stubSymbolsProvider{}}
	res, err := tt.Execute(context.Background(), json.RawMessage(`{"language":"rust"}`))
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !res.IsError || !strings.Contains(res.Content, "invalid language") {
		t.Fatalf("expected invalid-language error, got: %s", res.Content)
	}
}

func TestSymbolsToolNoProvider(t *testing.T) {
	var tt SymbolsTool
	if tt.Available() {
		t.Fatal("Available must be false without a provider")
	}
	res, _ := tt.Execute(context.Background(), nil)
	if !res.IsError {
		t.Fatal("expected error result without provider")
	}
}
