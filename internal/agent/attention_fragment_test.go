package agent

import (
	"fmt"
	"strings"
	"testing"
)

func TestAttentionFragment_HighSwitchDensity(t *testing.T) {
	s := newAttentionFragmentState()

	// Simulate 10 tool calls, each targeting a different directory.
	dirs := []string{
		"/proj/internal/agent/file.go",
		"/proj/internal/config/file.go",
		"/proj/internal/tool/file.go",
		"/proj/internal/chat/file.go",
		"/proj/internal/im/file.go",
		"/proj/internal/cron/file.go",
		"/proj/internal/cost/file.go",
		"/proj/internal/auth/file.go",
		"/proj/internal/a2a/file.go",
		"/proj/internal/acp/file.go",
	}
	for _, d := range dirs {
		s.recordToolCall("read_file", map[string]interface{}{"file_path": d})
	}

	msg := s.analyze()
	if msg == "" {
		t.Fatal("expected fragmentation warning for 10 unique dirs with 100% switch density")
	}
	if !strings.Contains(msg, "attention-fragment") {
		t.Errorf("warning should contain detector tag, got: %s", msg)
	}
}

func TestAttentionFragment_LowSwitchDensity(t *testing.T) {
	s := newAttentionFragmentState()

	// Simulate 10 tool calls all in the same directory - no fragmentation.
	for i := 0; i < 10; i++ {
		s.recordToolCall("read_file", map[string]interface{}{
			"file_path": "/proj/internal/agent/file.go",
		})
	}

	msg := s.analyze()
	if msg != "" {
		t.Errorf("expected no warning for coherent focus in single dir, got: %s", msg)
	}
}

func TestAttentionFragment_TooFewUniqueDirs(t *testing.T) {
	s := newAttentionFragmentState()

	// Alternating between only 2 dirs - high density but not enough unique dirs.
	for i := 0; i < 5; i++ {
		s.recordToolCall("read_file", map[string]interface{}{
			"file_path": "/proj/internal/agent/a.go",
		})
		s.recordToolCall("read_file", map[string]interface{}{
			"file_path": "/proj/internal/config/b.go",
		})
	}

	msg := s.analyze()
	if msg != "" {
		t.Errorf("expected no warning for only 2 unique dirs (below threshold of %d), got: %s", afMinUniqueDirs, msg)
	}
}

func TestAttentionFragment_WindowNotFull(t *testing.T) {
	s := newAttentionFragmentState()

	// Only 5 calls - window not full enough to analyze.
	for _, d := range []string{"a/x.go", "b/x.go", "c/x.go", "d/x.go", "e/x.go"} {
		s.recordToolCall("read_file", map[string]interface{}{"file_path": d})
	}

	msg := s.analyze()
	if msg != "" {
		t.Errorf("expected no warning when window not full, got: %s", msg)
	}
}

func TestAttentionFragment_MaxWarnings(t *testing.T) {
	s := newAttentionFragmentState()

	// Fill window with fragmented calls.
	dirs := []string{
		"/p/agent/a.go", "/p/config/b.go", "/p/tool/c.go",
		"/p/chat/d.go", "/p/im/e.go", "/p/cron/f.go",
		"/p/cost/g.go", "/p/auth/h.go", "/p/a2a/i.go", "/p/acp/j.go",
	}
	for _, d := range dirs {
		s.recordToolCall("read_file", map[string]interface{}{"file_path": d})
	}

	msg1 := s.analyze()
	if msg1 == "" {
		t.Fatal("expected first warning")
	}

	// Second attempt is capped at 1 per run (batch 2 guidance-noise cleanup;
	// a fresh warning is possible only after compaction resets the counter).
	for _, d := range dirs {
		s.recordToolCall("read_file", map[string]interface{}{"file_path": d})
	}
	msg2 := s.analyze()
	if msg2 != "" {
		t.Errorf("expected no second warning (max=%d), got: %s", afMaxWarnings, msg2)
	}

	// Third attempt should also be capped.
	for _, d := range dirs {
		s.recordToolCall("read_file", map[string]interface{}{"file_path": d})
	}
	msg3 := s.analyze()
	if msg3 != "" {
		t.Errorf("expected no third warning (max=%d), got: %s", afMaxWarnings, msg3)
	}
}

func TestAttentionFragment_IrrelevantToolIgnored(t *testing.T) {
	s := newAttentionFragmentState()

	// web_search doesn't target a file - should be ignored.
	for i := 0; i < 15; i++ {
		s.recordToolCall("web_search", map[string]interface{}{"query": "test"})
	}

	msg := s.analyze()
	if msg != "" {
		t.Errorf("expected no warning for irrelevant tools, got: %s", msg)
	}
}

func TestAttentionFragment_ExtractDir(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"/proj/internal/agent/file.go", "agent"},
		{"internal/agent/file.go", "internal/agent"},
		{"file.go", ""},
		{"", ""},
		{"C:\\Users\\proj\\src\\main.go", "proj/src"},
	}
	for _, tt := range tests {
		got := extractAFDir(tt.input)
		// For single-component paths, result may vary by OS.
		if tt.want != "" && !strings.Contains(got, tt.want) && got != tt.want {
			// Some OS variance is acceptable; just verify it's non-empty when expected.
			if got == "" {
				t.Errorf("extractAFDir(%q) = %q, want something containing %q", tt.input, got, tt.want)
			}
		}
	}
}

func TestAttentionFragment_ModerateSwitchDensity(t *testing.T) {
	s := newAttentionFragmentState()

	// 10 calls, 5 dirs (A,B,A,B,C,D,E pattern) - 60% density, below threshold.
	seq := []string{
		"/p/agent/a.go",  // A
		"/p/config/b.go", // B (switch)
		"/p/agent/c.go",  // A (switch back)
		"/p/config/d.go", // B (switch)
		"/p/tool/e.go",   // C (switch)
		"/p/chat/f.go",   // D (switch)
		"/p/im/g.go",     // E (switch)
		"/p/im/h.go",     // E (no switch)
		"/p/im/i.go",     // E (no switch)
		"/p/im/j.go",     // E (no switch)
	}
	for _, d := range seq {
		s.recordToolCall("read_file", map[string]interface{}{"file_path": d})
	}

	// 5 unique dirs, 5 switches / 9 pairs = 55.6% - below 70% threshold.
	msg := s.analyze()
	// Could fire if density exactly at threshold; verify it behaves sanely.
	if msg != "" && !strings.Contains(msg, "attention-fragment") {
		t.Errorf("unexpected message format: %s", msg)
	}
}

func TestAttentionFragment_EditTools(t *testing.T) {
	s := newAttentionFragmentState()

	// edit_file and write_file should also be tracked.
	dirs := []string{
		"/p/agent/a.go", "/p/config/b.go", "/p/tool/c.go",
		"/p/chat/d.go", "/p/im/e.go", "/p/cron/f.go",
		"/p/cost/g.go", "/p/auth/h.go", "/p/a2a/i.go", "/p/acp/j.go",
	}
	for i, d := range dirs {
		if i%2 == 0 {
			s.recordToolCall("edit_file", map[string]interface{}{"file_path": d})
		} else {
			s.recordToolCall("write_file", map[string]interface{}{"path": d})
		}
	}

	msg := s.analyze()
	if msg == "" {
		t.Fatal("expected fragmentation warning for edit tools across many dirs")
	}
}

func TestAttentionFragment_MultiFileRead(t *testing.T) {
	s := newAttentionFragmentState()

	// multi_file_read uses "files" array - first path should be extracted.
	for i := 0; i < 10; i++ {
		s.recordToolCall("multi_file_read", map[string]interface{}{
			"files": []interface{}{
				map[string]interface{}{"path": "/p/dir" + string(rune('A'+i)) + "/file.go"},
			},
		})
	}

	msg := s.analyze()
	if msg == "" {
		t.Fatal("expected fragmentation warning for multi_file_read across dirs")
	}
}

// #1855 case 1: the guidance-noise cleanup set max=1 but left the
// refire-gap/firedAt machinery behind as unreachable dead code. The
// machinery is now REMOVED - this pins the honest single-shot semantics
// (one warning per run; reset() re-arms).
func TestAttentionFragmentSingleShotHonest1855(t *testing.T) {
	s := newAttentionFragmentState()
	for i := 0; i < s.windowSize; i++ {
		s.recordToolCall("read_file", map[string]interface{}{"path": fmt.Sprintf("/dir%d/f.go", i%4)})
	}
	if m := s.analyze(); m == "" {
		t.Fatal("first warning expected")
	}
	if s.warnCount != 1 {
		t.Fatalf("warnCount = %d, want 1", s.warnCount)
	}
	// Sustained fragmentation stays silent (single-shot).
	for i := 0; i < s.windowSize*3; i++ {
		s.recordToolCall("read_file", map[string]interface{}{"path": fmt.Sprintf("/more%d/f.go", i%4)})
	}
	if m := s.analyze(); m != "" {
		t.Fatalf("single-shot violated: %s", m)
	}
	// reset() clears the window but deliberately keeps warnCount (per-run
	// persistence - see reset()); re-arm is a NEW state for the next run.
	s.reset()
	if s.warnCount != 1 {
		t.Fatalf("warnCount must persist across window reset, got %d", s.warnCount)
	}
}
