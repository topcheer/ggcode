package agent

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/tool"
)

func TestIssue5374SingleFileWarningNamesFile(t *testing.T) {
	s := newUnreadEditState()
	hint := s.checkUnreadEdit("/repo/a.go")
	if hint == "" {
		t.Fatal("expected warning for unread file")
	}
	if !strings.Contains(hint, "/repo/a.go") {
		t.Fatalf("single-file warning must name the file, got: %q", hint)
	}
	if strings.Contains(hint, "Files edited without prior read") {
		t.Fatalf("single-file warning should keep the per-file phrasing, got: %q", hint)
	}
}

func TestIssue5374MultiFileWarningCoalescedWithNames(t *testing.T) {
	s := newUnreadEditState()

	hint := unreadEditWarning([]string{s.normalize("/repo/a.go"), s.normalize("/repo/b.go"), s.normalize("/repo/c.go")})
	if hint == "" {
		t.Fatal("expected coalesced warning for 3 unread files")
	}
	for _, name := range []string{"/repo/a.go", "/repo/b.go", "/repo/c.go"} {
		if !strings.Contains(hint, name) {
			t.Fatalf("coalesced warning must name %s, got: %q", name, hint)
		}
	}
	if !strings.Contains(hint, "read_file") {
		t.Fatalf("coalesced warning must point at read_file, got: %q", hint)
	}
	if strings.Count(hint, "without prior read") != 1 {
		t.Fatalf("coalesced warning must appear once, got: %q", hint)
	}

	if got := unreadEditWarning(nil); got != "" {
		t.Fatalf("empty input must render empty, got %q", got)
	}
}

func TestIssue5374GuardStillPerFile(t *testing.T) {
	s := newUnreadEditState()

	if s.warnUnreadPath("/repo/a.go") == "" {
		t.Fatal("expected first check on a.go to warn")
	}
	if s.warnUnreadPath("/repo/a.go") != "" {
		t.Fatal("guard must stay per-file: second check on a.go must not warn again")
	}
	if s.warnUnreadPath("/repo/b.go") == "" {
		t.Fatal("first check on b.go must warn (independent file)")
	}

	s.recordRead("/repo/c.go")
	if s.warnUnreadPath("/repo/c.go") != "" {
		t.Fatal("read file must not warn")
	}
}

func TestIssue5374AppendGuidanceDedupsIdenticalText(t *testing.T) {
	a := &Agent{}
	res := &tool.Result{Content: "tool output line"}

	hint := "You are editing this file without reading it first in this session. Read it with read_file to ensure your edit targets the actual content."

	if !a.appendGuidance(res, hint) {
		t.Fatal("first append of the hint must succeed")
	}
	if a.appendGuidance(res, hint) {
		t.Fatal("identical hint appended to the same tool result must be suppressed (#5374 verbatim-repeat bug)")
	}
	if strings.Count(res.Content, "You are editing this file") != 1 {
		t.Fatalf("hint text must appear exactly once, got: %q", res.Content)
	}

	other := "A different hint entirely."
	if !a.appendGuidance(res, other) {
		t.Fatal("a different hint must still append after a deduped duplicate")
	}
}

func TestIssue5374AppendGuidanceEmptyResultFirstAppend(t *testing.T) {
	a := &Agent{}
	res := &tool.Result{Content: ""}
	hint := "one-off guidance hint"
	if !a.appendGuidance(res, hint) {
		t.Fatal("first append onto empty result must succeed")
	}
	if res.Content != hint {
		t.Fatalf("expected result content %q, got %q", hint, res.Content)
	}
}

func TestIssue5374MultiFileEditExtractsAllPaths(t *testing.T) {
	args, _ := json.Marshal(map[string]any{
		"files": []any{
			map[string]any{"path": "/x/a.go"},
			map[string]any{"path": "/x/b.go"},
		},
	})
	paths := extractEditFilePaths("multi_file_edit", args)
	if len(paths) != 2 {
		t.Fatalf("expected 2 paths, got %v", paths)
	}
}

// TestIssue5374AgentLoopWiring anchors the production loop shape: paths are
// collected via warnUnreadPath inside the per-file loop and ONE coalesced
// unreadEditWarning is appended after it. Guards against a silent revert to
// per-path checkUnreadEdit -> appendGuidance (the N-verbatim-repeat bug).
func TestIssue5374AgentLoopWiring(t *testing.T) {
	src, err := os.ReadFile("agent.go")
	if err != nil {
		t.Fatal(err)
	}
	collect := "if n := a.unreadEdit.warnUnreadPath(p); n != \"\" {"
	if !strings.Contains(string(src), collect) {
		t.Fatalf("agent.go must collect unread paths via warnUnreadPath in the edit loop (#5374), anchor missing: %q", collect)
	}
	emit := "if hint := unreadEditWarning(unreadEditPaths); hint != \"\" {"
	if !strings.Contains(string(src), emit) {
		t.Fatalf("agent.go must emit one coalesced unreadEditWarning after the edit loop (#5374), anchor missing: %q", emit)
	}
	if strings.Count(string(src), "unreadEdit.checkUnreadEdit(") != 0 {
		t.Fatal("agent.go must not call checkUnreadEdit per path anymore (#5374 per-path append was the bug)")
	}
}
