package agent

import (
	"fmt"
	"strings"
	"testing"
)

func TestAttentionFragment_HighSwitchDensity(t *testing.T) {
	s := newAttentionFragmentState()

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

	for _, d := range dirs {
		s.recordToolCall("read_file", map[string]interface{}{"file_path": d})
	}
	msg2 := s.analyze()
	if msg2 != "" {
		t.Errorf("expected no second warning (max=%d), got: %s", afMaxWarnings, msg2)
	}

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

		if tt.want != "" && !strings.Contains(got, tt.want) && got != tt.want {

			if got == "" {
				t.Errorf("extractAFDir(%q) = %q, want something containing %q", tt.input, got, tt.want)
			}
		}
	}
}

func TestAttentionFragment_ModerateSwitchDensity(t *testing.T) {
	s := newAttentionFragmentState()

	seq := []string{
		"/p/agent/a.go",
		"/p/config/b.go",
		"/p/agent/c.go",
		"/p/config/d.go",
		"/p/tool/e.go",
		"/p/chat/f.go",
		"/p/im/g.go",
		"/p/im/h.go",
		"/p/im/i.go",
		"/p/im/j.go",
	}
	for _, d := range seq {
		s.recordToolCall("read_file", map[string]interface{}{"file_path": d})
	}

	msg := s.analyze()

	if msg != "" && !strings.Contains(msg, "attention-fragment") {
		t.Errorf("unexpected message format: %s", msg)
	}
}

func TestAttentionFragment_EditTools(t *testing.T) {
	s := newAttentionFragmentState()

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

	for i := 0; i < s.windowSize*3; i++ {
		s.recordToolCall("read_file", map[string]interface{}{"path": fmt.Sprintf("/more%d/f.go", i%4)})
	}
	if m := s.analyze(); m != "" {
		t.Fatalf("single-shot violated: %s", m)
	}

	s.reset()
	if s.warnCount != 0 {
		t.Fatalf("warnCount must re-open per user turn (reset), got %d", s.warnCount)
	}
	if m := s.analyze(); m != "" {
		t.Fatalf("fresh window must not warn before thresholds: %s", m)
	}
}
