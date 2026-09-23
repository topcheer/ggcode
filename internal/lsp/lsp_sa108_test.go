package lsp

// sa-108 coverage net: table-driven tests for the function-level 0% set in
// internal/lsp — pure parse/convert helpers, retry warmup family (with a
// hand-built sessionClient, no real server), session state accessors, and the
// unsupported-workspace error paths of the top-level operations.
//
// Deliberately NOT covered here: ShutdownAll (package-level sync.Once drains
// globalSessions for the whole test binary; covered by zz_shutdownall_test.go
// in the integration tag domain) and handleServerRequest (needs a live
// stdioClient writer).

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// --- pure position/range converters ---

func TestToLSPPosition_sa108(t *testing.T) {
	tests := []struct {
		pos  Position
		want map[string]int
	}{
		{Position{Line: 1, Character: 1}, map[string]int{"line": 0, "character": 0}},
		{Position{Line: 5, Character: 10}, map[string]int{"line": 4, "character": 9}},
		// 1-based API coordinates clamp at zero, never go negative.
		{Position{Line: 0, Character: 0}, map[string]int{"line": 0, "character": 0}},
	}
	for _, tt := range tests {
		got := toLSPPosition(tt.pos)
		if got["line"] != tt.want["line"] || got["character"] != tt.want["character"] {
			t.Errorf("toLSPPosition(%+v) = %v, want %v", tt.pos, got, tt.want)
		}
	}
}

func TestToLSPRange_sa108(t *testing.T) {
	rng := Range{
		Start: Position{Line: 2, Character: 3},
		End:   Position{Line: 4, Character: 5},
	}
	got := toLSPRange(rng)
	start, _ := got["start"].(map[string]int)
	end, _ := got["end"].(map[string]int)
	if start["line"] != 1 || start["character"] != 2 || end["line"] != 3 || end["character"] != 4 {
		t.Errorf("toLSPRange(%+v) = %v", rng, got)
	}
}

func TestMustRaw_sa108(t *testing.T) {
	raw := mustRaw(map[string]int{"a": 1})
	var m map[string]int
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("mustRaw produced invalid JSON: %v", err)
	}
	if m["a"] != 1 {
		t.Errorf("mustRaw roundtrip lost data: %v", m)
	}
}

// --- lockedBuffer.Write (client.go stderr tail cap) ---

func TestLockedBuffer_Write_sa108(t *testing.T) {
	var lb lockedBuffer
	small := []byte("hello")
	if n, err := lb.Write(small); n != len(small) || err != nil {
		t.Fatalf("Write(small) = %d, %v", n, err)
	}
	if lb.String() != "hello" {
		t.Errorf("buffer = %q, want %q", lb.String(), "hello")
	}

	// Overflow trims from the FRONT (oldest): writing "hello" (5) plus a
	// chunk that exceeds the cap by 3 drops the first 3 bytes, keeps the tail.
	chunk := strings.Repeat("x", stderrMaxBytes-2)
	if _, err := lb.Write([]byte(chunk)); err != nil {
		t.Fatal(err)
	}
	if got := len(lb.String()); got != stderrMaxBytes {
		t.Errorf("after overflow buffer len = %d, want %d", got, stderrMaxBytes)
	}
	if s := lb.String(); !strings.HasPrefix(s, "lo") || !strings.HasSuffix(s, chunk) {
		t.Error("overflow must drop the 3 oldest bytes and keep the newest tail")
	}

	// Single write larger than the whole cap (overflow > current len):
	// buffer resets, only the last cap-sized tail of p is kept.
	var lb2 lockedBuffer
	lb2.Write([]byte("seed"))
	big := strings.Repeat("y", stderrMaxBytes+1024)
	if _, err := lb2.Write([]byte(big)); err != nil {
		t.Fatal(err)
	}
	s := lb2.String()
	if len(s) != stderrMaxBytes {
		t.Errorf("after big write len = %d, want %d", len(s), stderrMaxBytes)
	}
	if strings.Contains(s, "x") || strings.Contains(s, "seed") {
		t.Error("pre-overflow content must be dropped entirely on a >cap write")
	}
}

// --- hover parsing ---

func TestParseHover_sa108(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{"string contents", `{"contents":"plain text"}`, "plain text"},
		{"marked-string object", `{"contents":{"value":"markdown hover"}}`, "markdown hover"},
		{"array contents joined", `{"contents":[{"value":"a"},{"value":"b"},"c"]}`, "a\n\nb\n\nc"},
		{"empty array", `{"contents":[]}`, ""},
		{"invalid json", `{invalid`, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := parseHover(json.RawMessage(tt.raw)); got != tt.want {
				t.Errorf("parseHover(%s) = %q, want %q", tt.raw, got, tt.want)
			}
		})
	}
}

func TestStringifyHoverContents_sa108(t *testing.T) {
	if got := stringifyHoverContents(42); got != "" {
		t.Errorf("unsupported type should stringify to empty, got %q", got)
	}
	if got := stringifyHoverContents(map[string]any{"value": 3}); got != "" {
		t.Errorf("map with non-string value should be empty, got %q", got)
	}
	if got := stringifyHoverContents([]any{" a ", "", "b"}); got != "a\n\nb" {
		t.Errorf("slice with blanks = %q, want %q", got, "a\n\nb")
	}
}

// --- location / diagnostics / call-hierarchy parsing ---

func TestParseLocations_sa108(t *testing.T) {
	listForm := `[{"uri":"file:///a.go","range":{"start":{"line":0,"character":2},"end":{"line":1,"character":4}}}]`
	locs := parseLocations(json.RawMessage(listForm))
	if len(locs) != 1 {
		t.Fatalf("list form: got %d locations, want 1", len(locs))
	}
	if locs[0].Path == "" || !strings.HasSuffix(locs[0].Path, "a.go") {
		t.Errorf("path = %q, want suffix a.go", locs[0].Path)
	}
	if locs[0].Range.Start.Line != 1 || locs[0].Range.Start.Character != 3 || locs[0].Range.End.Line != 2 {
		t.Errorf("range not converted to 1-based: %+v", locs[0].Range)
	}

	singleForm := `{"uri":"file:///b.go","range":{"start":{"line":0,"character":0},"end":{"line":0,"character":1}}}`
	locs = parseLocations(json.RawMessage(singleForm))
	if len(locs) != 1 || !strings.HasSuffix(locs[0].Path, "b.go") {
		t.Errorf("single-object form: got %+v", locs)
	}

	if got := parseLocations(json.RawMessage(`{"nope":1}`)); got != nil {
		t.Errorf("unrelated object should parse to nil, got %+v", got)
	}
	if got := parseLocations(json.RawMessage(`[]`)); got != nil {
		t.Errorf("empty array should parse to nil, got %+v", got)
	}
}

func TestParseDocumentDiagnostics_sa108(t *testing.T) {
	raw := `{"items":[{"severity":1,"message":"undef: x","source":"compile","range":{"start":{"line":0,"character":1},"end":{"line":0,"character":2}}}]}`
	diags := parseDocumentDiagnostics(json.RawMessage(raw))
	if len(diags) != 1 {
		t.Fatalf("got %d diagnostics, want 1", len(diags))
	}
	d := diags[0]
	if d.Severity != 1 || d.Message != "undef: x" || d.Source != "compile" {
		t.Errorf("fields not carried: %+v", d)
	}
	if d.Range.Start.Line != 1 || d.Range.Start.Character != 2 {
		t.Errorf("range not 1-based: %+v", d.Range)
	}
	// Empty pull result (#1274): valid clean-file answer, not an error.
	if got := parseDocumentDiagnostics(json.RawMessage(`{"items":[]}`)); len(got) != 0 {
		t.Errorf("empty items should give empty slice, got %+v", got)
	}
	if got := parseDocumentDiagnostics(json.RawMessage(`nope`)); got != nil {
		t.Errorf("invalid JSON should give nil, got %+v", got)
	}
}

func TestParseCallHierarchyItems_sa108(t *testing.T) {
	raw := `[{"name":"Handler","kind":6,"detail":"func","uri":"file:///s.go","range":{"start":{"line":9,"character":0},"end":{"line":19,"character":1}}}]`
	items := parseCallHierarchyItems(json.RawMessage(raw))
	if len(items) != 1 {
		t.Fatalf("got %d items, want 1", len(items))
	}
	it := items[0]
	if it.Name != "Handler" || it.Kind != 6 || it.Detail != "func" {
		t.Errorf("fields not carried: %+v", it)
	}
	if it.Range.Start.Line != 10 || it.Range.End.Line != 20 {
		t.Errorf("range not 1-based: %+v", it.Range)
	}
	if it.rawURI != "file:///s.go" {
		t.Errorf("rawURI = %q", it.rawURI)
	}
	if got := parseCallHierarchyItems(json.RawMessage(`nope`)); got != nil {
		t.Errorf("invalid JSON should give nil, got %+v", got)
	}
}

func TestParseCallHierarchyCalls_sa108(t *testing.T) {
	raw := `[{"from":{"name":"A","uri":"file:///a.go","range":{"start":{"line":0,"character":0},"end":{"line":0,"character":5}}},"fromRanges":[{"start":{"line":1,"character":0},"end":{"line":1,"character":9}}]}]`
	in := parseCallHierarchyCalls(json.RawMessage(raw), true)
	if len(in) != 1 || in[0].From.Name != "A" {
		t.Fatalf("incoming calls not parsed: %+v", in)
	}
	if len(in[0].FromRanges) != 1 || in[0].FromRanges[0].Start.Line != 2 {
		t.Errorf("fromRanges not parsed/1-based: %+v", in[0].FromRanges)
	}

	// Outgoing direction reads the "to" item instead.
	rawOut := `[{"to":{"name":"B","uri":"file:///b.go","range":{"start":{"line":3,"character":0},"end":{"line":3,"character":9}}}}]`
	out := parseCallHierarchyCalls(json.RawMessage(rawOut), false)
	if len(out) != 1 || out[0].To.Name != "B" {
		t.Fatalf("outgoing calls not parsed: %+v", out)
	}

	// Entries without the direction key are skipped; invalid inner items too.
	if got := parseCallHierarchyCalls(json.RawMessage(`[{"fromRanges":[]}]`), true); len(got) != 0 {
		t.Errorf("missing key should skip entry, got %+v", got)
	}
	if got := parseCallHierarchyCalls(json.RawMessage(`[{"from":"not-an-object"}]`), true); len(got) != 0 {
		t.Errorf("invalid item should skip entry, got %+v", got)
	}
	if got := parseCallHierarchyCalls(json.RawMessage(`nope`), true); got != nil {
		t.Errorf("invalid JSON should give nil, got %+v", got)
	}
}

func TestParseFromRanges_sa108(t *testing.T) {
	if got := parseFromRanges(nil); got != nil {
		t.Errorf("nil raw should give nil, got %+v", got)
	}
	if got := parseFromRanges(json.RawMessage(`nope`)); got != nil {
		t.Errorf("invalid JSON should give nil, got %+v", got)
	}
	got := parseFromRanges(json.RawMessage(`[{"start":{"line":0,"character":0},"end":{"line":0,"character":3}}]`))
	if len(got) != 1 || got[0].Start.Character != 1 || got[0].End.Character != 4 {
		t.Errorf("ranges not 1-based: %+v", got)
	}
}

// --- sleepWithContext ---

func TestSleepWithContext_sa108(t *testing.T) {
	if err := sleepWithContext(context.Background(), time.Millisecond); err != nil {
		t.Errorf("happy path err = %v, want nil", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := sleepWithContext(ctx, time.Hour); err != context.Canceled {
		t.Errorf("cancelled ctx err = %v, want context.Canceled", err)
	}
}

// --- retry warmup family (sessionClient hand-built; no server) ---

func newTestSession_sa108(binary string) *sessionClient {
	return &sessionClient{
		workspace:   "/tmp/ws",
		resolved:    ResolvedServer{LanguageID: "csharp", DisplayName: binary, Binary: binary},
		readySignal: make(chan struct{}),
		docs:        make(map[string]documentState),
		diagnostics: make(map[string]diagnosticsState),
	}
}

func TestRetryEmptyStringResult_sa108(t *testing.T) {
	ctx := context.Background()
	csharp := newTestSession_sa108("/usr/local/bin/csharp-ls")
	gopls := newTestSession_sa108("/usr/local/bin/gopls")

	// Error from the first call is returned as-is (no retry).
	_, err := retryEmptyStringResult(ctx, csharp, func() (string, error) { return "", context.DeadlineExceeded })
	if err != context.DeadlineExceeded {
		t.Errorf("first-call error must pass through, got %v", err)
	}
	// Non-empty first result short-circuits.
	got, err := retryEmptyStringResult(ctx, csharp, func() (string, error) { return " hover ", nil })
	if err != nil || got != " hover " {
		t.Errorf("non-empty short-circuit = %q, %v", got, err)
	}
	// Non-csharp sessions never retry: empty stays empty immediately.
	got, err = retryEmptyStringResult(ctx, gopls, func() (string, error) { return "", nil })
	if err != nil || got != "" {
		t.Errorf("gopls empty = %q, %v, want empty passthrough", got, err)
	}
	// Cancelled context surfaces the ctx error (#1586-D).
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	_, err = retryEmptyStringResult(cctx, csharp, func() (string, error) { return "", nil })
	if err != context.Canceled {
		t.Errorf("cancelled warmup must surface ctx err, got %v", err)
	}
	// Second attempt yields content.
	n := 0
	got, err = retryEmptyStringResult(ctx, csharp, func() (string, error) {
		n++
		if n == 1 {
			return "", nil
		}
		return "warmed", nil
	})
	if err != nil || got != "warmed" {
		t.Errorf("retry-then-success = %q, %v, want warmed", got, err)
	}
	// Error on a later attempt aborts with that error.
	_, err = retryEmptyStringResult(ctx, csharp, func() (string, error) {
		if n >= 5 {
			return "", context.DeadlineExceeded
		}
		n++
		return "", nil
	})
	if err != context.DeadlineExceeded {
		t.Errorf("mid-retry error must abort, got %v", err)
	}
}

func TestRetryEmptySliceResult_sa108(t *testing.T) {
	ctx := context.Background()
	csharp := newTestSession_sa108("/usr/local/bin/csharp-ls")
	gopls := newTestSession_sa108("/usr/local/bin/gopls")

	_, err := retryEmptySliceResult[int](ctx, csharp, func() ([]int, error) { return nil, context.DeadlineExceeded })
	if err != context.DeadlineExceeded {
		t.Errorf("first-call error passthrough: %v", err)
	}
	got, err := retryEmptySliceResult[int](ctx, csharp, func() ([]int, error) { return []int{7}, nil })
	if err != nil || len(got) != 1 {
		t.Errorf("non-empty short-circuit = %v, %v", got, err)
	}
	got, err = retryEmptySliceResult[int](ctx, gopls, func() ([]int, error) { return nil, nil })
	if err != nil || got != nil {
		t.Errorf("gopls empty passthrough = %v, %v", got, err)
	}
	// Exhaustion: all attempts empty -> original (empty) result, nil error.
	start := time.Now()
	got, err = retryEmptySliceResult[int](ctx, csharp, func() ([]int, error) { return nil, nil })
	if err != nil || got != nil {
		t.Errorf("exhaustion = %v, %v, want nil,nil", got, err)
	}
	if elapsed := time.Since(start); elapsed < csharpWarmupRetryDelay {
		t.Errorf("exhaustion should wait at least one delay, elapsed %v", elapsed)
	}
	// Cancelled mid-warmup.
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	_, err = retryEmptySliceResult[int](cctx, csharp, func() ([]int, error) { return nil, nil })
	if err != context.Canceled {
		t.Errorf("cancelled = %v, want context.Canceled", err)
	}
}

func TestRetryCallHierarchyItems_sa108(t *testing.T) {
	ctx := context.Background()
	csharp := newTestSession_sa108("/usr/local/bin/csharp-ls")

	// Error passthrough.
	_, err := retryCallHierarchyItems(ctx, csharp, func() ([]CallHierarchyItem, error) {
		return nil, context.DeadlineExceeded
	})
	if err != context.DeadlineExceeded {
		t.Errorf("error passthrough: %v", err)
	}
	// Non-empty short-circuit.
	items, err := retryCallHierarchyItems(ctx, csharp, func() ([]CallHierarchyItem, error) {
		return []CallHierarchyItem{{Name: "F"}}, nil
	})
	if err != nil || len(items) != 1 {
		t.Errorf("non-empty short-circuit = %+v, %v", items, err)
	}
	// Retry-then-success (single 400ms delay).
	n := 0
	items, err = retryCallHierarchyItems(ctx, csharp, func() ([]CallHierarchyItem, error) {
		n++
		if n == 1 {
			return nil, nil
		}
		return []CallHierarchyItem{{Name: "G"}}, nil
	})
	if err != nil || len(items) != 1 || items[0].Name != "G" {
		t.Errorf("retry-then-success = %+v, %v", items, err)
	}
	// Cancelled.
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	_, err = retryCallHierarchyItems(cctx, csharp, func() ([]CallHierarchyItem, error) { return nil, nil })
	if err != context.Canceled {
		t.Errorf("cancelled = %v, want context.Canceled", err)
	}
}

// --- session state accessors ---

func TestSessionTouchAndLastTouch_sa108(t *testing.T) {
	s := newTestSession_sa108("gopls")
	past := time.Now().Add(-time.Hour)
	s.lastUsed = past
	if !s.lastTouch().Equal(past) {
		t.Errorf("lastTouch = %v, want %v", s.lastTouch(), past)
	}
	s.touch()
	if !s.lastTouch().After(past) {
		t.Error("touch must advance lastUsed")
	}
}

func TestSessionProjectReady_sa108(t *testing.T) {
	s := newTestSession_sa108("csharp-ls")
	if s.projectReady() {
		t.Error("fresh session must not be project-ready")
	}
	s.markProjectReady()
	if !s.projectReady() {
		t.Error("session must be ready after markProjectReady")
	}
	s.markProjectReady() // idempotent: must not panic on double close
}

func TestSessionPullDiagnosticsFlags_sa108(t *testing.T) {
	s := newTestSession_sa108("gopls")
	if !s.supportsPullDiagnostics() {
		t.Error("unknown support must default to optimistic true")
	}
	s.setPullDiagnosticsSupport(false)
	if s.supportsPullDiagnostics() {
		t.Error("after unsupported-method error, pull must be disabled")
	}
	s.setPullDiagnosticsSupport(true)
	if !s.supportsPullDiagnostics() {
		t.Error("explicit re-enable must stick")
	}
}

func TestSessionPublishedDiagnostics_sa108(t *testing.T) {
	s := newTestSession_sa108("gopls")
	if _, seen := s.publishedDiagnostics("file:///missing"); seen {
		t.Error("unknown URI must not be seen")
	}
	// Clean file: seen with empty set (#1274 semantics).
	s.setPublishedDiagnostics("file:///clean.go", nil)
	if _, seen := s.publishedDiagnostics("file:///clean.go"); !seen {
		t.Error("empty publish must still count as seen")
	}
	diags := []Diagnostic{{Severity: 2, Message: "m"}}
	s.setPublishedDiagnostics("file:///x.go", diags)
	got, seen := s.publishedDiagnostics("file:///x.go")
	if !seen || len(got) != 1 || got[0].Message != "m" {
		t.Errorf("published = %+v seen=%v", got, seen)
	}
	// Returned slice is a copy: mutating it must not corrupt the store.
	got[0].Message = "mutated"
	if again, _ := s.publishedDiagnostics("file:///x.go"); again[0].Message != "m" {
		t.Error("publishedDiagnostics must return a defensive copy")
	}
}

func TestSessionPrimeProjectShortCircuits_sa108(t *testing.T) {
	// Non-csharp sessions skip priming entirely (no client touched).
	g := newTestSession_sa108("gopls")
	if err := g.primeProject(context.Background(), "file:///a.go"); err != nil {
		t.Errorf("gopls primeProject = %v, want nil", err)
	}
	// Ready csharp sessions short-circuit before touching the client.
	c := newTestSession_sa108("csharp-ls")
	c.markProjectReady()
	if err := c.primeProject(context.Background(), "file:///a.go"); err != nil {
		t.Errorf("ready csharp primeProject = %v, want nil", err)
	}
}

func TestSessionRefreshDocumentUnknown_sa108(t *testing.T) {
	s := newTestSession_sa108("gopls")
	// Refreshing a document that was never opened is a no-op, and must not
	// touch the (nil) client.
	if err := s.refreshDocument(context.Background(), "file:///never-opened.go"); err != nil {
		t.Errorf("refreshDocument(unknown) = %v, want nil", err)
	}
}

// --- operations.go parsing ---

func TestParseWorkspaceSymbols_sa108(t *testing.T) {
	// SymbolInformation form (element carries a "location" object with uri).
	siForm := `[{"name":"main","kind":12,"location":{"uri":"file:///a.go","range":{"start":{"line":0,"character":0},"end":{"line":2,"character":0}}}}]`
	syms := parseWorkspaceSymbols(json.RawMessage(siForm))
	if len(syms) != 1 || syms[0].Name != "main" || syms[0].Kind != 12 {
		t.Fatalf("SymbolInformation form: %+v", syms)
	}
	if !strings.HasSuffix(syms[0].Path, "a.go") || syms[0].Range.End.Line != 3 {
		t.Errorf("path/range conversion: %+v", syms[0])
	}

	// Flat form (top-level uri, no "location" key in the first element).
	flatForm := `[{"name":"T","kind":23,"uri":"file:///b.ts"}]`
	syms = parseWorkspaceSymbols(json.RawMessage(flatForm))
	if len(syms) != 1 || syms[0].Name != "T" || !strings.HasSuffix(syms[0].Path, "b.ts") {
		t.Fatalf("flat form: %+v", syms)
	}

	if got := parseWorkspaceSymbols(json.RawMessage(`[]`)); got != nil {
		t.Errorf("empty array = %+v, want nil", got)
	}
	if got := parseWorkspaceSymbols(json.RawMessage(`nope`)); got != nil {
		t.Errorf("invalid JSON = %+v, want nil", got)
	}
}

func TestParseCodeActions_sa108(t *testing.T) {
	raw := `[{"title":"Organize imports","kind":"source.organizeImports","command":{"command":"x.apply"},"edit":{"changes":{"file:///a.go":[{"range":{"start":{"line":0,"character":0},"end":{"line":0,"character":9}},"newText":"import \"fmt\""}]}}}]`
	actions := parseCodeActions(json.RawMessage(raw))
	if len(actions) != 1 {
		t.Fatalf("got %d actions, want 1", len(actions))
	}
	a := actions[0]
	if a.Title != "Organize imports" || a.Kind != "source.organizeImports" || a.Command != "x.apply" {
		t.Errorf("fields not carried: %+v", a)
	}
	if len(a.Edits) != 1 || !strings.HasSuffix(a.Edits[0].Path, "a.go") || a.Edits[0].Edits[0].NewText != "import \"fmt\"" {
		t.Errorf("edit not parsed: %+v", a.Edits)
	}
	if got := parseCodeActions(json.RawMessage(`nope`)); got != nil {
		t.Errorf("invalid JSON = %+v, want nil", got)
	}
}

func TestDiagnosticsToLSP_sa108(t *testing.T) {
	if got := diagnosticsToLSP(nil); len(got) != 0 {
		t.Errorf("nil input must give empty slice, got %+v", got)
	}
	out := diagnosticsToLSP([]Diagnostic{{
		Severity: 1, Message: "boom", Source: "gc",
		Range: Range{Start: Position{Line: 1, Character: 1}, End: Position{Line: 1, Character: 9}},
	}})
	if len(out) != 1 || out[0]["severity"] != 1 || out[0]["message"] != "boom" || out[0]["source"] != "gc" {
		t.Errorf("fields: %+v", out)
	}
	rng, _ := out[0]["range"].(map[string]any)
	start, _ := rng["start"].(map[string]int)
	if start["line"] != 0 || start["character"] != 0 {
		t.Errorf("range should convert back to 0-based: %+v", rng)
	}
}

// --- top-level ops: unsupported workspace/file error paths ---

func TestTopLevelOps_UnsupportedWorkspace_sa108(t *testing.T) {
	ctx := context.Background()
	ws := t.TempDir() // empty dir: no markers, no supported language
	file := filepath.Join(ws, "nope.zzz")
	p := Position{Line: 1, Character: 1}

	if _, err := WorkspaceSymbols(ctx, ws, "Anything"); err == nil {
		t.Error("WorkspaceSymbols on unsupported workspace must error")
	}
	if _, err := Diagnostics(ctx, ws, file); err == nil {
		t.Error("Diagnostics on unsupported file must error")
	}
	if _, err := Hover(ctx, ws, file, p); err == nil {
		t.Error("Hover on unsupported file must error")
	}
	if _, err := Definition(ctx, ws, file, p); err == nil {
		t.Error("Definition on unsupported file must error")
	}
	if _, err := References(ctx, ws, file, p); err == nil {
		t.Error("References on unsupported file must error")
	}
	if _, err := Implementation(ctx, ws, file, p); err == nil {
		t.Error("Implementation on unsupported file must error")
	}
	if _, err := DocumentHighlights(ctx, ws, file, p); err == nil {
		t.Error("DocumentHighlights on unsupported file must error")
	}
	if _, err := RenameEdits(ctx, ws, file, p, "new"); err == nil {
		t.Error("RenameEdits on unsupported file must error")
	}
	if _, err := CodeActions(ctx, ws, file, Range{}); err == nil {
		t.Error("CodeActions on unsupported file must error")
	}

	// Call-hierarchy family: same unsupported-workspace error path.
	item := CallHierarchyItem{Name: "F", Path: file, rawURI: "file:///nope.zzz"}
	if _, err := PrepareCallHierarchy(ctx, ws, file, p); err == nil {
		t.Error("PrepareCallHierarchy on unsupported file must error")
	}
	if _, err := IncomingCalls(ctx, ws, item); err == nil {
		t.Error("IncomingCalls on unsupported file must error")
	}
	if _, err := OutgoingCalls(ctx, ws, item); err == nil {
		t.Error("OutgoingCalls on unsupported file must error")
	}
	if _, err := DocumentSymbols(ctx, ws, file); err == nil {
		t.Error("DocumentSymbols on unsupported file must error")
	}
}

func TestSessionPrepareDocument_ReadError_sa108(t *testing.T) {
	s := newTestSession_sa108("gopls")
	// Nonexistent file: os.ReadFile fails BEFORE any client interaction, so
	// the nil client in the hand-built session is never touched.
	if _, err := s.prepareDocument(context.Background(), filepath.Join(t.TempDir(), "does-not-exist.go"), "go"); err == nil {
		t.Error("prepareDocument on nonexistent file must error")
	}
}

// --- discovery helpers ---

func TestFirstLanguageExtension_sa108(t *testing.T) {
	if got := firstLanguageExtension(serverSpec{}); got != "" {
		t.Errorf("empty extensions = %q, want empty", got)
	}
	spec := serverSpec{extensions: []string{".go", ".mod"}}
	if got := firstLanguageExtension(spec); got != ".go" {
		t.Errorf("got %q, want .go", got)
	}
}

func TestResolveGoBinaryFallback_sa108(t *testing.T) {
	t.Run("GOBIN hit", func(t *testing.T) {
		bin := t.TempDir()
		target := filepath.Join(bin, executableName("some-server"))
		if err := os.WriteFile(target, []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		t.Setenv("GOBIN", bin)
		t.Setenv("GOPATH", "")
		t.Setenv("HOME", t.TempDir())
		display, cmd, ok := resolveGoBinaryFallback("some-server")
		if !ok || display != "some-server" || cmd != target {
			t.Errorf("resolveGoBinaryFallback = %q, %q, %v", display, cmd, ok)
		}
	})
	t.Run("all candidates miss", func(t *testing.T) {
		t.Setenv("GOBIN", filepath.Join(t.TempDir(), "missing"))
		t.Setenv("GOPATH", t.TempDir())
		t.Setenv("HOME", t.TempDir())
		if _, _, ok := resolveGoBinaryFallback("absent-server"); ok {
			t.Error("no candidate exists; must report not-ok")
		}
	})
	t.Run("GOPATH first entry", func(t *testing.T) {
		gopath := t.TempDir()
		bin := filepath.Join(gopath, "bin")
		if err := os.MkdirAll(bin, 0o755); err != nil {
			t.Fatal(err)
		}
		target := filepath.Join(bin, executableName("gopath-server"))
		if err := os.WriteFile(target, []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		t.Setenv("GOBIN", "")
		t.Setenv("GOPATH", gopath)
		t.Setenv("HOME", t.TempDir())
		_, cmd, ok := resolveGoBinaryFallback("gopath-server")
		if !ok || cmd != target {
			t.Errorf("GOPATH fallback = %q, %v", cmd, ok)
		}
	})
}

func TestGetInstallOptions_sa108(t *testing.T) {
	if got := GetInstallOptions("definitely-not-a-language", t.TempDir()); got != nil {
		t.Errorf("unknown language must return nil, got %+v", got)
	}
	opts := GetInstallOptions("python", t.TempDir())
	if len(opts) == 0 {
		t.Fatal("python must have install options")
	}
	if opts[0].ID != "pyright-user" || !opts[0].Recommended {
		t.Errorf("first python option = %+v, want recommended pyright-user", opts[0])
	}
}

// --- probe cache invalidation (#1586-A) ---

func TestInvalidateProbeCache_sa108(t *testing.T) {
	probeCache.Lock()
	probeCache.m["stale\x00/ws"] = probeCacheEntry{display: "d", command: "c", ok: true, expiry: time.Now().Add(time.Hour)}
	probeCache.Unlock()

	InvalidateProbeCache()

	probeCache.Lock()
	defer probeCache.Unlock()
	if len(probeCache.m) != 0 {
		t.Errorf("cache must be empty after invalidation, has %d entries", len(probeCache.m))
	}
}

// --- platform no-op (non-Windows build of this test file) ---

func TestDetachConsole_NoOp_sa108(t *testing.T) {
	detachConsole(nil) // must not panic; no return value to assert
}
