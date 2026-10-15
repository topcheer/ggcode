package lsp

// Offline coverage boost for the client query surface: the public query
// helpers (Hover/Definition/References/Implementation/DocumentHighlights/
// DocumentSymbols/PrepareCallHierarchy/IncomingCalls/OutgoingCalls/
// Diagnostics) plus the parse/retry helpers were near 0% because the
// integration tests require a real gopls. Here testdata/fakelsp - a tiny Go
// LSP server compiled into a PATH-injected "gopls" (same discovery pattern
// as discovery_test.go / issue993_test.go) - answers every JSON-RPC request
// by method name, so the full stack - resolution, handshake, didOpen,
// request, parse - runs offline.

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ── fake gopls server ────────────────────────────────────────────────────

// buildFakeGopls compiles testdata/fakelsp into binDir/gopls so that
// PATH-based discovery resolves it as the Go language server. The fake
// answers each JSON-RPC request by method name (see fakelsp/main.go) and
// learns the real document URI from the didOpen notification.
func buildFakeGopls(t *testing.T, binDir string) {
	t.Helper()
	src := filepath.Join("testdata", "fakelsp", "main.go")
	if _, err := os.Stat(src); err != nil {
		t.Fatalf("fakelsp source: %v", err)
	}
	cmd := exec.Command("go", "build", "-o", filepath.Join(binDir, "gopls"), src)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build fakelsp: %v\n%s", err, out)
	}
}

func TestFakeGoplsFullStackQueries(t *testing.T) {
	dir := t.TempDir()
	ws := filepath.Join(dir, "ws")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(ws, "go.mod"), []byte("module example.com/fake\n"), 0o644); err != nil {
		t.Fatalf("go.mod: %v", err)
	}
	mainPath := filepath.Join(ws, "main.go")
	if err := os.WriteFile(mainPath, []byte("package main\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatalf("main.go: %v", err)
	}
	binDir := t.TempDir()
	buildFakeGopls(t, binDir)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	withIsolatedManager(t, func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		mainFile := mainPath
		pos := Position{Line: 1, Character: 5}

		hov, err := Hover(ctx, ws, mainFile, pos)
		if err != nil || hov != "func Foo() int" {
			t.Fatalf("Hover = %q, %v", hov, err)
		}

		defs, err := Definition(ctx, ws, mainFile, pos)
		if err != nil || len(defs) != 1 || defs[0].Path != mainFile || defs[0].Range.Start.Line != 2 {
			t.Fatalf("Definition = %+v, %v", defs, err)
		}

		refs, err := References(ctx, ws, mainFile, pos)
		if err != nil || len(refs) != 2 {
			t.Fatalf("References = %+v, %v", refs, err)
		}

		impls, err := Implementation(ctx, ws, mainFile, pos)
		if err != nil || len(impls) != 1 {
			t.Fatalf("Implementation = %+v, %v", impls, err)
		}

		hl, err := DocumentHighlights(ctx, ws, mainFile, pos)
		if err != nil || len(hl) != 1 || hl[0].Kind != 2 || hl[0].Range.Start.Line != 2 {
			t.Fatalf("DocumentHighlights = %+v, %v", hl, err)
		}

		syms, err := DocumentSymbols(ctx, ws, mainFile)
		if err != nil || len(sysHaveName(syms, "Foo")) == 0 {
			t.Fatalf("DocumentSymbols = %+v, %v", syms, err)
		}

		items, err := PrepareCallHierarchy(ctx, ws, mainFile, pos)
		if err != nil || len(items) != 1 || items[0].Name != "main" || items[0].Path != mainFile {
			t.Fatalf("PrepareCallHierarchy = %+v, %v", items, err)
		}
		inc, err := IncomingCalls(ctx, ws, items[0])
		if err != nil || len(inc) != 1 || inc[0].From.Name != "caller" || len(inc[0].FromRanges) != 1 {
			t.Fatalf("IncomingCalls = %+v, %v", inc, err)
		}
		out, err := OutgoingCalls(ctx, ws, items[0])
		if err != nil || len(out) != 1 || out[0].To.Name != "callee" || len(out[0].FromRanges) != 1 {
			t.Fatalf("OutgoingCalls = %+v, %v", out, err)
		}

		diags, err := Diagnostics(ctx, ws, mainFile)
		if err != nil || len(diags) != 1 || diags[0].Message != "undefined: x" || diags[0].Severity != 1 {
			t.Fatalf("Diagnostics = %+v, %v", diags, err)
		}
	})
}

func sysHaveName(syms []Symbol, name string) []Symbol {
	var out []Symbol
	for _, s := range syms {
		if s.Name == name {
			out = append(out, s)
		}
	}
	return out
}

// ── parse helper unit tests ──────────────────────────────────────────────

func TestParseHoverVariants(t *testing.T) {
	if got := parseHover(json.RawMessage(`{"contents":{"kind":"markdown","value":"x\ny"}}`)); got != "x\ny" {
		t.Errorf("markdown hover = %q", got)
	}
	if got := parseHover(json.RawMessage(`{"contents":"plain text"}`)); got != "plain text" {
		t.Errorf("plaintext hover = %q", got)
	}
	if got := parseHover(json.RawMessage(`null`)); got != "" {
		t.Errorf("null hover = %q", got)
	}
	if got := parseHover(json.RawMessage(`bad json`)); got != "" {
		t.Errorf("bad json hover = %q", got)
	}
}

func TestParseLocationsVariants(t *testing.T) {
	two := parseLocations(json.RawMessage(`[{"uri":"file:///a.go","range":{"start":{"line":0,"character":1},"end":{"line":0,"character":2}}},{"uri":"file:///b.go","range":{"start":{"line":3,"character":0},"end":{"line":4,"character":0}}}]`))
	if len(two) != 2 || two[1].Range.Start.Line != 4 {
		t.Fatalf("array = %+v", two)
	}
	one := parseLocations(json.RawMessage(`{"uri":"file:///c.go","range":{"start":{"line":7,"character":0},"end":{"line":7,"character":1}}}`))
	if len(one) != 1 || one[0].Range.Start.Line != 8 {
		t.Fatalf("single object = %+v", one)
	}
	if got := parseLocations(json.RawMessage(`null`)); len(got) != 0 {
		t.Fatalf("null = %+v", got)
	}
	if got := parseLocations(json.RawMessage(`{bad`)); len(got) != 0 {
		t.Fatalf("bad = %+v", got)
	}
}

func TestParseSymbolsShapes(t *testing.T) {
	// DocumentSymbol tree (children flattened by parseSymbols).
	tree := parseSymbols(json.RawMessage(`[{"name":"Outer","kind":12,"range":{"start":{"line":0,"character":0},"end":{"line":9,"character":0}},"children":[{"name":"inner","kind":8,"range":{"start":{"line":1,"character":0},"end":{"line":2,"character":0}}}]}]`))
	if len(tree) != 2 || tree[0].Name != "Outer" || tree[1].Name != "inner" {
		t.Fatalf("documentSymbol tree = %+v", tree)
	}
	// SymbolInformation flat shape.
	flat := parseSymbols(json.RawMessage(`[{"name":"Sym","kind":6,"location":{"uri":"file:///x.go","range":{"start":{"line":2,"character":0},"end":{"line":2,"character":3}}}}]`))
	if len(flat) != 1 || flat[0].Name != "Sym" || flat[0].Range.Start.Line != 3 {
		t.Fatalf("symbolInformation = %+v", flat)
	}
	if got := parseSymbols(json.RawMessage(`null`)); len(got) != 0 {
		t.Fatalf("null = %+v", got)
	}
}

func TestParseDocumentHighlightsVariants(t *testing.T) {
	hl := parseDocumentHighlights(json.RawMessage(`[{"range":{"start":{"line":4,"character":0},"end":{"line":4,"character":3}},"kind":3}]`))
	if len(hl) != 1 || hl[0].Kind != 3 || hl[0].Range.Start.Line != 5 {
		t.Fatalf("highlights = %+v", hl)
	}
	if got := parseDocumentHighlights(json.RawMessage(`null`)); len(got) != 0 {
		t.Fatalf("null = %+v", got)
	}
}

func TestParseCallHierarchyHelpers(t *testing.T) {
	items := parseCallHierarchyItems(json.RawMessage(`[{"name":"fn","kind":12,"detail":"func()","uri":"file:///m.go","range":{"start":{"line":0,"character":0},"end":{"line":1,"character":0}},"selectionRange":{"start":{"line":0,"character":4},"end":{"line":0,"character":6}}}]`))
	if len(items) != 1 || items[0].Name != "fn" || items[0].rawURI != "file:///m.go" {
		t.Fatalf("items = %+v", items)
	}
	if got := parseCallHierarchyItems(json.RawMessage(`null`)); len(got) != 0 {
		t.Fatalf("null items = %+v", got)
	}
	inc := parseCallHierarchyCalls(json.RawMessage(`[{"from":{"name":"a","kind":12,"uri":"file:///m.go","range":{"start":{"line":0,"character":0},"end":{"line":0,"character":1}},"selectionRange":{"start":{"line":0,"character":0},"end":{"line":0,"character":1}}},"fromRanges":[{"start":{"line":2,"character":0},"end":{"line":2,"character":1}}]}]`), true)
	if len(inc) != 1 || inc[0].From.Name != "a" || len(inc[0].FromRanges) != 1 || inc[0].FromRanges[0].Start.Line != 3 {
		t.Fatalf("incoming = %+v", inc)
	}
	out := parseCallHierarchyCalls(json.RawMessage(`[{"to":{"name":"b","kind":12,"uri":"file:///m.go","range":{"start":{"line":0,"character":0},"end":{"line":0,"character":1}},"selectionRange":{"start":{"line":0,"character":0},"end":{"line":0,"character":1}}}}]`), false)
	if len(out) != 1 || out[0].To.Name != "b" {
		t.Fatalf("outgoing = %+v", out)
	}
	if got := parseCallHierarchyCalls(json.RawMessage(`{bad`), true); len(got) != 0 {
		t.Fatalf("bad calls = %+v", got)
	}
	rngs := parseFromRanges(json.RawMessage(`[{"start":{"line":8,"character":1},"end":{"line":8,"character":2}}]`))
	if len(rngs) != 1 || rngs[0].Start.Line != 9 || rngs[0].Start.Character != 2 {
		t.Fatalf("fromRanges = %+v", rngs)
	}
	if got := parseFromRanges(json.RawMessage(`null`)); len(got) != 0 {
		t.Fatalf("null fromRanges = %+v", got)
	}
}

func TestParseDocumentDiagnosticsVariants(t *testing.T) {
	ds := parseDocumentDiagnostics(json.RawMessage(`{"kind":"full","items":[{"range":{"start":{"line":1,"character":0},"end":{"line":1,"character":2}},"severity":2,"message":"unused var","source":"fake"}]}`))
	if len(ds) != 1 || ds[0].Severity != 2 || ds[0].Source != "fake" {
		t.Fatalf("diags = %+v", ds)
	}
	if got := parseDocumentDiagnostics(json.RawMessage(`{"kind":"full","items":[]}`)); len(got) != 0 {
		t.Fatalf("empty items = %+v", got)
	}
	if got := parseDocumentDiagnostics(json.RawMessage(`null`)); len(got) != 0 {
		t.Fatalf("null = %+v", got)
	}
}

// ── retry helper unit tests ──────────────────────────────────────────────

func TestRetryEmptyStringResultSemantics(t *testing.T) {
	// Non-csharp server: no retry even on empty result.
	plain := &sessionClient{resolved: ResolvedServer{Binary: "gopls"}}
	calls := 0
	got, err := retryEmptyStringResult(context.Background(), plain, func() (string, error) {
		calls++
		return "", nil
	})
	if err != nil || calls != 1 || got != "" {
		t.Fatalf("no-retry path: got=%q calls=%d err=%v", got, calls, err)
	}

	// csharp-ls: empty first attempt retries until non-empty.
	csharp := &sessionClient{resolved: ResolvedServer{Binary: "/tools/csharp-ls"}}
	calls = 0
	got, err = retryEmptyStringResult(context.Background(), csharp, func() (string, error) {
		calls++
		if calls == 1 {
			return "", nil
		}
		return "warmed", nil
	})
	if err != nil || calls != 2 || got != "warmed" {
		t.Fatalf("csharp retry path: got=%q calls=%d err=%v", got, calls, err)
	}

	// Cancelled context surfaces the ctx error (#1586-D).
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	calls = 0
	_, err = retryEmptyStringResult(ctx, csharp, func() (string, error) { calls++; return "", nil })
	if err == nil {
		t.Fatal("cancelled ctx must surface an error, not a clean empty string")
	}
}

func TestRetryEmptySliceResultSemantics(t *testing.T) {
	plain := &sessionClient{resolved: ResolvedServer{Binary: "gopls"}}
	calls := 0
	got, err := retryEmptySliceResult(context.Background(), plain, func() ([]Location, error) {
		calls++
		return nil, nil
	})
	if err != nil || calls != 1 || len(got) != 0 {
		t.Fatalf("no-retry path: calls=%d err=%v", calls, err)
	}

	csharp := &sessionClient{resolved: ResolvedServer{Binary: "csharp-ls"}}
	calls = 0
	got, err = retryEmptySliceResult(context.Background(), csharp, func() ([]Location, error) {
		calls++
		if calls == 1 {
			return nil, nil
		}
		return []Location{{Path: "/a.go"}}, nil
	})
	if err != nil || calls != 2 || len(got) != 1 {
		t.Fatalf("csharp retry path: calls=%d got=%v err=%v", calls, got, err)
	}
}

func TestRetryCallHierarchyItemsSemantics(t *testing.T) {
	plain := &sessionClient{resolved: ResolvedServer{Binary: "gopls"}}
	calls := 0
	items, err := retryCallHierarchyItems(context.Background(), plain, func() ([]CallHierarchyItem, error) {
		calls++
		return nil, nil
	})
	if err != nil || calls != 1 || len(items) != 0 {
		t.Fatalf("no-retry path: calls=%d err=%v", calls, err)
	}

	csharp := &sessionClient{resolved: ResolvedServer{Binary: "csharp-ls"}}
	calls = 0
	items, err = retryCallHierarchyItems(context.Background(), csharp, func() ([]CallHierarchyItem, error) {
		calls++
		if calls == 1 {
			return nil, nil
		}
		return []CallHierarchyItem{{Name: "fn"}}, nil
	})
	if err != nil || calls != 2 || len(items) != 1 || items[0].Name != "fn" {
		t.Fatalf("csharp retry path: calls=%d err=%v", calls, err)
	}
}

func TestSleepWithContext(t *testing.T) {
	if err := sleepWithContext(context.Background(), time.Millisecond); err != nil {
		t.Fatalf("normal sleep: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := sleepWithContext(ctx, time.Minute); err == nil {
		t.Fatal("cancelled ctx must abort the sleep")
	}
}

// ── lockedBuffer tail-cap ────────────────────────────────────────────────

func TestLockedBufferKeepsTail(t *testing.T) {
	var lb lockedBuffer
	chunk := strings.Repeat("x", 32<<10)
	if _, err := lb.Write([]byte(chunk)); err != nil {
		t.Fatalf("write1: %v", err)
	}
	if _, err := lb.Write([]byte("TAIL-MARKER")); err != nil {
		t.Fatalf("write2: %v", err)
	}
	got := lb.String()
	if len(got) > stderrMaxBytes {
		t.Fatalf("buffer exceeded cap: %d", len(got))
	}
	if !strings.HasSuffix(got, "TAIL-MARKER") {
		t.Fatal("tail must be preserved when the cap kicks in")
	}
}
