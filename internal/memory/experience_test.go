package memory

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/config"
)

func newTestExperienceStore(t *testing.T) *ExperienceStore {
	t.Helper()
	return NewExperienceStore(filepath.Join(t.TempDir(), "experience"))
}

func TestExperienceRecordAndRetrieve(t *testing.T) {
	es := newTestExperienceStore(t)

	id1, updated, err := es.Record(
		"Fix flaky login test in auth package",
		"Root cause: time.Now() not injected. Fixed by passing a clock into SessionValidator.",
		"success",
		[]string{"internal/auth/session.go", "internal/auth/session_test.go"},
	)
	if err != nil {
		t.Fatalf("Record failed: %v", err)
	}
	if updated {
		t.Fatalf("first record should not be an update")
	}
	if id1 == "" {
		t.Fatalf("empty id")
	}

	if _, _, err := es.Record(
		"Add rate limiting to the payments endpoint",
		"Used a token bucket middleware; configurable per-route limits.",
		"success",
		[]string{"internal/api/payments.go"},
	); err != nil {
		t.Fatalf("second Record failed: %v", err)
	}

	scored, err := es.Retrieve("login test is flaky again, auth", 3)
	if err != nil {
		t.Fatalf("Retrieve failed: %v", err)
	}
	if len(scored) == 0 {
		t.Fatalf("expected at least one match for login/auth query")
	}
	if scored[0].Task != "fix flaky login test in auth package" {
		t.Fatalf("top match = %q, want the auth case", scored[0].Task)
	}
	// The unrelated payments case must not outrank the auth case.
	if len(scored) > 1 && scored[1].Score >= scored[0].Score {
		t.Fatalf("scoring not discriminative: %v", scored)
	}
	if !strings.Contains(scored[0].Approach, "clock") {
		t.Fatalf("approach body lost: %q", scored[0].Approach)
	}
}

func TestExperienceRecordDedupesSameTask(t *testing.T) {
	es := newTestExperienceStore(t)

	id1, _, err := es.Record("Fix the login flake", "first attempt", "failed", nil)
	if err != nil {
		t.Fatalf("Record 1 failed: %v", err)
	}
	time.Sleep(10 * time.Millisecond)
	id2, updated, err := es.Record("Fix  the  LOGIN   flake", "second attempt worked", "success", []string{"auth.go"})
	if err != nil {
		t.Fatalf("Record 2 failed: %v", err)
	}
	if id1 != id2 {
		t.Fatalf("same logical task produced different ids %q vs %q", id1, id2)
	}
	if !updated {
		t.Fatalf("re-record of same task should report updated=true")
	}

	cases, err := es.List()
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(cases) != 1 {
		t.Fatalf("dedupe failed: got %d cases, want 1", len(cases))
	}
	if cases[0].Outcome != "success" || !strings.Contains(cases[0].Approach, "second attempt") {
		t.Fatalf("case not reconsolidated: %+v", cases[0])
	}
	if cases[0].Files == nil || len(cases[0].Files) != 1 || cases[0].Files[0] != "auth.go" {
		t.Fatalf("files not refreshed: %v", cases[0].Files)
	}
}

func TestExperienceCapEvictsOldest(t *testing.T) {
	es := newTestExperienceStore(t)
	for i := 0; i < MaxExperienceCases+5; i++ {
		if _, _, err := es.Record(string(rune('a'+i%26))+"-task number "+string(rune('0'+i%10))+" variant "+string(rune('a'+i)), "approach", "success", nil); err != nil {
			t.Fatalf("Record %d failed: %v", i, err)
		}
	}
	cases, err := es.List()
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(cases) > MaxExperienceCases {
		t.Fatalf("cap not enforced: %d cases", len(cases))
	}
}

func TestExperienceFormatIndexEmptyWhenNoMatch(t *testing.T) {
	es := newTestExperienceStore(t)
	if got := es.FormatIndex("anything", 3); got != "" {
		t.Fatalf("empty store should format nothing, got %q", got)
	}
	if _, _, err := es.Record("Refactor the widget cache layer", "split cache into read/write halves", "success", nil); err != nil {
		t.Fatalf("Record failed: %v", err)
	}
	if got := es.FormatIndex("totally unrelated query about satellites", 3); got != "" {
		t.Fatalf("irrelevant query should match nothing, got %q", got)
	}
	idx := es.FormatIndex("refactor widget cache", 3)
	if idx == "" {
		t.Fatalf("relevant query should produce an index")
	}
	if !strings.Contains(idx, "outcome: success") || !strings.Contains(idx, "widget") {
		t.Fatalf("index missing case details: %q", idx)
	}
}

func TestExperienceRoundTripPersistsFields(t *testing.T) {
	es := newTestExperienceStore(t)
	id, _, err := es.Record("Port the streaming parser to v2", "Strangler-fig migration behind a build tag.", "partial",
		[]string{"parser/a.go", "parser/b.go"})
	if err != nil {
		t.Fatalf("Record failed: %v", err)
	}

	// Fresh store instance over the same dir (simulates a new session).
	es2 := NewExperienceStore(es.Dir())
	cases, err := es2.List()
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(cases) != 1 {
		t.Fatalf("want 1 case, got %d", len(cases))
	}
	c := cases[0]
	if c.ID != id || c.Outcome != "partial" {
		t.Fatalf("round-trip mismatch: %+v", c)
	}
	if len(c.Files) != 2 || c.Files[0] != "parser/a.go" {
		t.Fatalf("files lost in round-trip: %v", c.Files)
	}
	if !strings.Contains(c.Approach, "Strangler-fig") {
		t.Fatalf("approach lost in round-trip: %q", c.Approach)
	}
	if c.Created.IsZero() || c.Updated.IsZero() {
		t.Fatalf("timestamps lost: %+v", c)
	}
}

func TestExperienceRecordValidation(t *testing.T) {
	es := newTestExperienceStore(t)
	if _, _, err := es.Record("   ", "approach", "success", nil); err == nil {
		t.Fatalf("empty task should be rejected")
	}
	if _, _, err := es.Record("valid task", "", "bogus-outcome", nil); err != nil {
		t.Fatalf("unknown outcome should coerce, not error: %v", err)
	}
	cases, _ := es.List()
	if len(cases) != 1 || cases[0].Outcome != "partial" {
		t.Fatalf("outcome coercion failed: %+v", cases)
	}

	// Disabled store (empty dir) rejects Record.
	if _, _, err := (&ExperienceStore{}).Record("t", "a", "success", nil); err == nil {
		t.Fatalf("disabled store should reject Record")
	}
	// And Retrieve/List/FormatIndex are safe no-ops.
	if got := (&ExperienceStore{}).FormatIndex("q", 3); got != "" {
		t.Fatalf("disabled store should format nothing")
	}
}

func TestExperienceProjectStoreSkipsHome(t *testing.T) {
	if NewProjectExperienceStore("") != nil {
		t.Fatalf("empty workingDir should yield nil store")
	}
	if s := NewProjectExperienceStore(config.HomeDir()); s != nil {
		t.Fatalf("HOME workingDir should yield nil store, got %v", s.Dir())
	}
}

func TestExperienceSubdirInvisibleToMemoryIndex(t *testing.T) {
	root := t.TempDir()
	am := &AutoMemory{dir: root}
	if err := os.WriteFile(filepath.Join(root, "regular.md"), []byte("body"), 0644); err != nil {
		t.Fatalf("setup failed: %v", err)
	}
	es := NewExperienceStore(filepath.Join(root, "experience"))
	if _, _, err := es.Record("a task", "an approach", "success", nil); err != nil {
		t.Fatalf("Record failed: %v", err)
	}
	idx, _, err := am.LoadIndex()
	if err != nil {
		t.Fatalf("LoadIndex failed: %v", err)
	}
	if strings.Contains(idx, "a task") || strings.Contains(idx, "experience") {
		t.Fatalf("experience cases leaked into memory index: %q", idx)
	}
	if !strings.Contains(idx, "regular") {
		t.Fatalf("regular memory missing from index: %q", idx)
	}
}

func TestExpTokenize(t *testing.T) {
	toks := expTokenize("Fix the FLAKY login-test, 3rd time!")
	want := map[string]bool{"fix": true, "the": true, "flaky": true, "login": true, "test": true, "3rd": true, "time": true}
	if len(toks) != len(want) {
		t.Fatalf("tokenize = %v, want %v", toks, want)
	}
	for _, tok := range toks {
		if !want[tok] {
			t.Fatalf("unexpected token %q in %v", tok, toks)
		}
	}
	cjk := expTokenize("修复登录测试")
	if len(cjk) != 6 {
		t.Fatalf("CJK runes should tokenize per-rune, got %v", cjk)
	}
}
