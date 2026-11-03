package agent

import "testing"

func TestToolSequenceSymbolRetrievalDupFires(t *testing.T) {
	v := newToolSequenceValidator()

	g := v.record(mkToolCall("grep", map[string]interface{}{
		"pattern": "extractPrimaryPath",
	}), 1)
	if g != "" {
		t.Fatalf("first grep must not trigger: got %q", g)
	}

	g = v.record(mkToolCall("lsp_workspace_symbols", map[string]interface{}{
		"query": "extractPrimaryPath",
	}), 2)
	if g == "" {
		t.Fatal("grep then lsp_workspace_symbols on same symbol should trigger")
	}

	g = v.record(mkToolCall("lsp_workspace_symbols", map[string]interface{}{
		"query": "parseArgsMap",
	}), 3)

	if v.hintsGiven["symbol_retrieval_dup"] != true {
		t.Fatal("hint should be marked as given")
	}
}

func TestToolSequenceSymbolRetrievalDupPatternWrapsSymbol(t *testing.T) {

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

	g := v.record(mkToolCall("lsp_workspace_symbols", map[string]interface{}{
		"query": "extractPrimaryPath",
	}), 5)
	if g != "" {
		t.Fatalf("outside retrieval gap must not trigger: got %q", g)
	}
}

func TestToolSequenceSymbolRetrievalDupReverseDirectionAllowed(t *testing.T) {

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
