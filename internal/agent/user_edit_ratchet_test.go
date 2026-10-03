package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func restoreMtime(old func(string) (time.Time, error)) { mtimeOf = old }

func newTestObserver(t *testing.T) (*UserEditObserver, string) {
	t.Helper()
	dir := t.TempDir()
	rs := NewRuleStore(dir)
	if rs == nil {
		t.Fatal("nil rule store")
	}
	return newUserEditObserver(rs), dir
}

// TestUserEditSingleObservationNoPromotion: one turn-gap rewrite must NOT
// become a rule (anti-false-learn: user experiments, one-off fmt runs).
func TestUserEditSingleObservationNoPromotion(t *testing.T) {
	o, dir := newTestObserver(t)
	f := filepath.Join(dir, "main.go")
	if err := os.WriteFile(f, []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	t1 := time.Now()
	old := mtimeOf
	mtimeOf = func(string) (time.Time, error) { return t1, nil }
	o.NoteAgentWrite(f)
	// gap: user rewrote the file -> mtime moved
	mtimeOf = func(string) (time.Time, error) { return t1.Add(time.Minute), nil }
	o.CheckTurnBoundary()
	restoreMtime(old)

	if got := len(o.store.Rules()); got != 0 {
		t.Fatalf("single observation must not promote, got %d rules", got)
	}
	if o.pending[f] == nil || o.pending[f].turns != 1 {
		t.Fatalf("expected pending observation turns=1, got %+v", o.pending[f])
	}
}

// TestUserEditTwoGapsPromote: two independent turn-gap rewrites promote a
// user_edit rule with basename ToolPattern and Source set.
func TestUserEditTwoGapsPromote(t *testing.T) {
	o, dir := newTestObserver(t)
	f := filepath.Join(dir, "widget.go")
	base := time.Now()

	for gap := 0; gap < 2; gap++ {
		old := mtimeOf
		mtimeOf = func(string) (time.Time, error) {
			return base.Add(time.Duration(gap) * time.Hour), nil
		}
		o.NoteAgentWrite(f)
		mtimeOf = func(string) (time.Time, error) {
			return base.Add(time.Duration(gap)*time.Hour + time.Minute), nil
		}
		o.CheckTurnBoundary()
		restoreMtime(old)
	}

	rules := o.store.Rules()
	if len(rules) != 1 {
		t.Fatalf("expected 1 promoted rule, got %d", len(rules))
	}
	r := rules[0]
	if r.Source != ruleSourceUserEdit {
		t.Fatalf("Source = %q, want %q", r.Source, ruleSourceUserEdit)
	}
	if r.Category != "convention" {
		t.Fatalf("Category = %q, want convention", r.Category)
	}
	if !strings.Contains(r.ToolPattern, "widget\\.go") {
		t.Fatalf("ToolPattern %q must match basename", r.ToolPattern)
	}
	if !strings.Contains(r.Rule, "widget.go") {
		t.Fatalf("Rule text must mention the file: %q", r.Rule)
	}
	// promotion consumes the pending observation
	if _, still := o.pending[f]; still {
		t.Fatal("promotion must clear pending observation")
	}
}

// TestUserEditNoExternalChangeNoObservation: untouched mtime across the
// gap records nothing.
func TestUserEditNoExternalChangeNoObservation(t *testing.T) {
	o, dir := newTestObserver(t)
	f := filepath.Join(dir, "same.go")
	mt := time.Now()
	old := mtimeOf
	mtimeOf = func(string) (time.Time, error) { return mt, nil }
	o.NoteAgentWrite(f)
	o.CheckTurnBoundary()
	restoreMtime(old)

	if len(o.pending) != 0 {
		t.Fatalf("no mtime move must leave pending empty, got %+v", o.pending)
	}
	if len(o.store.Rules()) != 0 {
		t.Fatalf("no rules expected, got %d", len(o.store.Rules()))
	}
}

// TestUserEditRuleSourceBackwardCompat: pre-r444 agent-rules.json (no
// source field) loads with empty Source; rewrite keeps omitempty so the
// file stays byte-compatible for the error path.
func TestUserEditRuleSourceBackwardCompat(t *testing.T) {
	dir := t.TempDir()
	gg := filepath.Join(dir, ".ggcode")
	if err := os.MkdirAll(gg, 0o755); err != nil {
		t.Fatal(err)
	}
	legacy := `{"version":1,"rules":[{"id":"r-old","category":"build","rule":"run make verify","match_pattern":"verify failed","hit_count":3,"last_seen":"2026-01-01T00:00:00Z","created_at":"2026-01-01T00:00:00Z"}]}`
	if err := os.WriteFile(filepath.Join(gg, "agent-rules.json"), []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}
	rs := NewRuleStore(dir)
	rules := rs.Rules()
	if len(rules) != 1 || rules[0].Source != "" {
		t.Fatalf("legacy rule must load with empty Source, got %+v", rules)
	}
	// add a user_edit rule and verify JSON round-trip keeps both sources
	rs.AddRule(userEditRule(filepath.Join(dir, "x.go"), 2))
	rs.mu.Lock()
	raw, err := os.ReadFile(filepath.Join(gg, "agent-rules.json"))
	rs.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	var persisted struct {
		Rules []map[string]any `json:"rules"`
	}
	if err := json.Unmarshal(raw, &persisted); err != nil {
		t.Fatal(err)
	}
	sources := map[string]bool{}
	for _, m := range persisted.Rules {
		if s, ok := m["source"].(string); ok {
			sources[s] = true
		}
	}
	if sources[ruleSourceUserEdit] != true {
		t.Fatalf("user_edit rule must persist its source, json: %s", raw)
	}
}

// TestExtractUserEditPath: argument shapes for the four write tools.
func TestExtractUserEditPath(t *testing.T) {
	cases := []struct {
		name, args, want string
	}{
		{"edit_file", `{"file_path":"/a/b.go","old_text":"x"}`, "/a/b.go"},
		{"write_file", `{"path":"/a/c.go","content":"y"}`, "/a/c.go"},
		{"multi_file_edit", `{"files":[{"path":"/a/d.go"}],"mode":"atomic"}`, "/a/d.go"},
		{"multi_edit_file", `{"file_path":"/a/e.go","edits":[]}`, "/a/e.go"},
		{"bad_json", `{not json`, ""},
		{"empty", `{}`, ""},
	}
	for _, c := range cases {
		if got := extractUserEditPath(c.name, []byte(c.args)); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

// TestUserEditObserverNilSafety: nil store observer never panics.
func TestUserEditObserverNilSafety(t *testing.T) {
	o := newUserEditObserver(nil)
	o.NoteAgentWrite("/whatever/exists.go")
	o.CheckTurnBoundary() // must not panic
}

// TestUserEditFloodGuard: per-turn tracked-file cap stops novel paths
// beyond userEditMaxTracked while re-writes of tracked files still record.
func TestUserEditFloodGuard(t *testing.T) {
	o, _ := newTestObserver(t)
	mt := time.Now()
	old := mtimeOf
	mtimeOf = func(string) (time.Time, error) { return mt, nil }
	defer restoreMtime(old)

	for i := 0; i < userEditMaxTracked+5; i++ {
		o.NoteAgentWrite(filepath.Join("/flood", "f"+string(rune('a'+i%26))+string(rune('a'+i/26))+".go"))
	}
	if len(o.wrote) != userEditMaxTracked {
		t.Fatalf("flood guard: wrote = %d, want %d", len(o.wrote), userEditMaxTracked)
	}
}

// fakeClock shim kept unused-var clean.
