package tool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Temporal-validity tests for the bi-temporal knowledge graph
// (Zep/Graphiti model, arXiv:2501.13956): edges carry validity intervals,
// invalidate soft-deletes while preserving history, trace supports as_of
// point-in-time views.

func kgExec(t *testing.T, tool *KnowledgeGraphTool, input string) Result {
	t.Helper()
	r, err := tool.Execute(context.Background(), json.RawMessage(input))
	if err != nil {
		t.Fatalf("Execute(%s): %v", input, err)
	}
	return r
}

func kgMustOK(t *testing.T, tool *KnowledgeGraphTool, input string) Result {
	t.Helper()
	r := kgExec(t, tool, input)
	if r.IsError {
		t.Fatalf("expected ok, got error for %s: %s", input, r.Content)
	}
	return r
}

func kgSetup(t *testing.T) *KnowledgeGraphTool {
	t.Helper()
	tool, _ := newKGTool(t)
	kgMustOK(t, tool, `{"action":"add","id":"a","type":"decision","title":"A"}`)
	kgMustOK(t, tool, `{"action":"add","id":"b","type":"entity","title":"B"}`)
	return tool
}

func TestKGLinkSetsTemporalDefaults(t *testing.T) {
	tool := kgSetup(t)
	kgMustOK(t, tool, `{"action":"link","id":"a","to":"b","type":"relates-to"}`)

	store, err := tool.load()
	if err != nil {
		t.Fatal(err)
	}
	if len(store.Edges) != 1 {
		t.Fatalf("want 1 edge, got %d", len(store.Edges))
	}
	e := store.Edges[0]
	if e.RecordedAt.IsZero() {
		t.Error("recorded_at not set on link")
	}
	if e.ValidFrom != nil {
		t.Errorf("valid_from should default to nil (recorded_at acts as start), got %v", *e.ValidFrom)
	}
	if e.ValidUntil != nil {
		t.Errorf("fresh edge must not be invalidated, got %v", *e.ValidUntil)
	}
}

func TestKGLinkBackdatedValidFrom(t *testing.T) {
	tool := kgSetup(t)
	kgMustOK(t, tool, `{"action":"link","id":"a","to":"b","type":"relates-to","valid_from":"2026-01-01T00:00:00Z"}`)

	store, err := tool.load()
	if err != nil {
		t.Fatal(err)
	}
	if store.Edges[0].ValidFrom == nil {
		t.Fatal("valid_from not persisted")
	}
	want := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if !store.Edges[0].ValidFrom.Equal(want) {
		t.Errorf("valid_from = %v, want %v", *store.Edges[0].ValidFrom, want)
	}
}

func TestKGLinkInvalidValidFrom(t *testing.T) {
	tool := kgSetup(t)
	r := kgExec(t, tool, `{"action":"link","id":"a","to":"b","type":"relates-to","valid_from":"not-a-time"}`)
	if !r.IsError {
		t.Fatalf("want error for bad valid_from, got: %s", r.Content)
	}
	if !strings.Contains(r.Content, "invalid valid_from") {
		t.Errorf("error should mention invalid valid_from: %s", r.Content)
	}
}

func TestKGDedupValidVsInvalidated(t *testing.T) {
	tool := kgSetup(t)
	link := `{"action":"link","id":"a","to":"b","type":"relates-to"}`

	// First link OK.
	kgMustOK(t, tool, link)
	// Duplicate while currently valid -> "already exists", no new edge.
	r := kgExec(t, tool, link)
	if r.IsError || !strings.Contains(r.Content, "already exists") {
		t.Fatalf("duplicate should report already exists: %s", r.Content)
	}
	// Invalidate, then re-link -> re-assertion succeeds with a fresh interval.
	r = kgMustOK(t, tool, `{"action":"invalidate","id":"a","to":"b"}`)
	if !strings.Contains(r.Content, "Invalidated 1 edge") {
		t.Errorf("invalidate should report 1 edge: %s", r.Content)
	}
	r = kgMustOK(t, tool, link)
	if !strings.Contains(r.Content, "Re-validated") {
		t.Errorf("re-link after invalidate should re-assert: %s", r.Content)
	}
	store, err := tool.load()
	if err != nil {
		t.Fatal(err)
	}
	if len(store.Edges) != 2 {
		t.Fatalf("want 2 intervals, got %d", len(store.Edges))
	}
	// Duplicate again -> back to "already exists".
	r = kgExec(t, tool, link)
	if !strings.Contains(r.Content, "already exists") {
		t.Errorf("valid duplicate should be rejected: %s", r.Content)
	}
}

func TestKGInvalidateNoMatch(t *testing.T) {
	tool := kgSetup(t)
	r := kgExec(t, tool, `{"action":"invalidate","id":"a","to":"b"}`)
	if r.IsError {
		t.Fatal(r.Content)
	}
	if !strings.Contains(r.Content, "No currently-valid edge") {
		t.Errorf("want no-match message: %s", r.Content)
	}
	// Missing params.
	r = kgExec(t, tool, `{"action":"invalidate","id":"a"}`)
	if !r.IsError || !strings.Contains(r.Content, "both id") {
		t.Errorf("want param error: %s", r.Content)
	}
	// Invalid edge type.
	r = kgExec(t, tool, `{"action":"invalidate","id":"a","to":"b","type":"bogus"}`)
	if !r.IsError || !strings.Contains(r.Content, "invalid edge type") {
		t.Errorf("want edge type error: %s", r.Content)
	}
}

func TestKGTraceFiltersInvalidatedAndAsOf(t *testing.T) {
	tool := kgSetup(t)
	kgMustOK(t, tool, `{"action":"link","id":"a","to":"b","type":"relates-to"}`)
	// Backdate invalidation so as_of is deterministic.
	store, err := tool.load()
	if err != nil {
		t.Fatal(err)
	}
	past := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	store.Edges[0].ValidUntil = &past
	if err := tool.save(store); err != nil {
		t.Fatal(err)
	}

	// Default (now): invalidated edge hidden.
	r := kgMustOK(t, tool, `{"action":"trace","id":"a"}`)
	if strings.Contains(r.Content, "--[relates-to]-->") {
		t.Errorf("invalidated edge should be hidden by default: %s", r.Content)
	}
	if !strings.Contains(r.Content, "edge(s) hidden") {
		t.Errorf("should hint at hidden history: %s", r.Content)
	}

	// as_of before invalidation: historical view includes it.
	r = kgMustOK(t, tool, `{"action":"trace","id":"a","as_of":"2025-01-01T00:00:00Z"}`)
	if !strings.Contains(r.Content, "--[relates-to]-->") {
		t.Errorf("as_of historical view should include edge: %s", r.Content)
	}
	// as_of after invalidation (and before future start): excluded again.
	r = kgMustOK(t, tool, `{"action":"trace","id":"a","as_of":"2027-01-01T00:00:00Z"}`)
	if strings.Contains(r.Content, "--[relates-to]-->") {
		t.Errorf("as_of after invalidation should exclude edge: %s", r.Content)
	}

	// Future valid_from: excluded from current view.
	tool2 := kgSetup(t)
	kgMustOK(t, tool2, `{"action":"link","id":"a","to":"b","type":"relates-to","valid_from":"2030-01-01T00:00:00Z"}`)
	r = kgMustOK(t, tool2, `{"action":"trace","id":"a"}`)
	if strings.Contains(r.Content, "--[relates-to]-->") {
		t.Errorf("future valid_from edge should be hidden now: %s", r.Content)
	}
}

func TestKGTraceInvalidAsOf(t *testing.T) {
	tool := kgSetup(t)
	r := kgExec(t, tool, `{"action":"trace","id":"a","as_of":"yesterday"}`)
	if !r.IsError || !strings.Contains(r.Content, "invalid as_of") {
		t.Errorf("want invalid as_of error: %s", r.Content)
	}
}

func TestKGStatsValidityBreakdown(t *testing.T) {
	tool := kgSetup(t)
	kgMustOK(t, tool, `{"action":"link","id":"a","to":"b","type":"relates-to"}`)
	kgMustOK(t, tool, `{"action":"invalidate","id":"a","to":"b"}`)
	r := kgMustOK(t, tool, `{"action":"stats"}`)
	if !strings.Contains(r.Content, "0 current, 1 invalidated") {
		t.Errorf("stats should break down validity: %s", r.Content)
	}
}

func TestKGLegacyFileLoadsAsAlwaysValid(t *testing.T) {
	tool, dir := newKGTool(t)
	legacy := `{"nodes":{"a":{"id":"a","type":"decision","title":"A","created_at":"2024-01-01T00:00:00Z","updated_at":"2024-01-01T00:00:00Z"},"b":{"id":"b","type":"entity","title":"B","created_at":"2024-01-01T00:00:00Z","updated_at":"2024-01-01T00:00:00Z"}},"edges":[{"from":"a","to":"b","type":"relates-to"}]}`
	if err := os.MkdirAll(filepath.Join(dir, ".ggcode"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".ggcode", kgFileName), []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}
	r := kgMustOK(t, tool, `{"action":"trace","id":"a"}`)
	if !strings.Contains(r.Content, "--[relates-to]-->") {
		t.Errorf("legacy edge without temporal fields must stay visible: %s", r.Content)
	}
	if strings.Contains(r.Content, "validity window") {
		t.Errorf("legacy edge must not be counted as hidden: %s", r.Content)
	}
}

func TestKGParseTime(t *testing.T) {
	tm, err := kgParseTime("now")
	if err != nil || tm.IsZero() {
		t.Fatalf("now should parse: %v %v", tm, err)
	}
	if _, err := kgParseTime("2026-01-02T03:04:05Z"); err != nil {
		t.Fatalf("RFC3339 should parse: %v", err)
	}
	if _, err := kgParseTime("01/02/2026"); err == nil {
		t.Fatal("non-RFC3339 must fail")
	}
}
