package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// PackageSymbols is one package's exported-symbol list served by the symbols
// tool (#5446 P3): the full system-prompt export lists became this on-demand
// retrieval path.
type PackageSymbols struct {
	Language string   `json:"language"` // go | ts | py
	Path     string   `json:"path"`     // workspace-relative package/dir path
	Symbols  []string `json:"symbols"`
}

// SymbolsProvider extracts package symbols for a workspace root. Implemented
// by agentruntime (which owns the Go/TS/Py extractors); injected to avoid an
// import cycle (agentruntime imports tool).
type SymbolsProvider interface {
	Symbols(lang, query string, limit int) ([]PackageSymbols, error)
}

// SymbolsTool serves package export lists on demand (#5446 P3): the
// system prompt now carries per-language counts plus a pointer to this tool.
type SymbolsTool struct {
	Provider SymbolsProvider
}

func (t SymbolsTool) Name() string { return "symbols" }

func (t SymbolsTool) Description() string {
	return "Query exported package symbols per language (go/ts/py) for this workspace - the on-demand replacement for the prompt-injected symbol maps. Use when you need to know what a package exports or where a symbol lives. 'query' matches package paths (substring) and symbol names (prefix/substring); omit it to list the largest packages. Prefer lsp_workspace_symbols when you need symbol POSITIONS in Go."
}

func (t SymbolsTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"query":   {"type": "string", "description": "Substring matched against package paths and symbol names (case-insensitive). Empty = overview of largest packages."},
			"language": {"type": "string", "enum": ["go", "ts", "py"], "description": "Restrict to one language. Default: all."},
			"limit":   {"type": "integer", "description": "Max packages returned (default 12, max 40)."}
		},
		"required": []
	}`)
}

type symbolsToolInput struct {
	Query    string  `json:"query"`
	Language string  `json:"language"`
	Limit    float64 `json:"limit"`
}

func (t SymbolsTool) Available() bool { return t.Provider != nil }

func (t SymbolsTool) Execute(ctx context.Context, input json.RawMessage) (Result, error) {
	if t.Provider == nil {
		return Result{IsError: true, Content: "symbols provider not configured in this runtime"}, nil
	}
	var in symbolsToolInput
	if len(input) > 0 {
		if err := json.Unmarshal(input, &in); err != nil {
			return Result{IsError: true, Content: fmt.Sprintf("invalid input: %v", err)}, nil
		}
	}
	lang := strings.ToLower(strings.TrimSpace(in.Language))
	switch lang {
	case "", "go", "ts", "py":
	default:
		return Result{IsError: true, Content: fmt.Sprintf("invalid language %q (want go|ts|py)", lang)}, nil
	}
	limit := 12
	if in.Limit > 0 {
		limit = int(in.Limit)
	}
	if limit > 40 {
		limit = 40
	}

	pkgs, err := t.Provider.Symbols(lang, strings.TrimSpace(in.Query), limit)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("symbol extraction failed: %v", err)}, nil
	}
	if len(pkgs) == 0 {
		return Result{Content: "No matching packages. Check the query/language, or list the workspace tree with glob first."}, nil
	}

	var b strings.Builder
	total := 0
	for _, p := range pkgs {
		sort.Strings(p.Symbols)
		fmt.Fprintf(&b, "[%s] %s (%d symbols): %s\n", p.Language, p.Path, len(p.Symbols), strings.Join(p.Symbols, ", "))
		total += len(p.Symbols)
	}
	fmt.Fprintf(&b, "(%d packages, %d symbols)\n", len(pkgs), total)
	return Result{Content: strings.TrimRight(b.String(), "\n")}, nil
}
