package agent

import (
	"strings"
	"testing"
)

func TestInfoScent_NoExploration(t *testing.T) {
	s := newInfoScentState()
	s.recordExploration("edit_file", `{"path": "foo.go"}`, "ok", 1)
	if msg := s.maybeWarn(1); msg != "" {
		t.Fatalf("expected no warning for non-exploration tool, got: %s", msg)
	}
}

func TestInfoScent_TooFewPaths(t *testing.T) {
	s := newInfoScentState()
	s.recordExploration("read_file", `{"path": "foo.go"}`, "content", 1)
	if msg := s.maybeWarn(1); msg != "" {
		t.Fatalf("expected no warning for single-path exploration, got: %s", msg)
	}
}

func TestInfoScent_HighNovelty(t *testing.T) {
	s := newInfoScentState()
	s.recordExploration("grep", `{}`, "foo.go:1\nbar.go:2\nbaz.go:3", 1)
	s.recordExploration("grep", `{}`, "qux.go:1\nquux.go:2", 2)
	if msg := s.maybeWarn(2); msg != "" {
		t.Fatalf("expected no warning with high novelty, got: %s", msg)
	}
}

func TestInfoScent_DecayDetected(t *testing.T) {
	s := newInfoScentState()
	// First exploration: 3 novel paths (100% novelty)
	s.recordExploration("grep", `{}`, "a.go:1\nb.go:2\nc.go:3", 1)
	// Second through fourth: all already-seen paths (0% novelty each)
	s.recordExploration("grep", `{}`, "a.go:1\nb.go:2", 2)
	s.recordExploration("grep", `{}`, "a.go:1\nb.go:2", 3)
	s.recordExploration("grep", `{}`, "a.go:1\nb.go:2", 4)

	msg := s.maybeWarn(4)
	if msg == "" {
		t.Fatal("expected scent decay warning after 3+ low-novelty explorations")
	}
	if !strings.Contains(msg, "info-scent") {
		t.Errorf("warning should contain [info-scent] tag, got: %s", msg)
	}
	if !strings.Contains(msg, "depleted") {
		t.Errorf("warning should mention depleted patch, got: %s", msg)
	}
}

func TestInfoScent_MaxInjections(t *testing.T) {
	s := newInfoScentState()
	// Trigger decay - all same paths
	s.recordExploration("grep", `{}`, "a.go\nb.go\nc.go", 1)
	s.recordExploration("grep", `{}`, "a.go\nb.go", 2)
	s.recordExploration("grep", `{}`, "a.go\nb.go", 3)
	s.recordExploration("grep", `{}`, "a.go\nb.go", 4)

	msg1 := s.maybeWarn(4)
	if msg1 == "" {
		t.Fatal("expected first warning")
	}

	// Advance past cooldown (4 iterations) and try again
	s.recordExploration("grep", `{}`, "a.go\nb.go", 9)
	msg2 := s.maybeWarn(9)
	if msg2 == "" {
		t.Fatal("expected second warning")
	}

	// Third should be suppressed
	s.recordExploration("grep", `{}`, "a.go\nb.go", 14)
	msg3 := s.maybeWarn(14)
	if msg3 != "" {
		t.Fatalf("expected no third warning (max injections reached), got: %s", msg3)
	}
}

func TestInfoScent_Reset(t *testing.T) {
	s := newInfoScentState()
	s.recordExploration("grep", `{}`, "a.go:1\nb.go:2\nc.go:3", 1)
	if len(s.allSeenPaths) == 0 {
		t.Fatal("expected paths to be tracked")
	}

	s.reset()
	if len(s.allSeenPaths) != 0 {
		t.Errorf("expected paths cleared after reset, got %d", len(s.allSeenPaths))
	}
	if len(s.recentExplorations) != 0 {
		t.Errorf("expected explorations cleared after reset, got %d", len(s.recentExplorations))
	}
	if s.injectionCount != 0 {
		t.Errorf("expected injectionCount cleared, got %d", s.injectionCount)
	}
}

func TestInfoScent_CooldownBetweenWarnings(t *testing.T) {
	s := newInfoScentState()
	s.recordExploration("grep", `{}`, "a.go\nb.go\nc.go", 1)
	s.recordExploration("grep", `{}`, "a.go\nb.go", 2)
	s.recordExploration("grep", `{}`, "a.go\nb.go", 3)
	s.recordExploration("grep", `{}`, "a.go\nb.go", 4)

	msg1 := s.maybeWarn(4)
	if msg1 == "" {
		t.Fatal("expected first warning")
	}

	// Next iteration - should be in cooldown (need 4 iteration gap)
	msg2 := s.maybeWarn(5)
	if msg2 != "" {
		t.Fatalf("expected no warning during cooldown, got: %s", msg2)
	}
}

func TestExtractFilePathsFromJSON(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  int
	}{
		{"empty", "", 0},
		{"single path", `{"path": "foo.go"}`, 1},
		{"files array", `{"files": ["a.go", "b.go"]}`, 2},
		{"no path fields", `{"foo": "bar"}`, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			paths := extractFilePathsFromJSON(tt.input)
			if len(paths) != tt.want {
				t.Errorf("got %d paths (%v), want %d", len(paths), paths, tt.want)
			}
		})
	}
}

func TestExtractFilePathsFromText(t *testing.T) {
	text := "some output\nfile.go:42: TODO\nanother.go:10: FIXME\nnot a path here\n"
	paths := extractFilePathsFromText(text)
	if len(paths) < 2 {
		t.Errorf("expected at least 2 paths, got %d: %v", len(paths), paths)
	}
}

func TestInfoScent_WindowEviction(t *testing.T) {
	s := newInfoScentState()
	for i := 0; i < 6; i++ {
		s.recordExploration("grep", `{}`, "a.go\nb.go", i+1)
	}
	if len(s.recentExplorations) > scentWindowSize+1 {
		t.Errorf("window should be capped, got %d entries", len(s.recentExplorations))
	}
}
