package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDistillUsageHintFiltersAndNormalizes(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{
			name: "param semantics with path and number normalized",
			in:   `missing required parameter "file_path" for /Volumes/x/proj/main.go line 42`,
			want: `missing required parameter "X" for <path> line N`,
		},
		{
			name: "type mismatch distilled",
			in:   `type mismatch: offset must be number, got string`,
			want: `type mismatch: offset must be number, got string`,
		},
		{
			name: "infra error ignored",
			in:   `connection refused: dial tcp 10.0.0.1:443`,
			want: "",
		},
		{
			name: "first line only",
			in:   "invalid parameter \"x\": bad\nstack trace follows\nmore",
			want: `invalid parameter "X": bad`,
		},
	}
	for _, c := range cases {
		if got := distillUsageHint(c.in); got != c.want {
			t.Errorf("%s: distill = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestUsageHintStoreLifecycle(t *testing.T) {
	dir := t.TempDir()
	s := newToolUsageHintStore()
	s.attach(dir)

	// Failure records a hint; duplicate bumps count instead of appending.
	s.recordFailure("edit_file", `missing required parameter "old_text" in /tmp/f.go`)
	s.recordFailure("edit_file", `missing required parameter "old_text" in /tmp/g.go`)
	if got := len(s.hints["edit_file"]); got != 1 {
		t.Fatalf("duplicate hint appended: %d entries", got)
	}
	ov := s.OverlayFor("edit_file")
	if !strings.Contains(ov, "missing required parameter") || !strings.Contains(ov, "usage hints from past failures") {
		t.Fatalf("overlay missing hint: %q", ov)
	}
	if s.OverlayFor("read_file") != "" {
		t.Fatal("unrelated tool must have no overlay")
	}

	// Roundtrip: a fresh store loading the persisted file sees the hint.
	s2 := newToolUsageHintStore()
	s2.attach(dir)
	if s2.OverlayFor("edit_file") == "" {
		t.Fatal("hint did not persist across stores")
	}

	// Success decays the hint and persists the removal.
	s2.recordSuccess("edit_file")
	if s2.OverlayFor("edit_file") != "" {
		t.Fatal("success must decay the hint")
	}
	s3 := newToolUsageHintStore()
	s3.attach(dir)
	if s3.OverlayFor("edit_file") != "" {
		t.Fatal("decayed hint must not reload")
	}
}

func TestUsageHintStoreCapAndTTL(t *testing.T) {
	dir := t.TempDir()
	s := newToolUsageHintStore()
	s.attach(dir)
	for i := 0; i < usageHintsPerTool+3; i++ {
		s.recordFailure("browser", "invalid parameter: must be one of [navigate] attempt "+strings.Repeat("x", i))
	}
	if got := len(s.hints["browser"]); got != usageHintsPerTool {
		t.Fatalf("cap not enforced: %d hints", got)
	}

	// TTL: stale entries dropped on load.
	s.hints["browser"] = []usageHint{{
		Tool: "browser", Message: "invalid parameter: must be string",
		LastSeen: time.Now().Add(-usageHintsTTL - time.Hour), FailCount: 1,
	}}
	s.persistLocked()
	s2 := newToolUsageHintStore()
	s2.attach(dir)
	if len(s2.hints["browser"]) != 0 {
		t.Fatalf("expired hint survived TTL: %+v", s2.hints["browser"])
	}
}

func TestUsageHintStoreCorruptFileDegrades(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, usageHintsFileName), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := newToolUsageHintStore()
	s.attach(dir) // must not panic or brick
	s.recordFailure("grep", `missing required parameter "pattern"`)
	if s.OverlayFor("grep") == "" {
		t.Fatal("post-corruption recording must still work in-memory")
	}
}
