package agent

import "testing"

// Pattern 7 (sa-98): grep/search_files → lsp_workspace_symbols same-symbol
// duplicate retrieval. Process-axis check: outcome gates see "found it";
// only the sequence sees the wasted second retrieval (PRM survey, arXiv
// 2510.08049).

func TestToolSequenceSymbolRetrievalDupFires(t *testing.T) {
	v := newToolSequenceValidator()
	// grep for the symbol first (bare symbol pattern)
	g := v.record(mkToolCall("grep", map[string]interface{}{
		"pattern": "extractPrimaryPath",
	}), 1)
	if g != "" {
		t.Fatalf("first grep must not trigger: got %q", g)
	}
	// Then LSP workspace symbol search for the same symbol
	g = v.record(mkToolCall("lsp_workspace_symbols", map[string]interface{}{
		"query": "extractPrimaryPath",
	}), 2)
	if g == "" {
		t.Fatal("grep then lsp_workspace_symbols on same symbol should trigger")
	}
	// Fires once per run
	g = v.record(mkToolCall("lsp_workspace_symbols", map[string]interface{}{
		"query": "parseArgsMap",
	}), 3)
	// note: parseArgsMap was never grepped, so this specific call wouldn't
	// fire anyway; the once-per-run flag is asserted directly.
	if v.hintsGiven["symbol_retrieval_dup"] != true {
		t.Fatal("hint should be marked as given")
	}
}

func TestToolSequenceSymbolRetrievalDupPatternWrapsSymbol(t *testing.T) {
	// A grep regex wrapping the bare symbol still aligns with the query.
	v := newToolSequenceValidator()
	v.record(mkToolCall("grep", map[string]interface{}{
		"pattern": `^\s*MyFunc\(`,
	}), 1)
	g := v.record(mkToolCall("lsp_workspace_symbols", map[string]interface{}{
		"query": "MyFunc",
	}), 2)
	if g == "" {
		t.Fatal("regex-wrapped pattern containing the query should trigger")
	}
}

func TestToolSequenceSymbolRetrievalDupDifferentSymbol(t *testing.T) {
	v := newToolSequenceValidator()
	v.record(mkToolCall("grep", map[string]interface{}{
		"pattern": "extractPrimaryPath",
	}), 1)
	g := v.record(mkToolCall("lsp_workspace_symbols", map[string]interface{}{
		"query": "parseArgsMap",
	}), 2)
	if g != "" {
		t.Fatalf("different symbol must not trigger: got %q", g)
	}
}

func TestToolSequenceSymbolRetrievalDupStalenessExemption(t *testing.T) {
	// A source edit between the two retrievals invalidates the earlier hit
	// set (#2813 staleness principle) - the LSP re-query is legitimate.
	v := newToolSequenceValidator()
	v.record(mkToolCall("grep", map[string]interface{}{
		"pattern": "extractPrimaryPath",
	}), 1)
	v.record(mkToolCall("edit_file", map[string]interface{}{
		"file_path": "/foo/bar.go",
		"old_text":  "a",
		"new_text":  "b",
	}), 2)
	g := v.record(mkToolCall("lsp_workspace_symbols", map[string]interface{}{
		"query": "extractPrimaryPath",
	}), 3)
	if g != "" {
		t.Fatalf("post-edit re-query is legitimate (staleness): got %q", g)
	}
}

func TestToolSequenceSymbolRetrievalDupWindowExpired(t *testing.T) {
	v := newToolSequenceValidator()
	v.record(mkToolCall("grep", map[string]interface{}{
		"pattern": "extractPrimaryPath",
	}), 1)
	// Gap > seqRetrievalGap (3): earlier result may be stale by then.
	g := v.record(mkToolCall("lsp_workspace_symbols", map[string]interface{}{
		"query": "extractPrimaryPath",
	}), 5)
	if g != "" {
		t.Fatalf("outside retrieval gap must not trigger: got %q", g)
	}
}

func TestToolSequenceSymbolRetrievalDupReverseDirectionAllowed(t *testing.T) {
	// LSP first then grep is the documented fallback path when LSP is cold
	// (tool_error_fallback) - must NOT fire.
	v := newToolSequenceValidator()
	v.record(mkToolCall("lsp_workspace_symbols", map[string]interface{}{
		"query": "extractPrimaryPath",
	}), 1)
	g := v.record(mkToolCall("grep", map[string]interface{}{
		"pattern": "extractPrimaryPath",
	}), 2)
	if g != "" {
		t.Fatalf("LSP->grep fallback direction must not trigger: got %q", g)
	}
}

func TestToolSequenceSymbolRetrievalDupShortSymbol(t *testing.T) {
	// Short symbols/queries match too easily - below the floor, no fire.
	v := newToolSequenceValidator()
	v.record(mkToolCall("grep", map[string]interface{}{
		"pattern": "err",
	}), 1)
	g := v.record(mkToolCall("lsp_workspace_symbols", map[string]interface{}{
		"query": "err",
	}), 2)
	if g != "" {
		t.Fatalf("short symbol below floor must not trigger: got %q", g)
	}
}
