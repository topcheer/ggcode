package agent

import (
	"strings"
	"testing"
)

func TestEmptySearchState_ConsecutiveEmpties(t *testing.T) {
	e := newEmptySearchState()

	// First empty result — no guidance yet
	g := e.recordResult("grep", "No matches found.", false, "TODO")
	if g != "" {
		t.Fatalf("expected no guidance on 1st empty, got: %q", g)
	}
	if e.consecutive != 1 {
		t.Fatalf("expected consecutive=1, got %d", e.consecutive)
	}

	// Second empty — still no guidance
	g = e.recordResult("glob", "No files found.", false, "*.xyz")
	if g != "" {
		t.Fatalf("expected no guidance on 2nd empty, got: %q", g)
	}
	if e.consecutive != 2 {
		t.Fatalf("expected consecutive=2, got %d", e.consecutive)
	}

	// Third empty — guidance should fire
	g = e.recordResult("search_files", "No matches found for pattern.", false, "test")
	if g == "" {
		t.Fatal("expected guidance on 3rd consecutive empty, got empty string")
	}
	if !strings.Contains(g, "Empty search spiral") {
		t.Errorf("expected 'Empty search spiral' in guidance, got: %q", g)
	}
	if e.guidanceFired != 1 {
		t.Errorf("expected guidanceFired=1, got %d", e.guidanceFired)
	}
}

func TestEmptySearchState_ResetOnNonEmpty(t *testing.T) {
	e := newEmptySearchState()

	// Two empties
	e.recordResult("grep", "No matches found.", false, "TODO")
	e.recordResult("glob", "No files found.", false, "*.xyz")

	// A productive result resets the counter
	g := e.recordResult("grep", "/path/to/file.go:42:func main()", false, "func main")
	if g != "" {
		t.Fatalf("expected no guidance on productive result, got: %q", g)
	}
	if e.consecutive != 0 {
		t.Fatalf("expected consecutive=0 after productive result, got %d", e.consecutive)
	}

	// Need 3 more empties to trigger again
	g = e.recordResult("grep", "No matches found.", false, "TODO")
	if g != "" {
		t.Fatal("expected no guidance — streak was reset")
	}
}

func TestEmptySearchState_ResetOnError(t *testing.T) {
	e := newEmptySearchState()

	e.recordResult("grep", "No matches found.", false, "TODO")
	e.recordResult("glob", "No files found.", false, "*.xyz")

	// An error result resets the streak
	g := e.recordResult("grep", "permission denied", true, "TODO")
	if g != "" {
		t.Fatalf("expected no guidance on error, got: %q", g)
	}
	if e.consecutive != 0 {
		t.Fatalf("expected consecutive=0 after error, got %d", e.consecutive)
	}
}

func TestEmptySearchState_ResetOnNonSearchTool(t *testing.T) {
	e := newEmptySearchState()

	e.recordResult("grep", "No matches found.", false, "TODO")
	e.recordResult("glob", "No files found.", false, "*.xyz")

	// A non-search tool result resets the streak
	g := e.recordResult("read_file", "file contents here", false, "/some/file")
	if g != "" {
		t.Fatalf("expected no guidance on non-search result, got: %q", g)
	}
	if e.consecutive != 0 {
		t.Fatalf("expected consecutive=0 after non-search, got %d", e.consecutive)
	}
}

func TestEmptySearchState_MaxFires(t *testing.T) {
	e := newEmptySearchState()

	// Fire twice (threshold=3, maxFires=2)
	for i := 0; i < 3; i++ {
		e.recordResult("grep", "No matches found.", false, "TODO")
	}
	for i := 0; i < 3; i++ {
		e.recordResult("grep", "No matches found.", false, "TODO")
	}

	if e.guidanceFired != 2 {
		t.Fatalf("expected guidanceFired=2 (max), got %d", e.guidanceFired)
	}

	// Further empties should NOT produce guidance
	g := e.recordResult("grep", "No matches found.", false, "TODO")
	if g != "" {
		t.Errorf("expected no more guidance after max fires, got: %q", g)
	}
}

func TestEmptySearchState_SevereGuidance(t *testing.T) {
	e := newEmptySearchState()

	// Trigger 6+ consecutive empties to get severe guidance
	for i := 0; i < 6; i++ {
		e.recordResult("grep", "No matches found.", false, "TODO")
	}

	// Severe guidance should suggest fundamentally different approaches
	if e.consecutive < emptySearchThreshold*2 {
		t.Fatalf("expected consecutive >= %d for severe, got %d", emptySearchThreshold*2, e.consecutive)
	}
}

func TestIsEmptyResult_Patterns(t *testing.T) {
	tests := []struct {
		content string
		want    bool
	}{
		{"No matches found.", true},
		{"No files found.", true},
		{"No commits found.", true},
		{"0 matches", true},
		{"", true},
		{"   \n  ", true},
		{"/path/to/file.go:42:func main()", false},
		{"Found 5 matches:\nfile1.go\nfile2.go", false},
		{"commit abc123 - added feature", false},
	}

	for _, tt := range tests {
		got := isEmptyResult(tt.content)
		if got != tt.want {
			t.Errorf("isEmptyResult(%q) = %v, want %v", tt.content, got, tt.want)
		}
	}
}

func TestIsEmptyResult_LongContentNotTreatedAsEmpty(t *testing.T) {
	// Long content that happens to contain "no matches" should not be treated as empty
	long := strings.Repeat("This is some real content. ", 30) + " no matches found in this context"
	if isEmptyResult(long) {
		t.Error("expected long content with embedded pattern to NOT be empty")
	}
}

func TestEmptySearchState_Reset(t *testing.T) {
	e := newEmptySearchState()
	e.consecutive = 5
	e.totalEmpties = 10
	e.guidanceFired = 2
	e.lastTool = "grep"

	e.reset()

	if e.consecutive != 0 || e.totalEmpties != 0 || e.guidanceFired != 0 {
		t.Error("reset did not clear all fields")
	}
}
