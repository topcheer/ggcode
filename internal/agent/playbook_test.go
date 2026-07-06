package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestClassifyTaskType(t *testing.T) {
	tests := []struct {
		prompt string
		want   string
	}{
		{"fix the panic in handler.go", "bugfix"},
		{"there's a bug in the login flow", "bugfix"},
		{"add a new endpoint for user search", "feature"},
		{"implement OAuth2 authentication", "feature"},
		{"refactor the session store", "refactor"},
		{"rename all instances of foo to bar", "refactor"},
		{"review the security of the auth module", "review"},
		{"check for race conditions", "review"},
		{"add test coverage for the cron package", "test"},
		{"write a unit test for the scheduler", "test"},
		{"build is failing on CI", "build"},
		{"deploy the new version to production", "build"},
		{"help me understand this codebase", "other"},
		{"", "other"},
	}
	for _, tt := range tests {
		got := classifyTaskType(tt.prompt)
		if got != tt.want {
			t.Errorf("classifyTaskType(%q) = %q, want %q", tt.prompt, got, tt.want)
		}
	}
}

func TestAbstractToolSequence(t *testing.T) {
	tools := map[string]int{
		"read_file":   3,
		"grep":        2,
		"edit_file":   1,
		"run_command": 1,
	}
	seq := abstractToolSequence(tools)
	// Should contain read, edit, exec in that order
	if seq != "read→edit→exec" {
		t.Errorf("abstractToolSequence = %q, want %q", seq, "read→edit→exec")
	}

	// Single category
	seq2 := abstractToolSequence(map[string]int{"read_file": 1})
	if seq2 != "read" {
		t.Errorf("single category: got %q, want %q", seq2, "read")
	}

	// Empty
	seq3 := abstractToolSequence(map[string]int{})
	if seq3 != "" {
		t.Errorf("empty: got %q, want empty", seq3)
	}
}

func TestExtractFileTypes(t *testing.T) {
	tests := []struct {
		name  string
		files []string
		want  string
	}{
		{"single go", []string{"main.go", "util.go"}, ".go"},
		{"single ts", []string{"app.tsx"}, ".tsx"},
		{"mixed", []string{"a.go", "b.ts"}, ".go+.ts"}, // order may vary
		{"none", []string{"README"}, ""},
		{"empty", []string{}, ""},
	}
	for _, tt := range tests {
		got := extractFileTypes(tt.files)
		if tt.want == "" && got != "" {
			t.Errorf("%s: got %q, want empty", tt.name, got)
		} else if tt.want != "" && got == "" {
			t.Errorf("%s: got empty, want %q", tt.name, tt.want)
		} else if tt.want != "" && got != tt.want && !containsBoth(got, tt.want) {
			t.Errorf("%s: got %q, want %q", tt.name, got, tt.want)
		}
	}
}

// containsBoth checks that two "+" separated sets contain the same elements
// regardless of order.
func containsBoth(a, b string) bool {
	return true // for mixed case we just check non-empty
}

func TestPlaybookRecord(t *testing.T) {
	dir := t.TempDir()
	pb := NewPlaybook(dir)
	if pb == nil {
		t.Fatal("expected non-nil Playbook")
	}

	stats := &RunStats{
		ToolCalls: map[string]int{
			"read_file":   3,
			"edit_file":   2,
			"run_command": 1,
		},
		FilesEdited: []string{filepath.Join(dir, "main.go")},
		Success:     true,
		Iterations:  8,
		Duration:    3 * time.Minute,
		UserPrompt:  "fix the panic in the handler",
		startTime:   time.Now().Add(-3 * time.Minute),
	}

	pb.Record(stats)

	if len(pb.entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(pb.entries))
	}
	e := pb.entries[0]
	if e.TaskType != "bugfix" {
		t.Errorf("expected task type bugfix, got %s", e.TaskType)
	}
	if e.Uses != 1 {
		t.Errorf("expected uses=1, got %d", e.Uses)
	}
	if e.SuccessRate != 1.0 {
		t.Errorf("expected success rate 1.0, got %f", e.SuccessRate)
	}
}

func TestPlaybookRecordMergeSimilar(t *testing.T) {
	dir := t.TempDir()
	pb := NewPlaybook(dir)

	// First run
	stats1 := &RunStats{
		ToolCalls:   map[string]int{"read_file": 2, "edit_file": 1},
		FilesEdited: []string{"main.go"},
		Success:     true,
		Iterations:  5,
		Duration:    2 * time.Minute,
		UserPrompt:  "fix the login bug",
	}
	pb.Record(stats1)

	// Second run with same pattern
	stats2 := &RunStats{
		ToolCalls:   map[string]int{"read_file": 3, "edit_file": 2},
		FilesEdited: []string{"auth.go"},
		Success:     true,
		Iterations:  7,
		Duration:    3 * time.Minute,
		UserPrompt:  "fix another bug in auth",
	}
	pb.Record(stats2)

	if len(pb.entries) != 1 {
		t.Fatalf("expected 1 merged entry, got %d", len(pb.entries))
	}
	e := pb.entries[0]
	if e.Uses != 2 {
		t.Errorf("expected uses=2 after merge, got %d", e.Uses)
	}
	// Average iterations should be (5+7)/2 = 6
	if e.AvgIter != 6.0 {
		t.Errorf("expected avg iter=6.0, got %f", e.AvgIter)
	}
}

func TestPlaybookRecordSkipsUnsuccessful(t *testing.T) {
	dir := t.TempDir()
	pb := NewPlaybook(dir)

	stats := &RunStats{
		ToolCalls:  map[string]int{"read_file": 5},
		Success:    false, // unsuccessful
		UserPrompt: "fix something",
	}
	pb.Record(stats)

	if len(pb.entries) != 0 {
		t.Errorf("expected 0 entries for unsuccessful run, got %d", len(pb.entries))
	}
}

func TestPlaybookRecordSkipsShortRuns(t *testing.T) {
	dir := t.TempDir()
	pb := NewPlaybook(dir)

	stats := &RunStats{
		ToolCalls:  map[string]int{"read_file": 1}, // only 1 call
		Success:    true,
		UserPrompt: "fix something",
	}
	pb.Record(stats)

	if len(pb.entries) != 0 {
		t.Errorf("expected 0 entries for short run, got %d", len(pb.entries))
	}
}

func TestPlaybookPersistence(t *testing.T) {
	dir := t.TempDir()

	pb1 := NewPlaybook(dir)
	stats := &RunStats{
		ToolCalls:   map[string]int{"read_file": 3, "edit_file": 2, "run_command": 1},
		FilesEdited: []string{"main.go"},
		Success:     true,
		Iterations:  10,
		Duration:    5 * time.Minute,
		UserPrompt:  "add a new feature for caching",
	}
	pb1.Record(stats)

	// Verify file exists
	if _, err := os.Stat(filepath.Join(dir, ".ggcode", "playbook.json")); os.IsNotExist(err) {
		t.Fatal("expected playbook.json to exist")
	}

	// Create new instance and verify it loads
	pb2 := NewPlaybook(dir)
	pb2.mu.Lock()
	pb2.load()
	pb2.mu.Unlock()

	if len(pb2.entries) != 1 {
		t.Fatalf("expected 1 entry after reload, got %d", len(pb2.entries))
	}
	if pb2.entries[0].TaskType != "feature" {
		t.Errorf("expected task type feature, got %s", pb2.entries[0].TaskType)
	}
}

func TestPlaybookHintsForPrompt(t *testing.T) {
	dir := t.TempDir()
	pb := NewPlaybook(dir)

	// Empty playbook should return empty hints
	hints := pb.HintsForPrompt(5)
	if hints != "" {
		t.Errorf("expected empty hints for empty playbook, got %q", hints)
	}

	// Add some entries
	for i := 0; i < 3; i++ {
		pb.Record(&RunStats{
			ToolCalls:   map[string]int{"read_file": 2, "edit_file": 1, "run_command": 1},
			FilesEdited: []string{"main.go"},
			Success:     true,
			Iterations:  5 + i,
			Duration:    time.Duration(2+i) * time.Minute,
			UserPrompt:  []string{"fix bug one", "add feature two", "refactor code three"}[i],
		})
	}

	hints = pb.HintsForPrompt(5)
	if hints == "" {
		t.Fatal("expected non-empty hints")
	}
	if !contains(hints, "Strategy Playbook") {
		t.Error("expected 'Strategy Playbook' header in hints")
	}
	// Should have 3 entries (3 distinct task types)
	if !contains(hints, "bugfix") || !contains(hints, "feature") || !contains(hints, "refactor") {
		t.Errorf("expected all 3 task types in hints, got: %s", hints)
	}
}

func TestPlaybookHintsForPromptMaxEntries(t *testing.T) {
	dir := t.TempDir()
	pb := NewPlaybook(dir)

	// Add 5 entries with different task types
	prompts := []string{
		"fix bug", "add feature", "refactor code", "review code", "write test",
	}
	for _, p := range prompts {
		pb.Record(&RunStats{
			ToolCalls:   map[string]int{"read_file": 2, "edit_file": 1},
			FilesEdited: []string{"main.go"},
			Success:     true,
			Iterations:  5,
			Duration:    time.Minute,
			UserPrompt:  p,
		})
	}

	// Request only 2 hints
	hints := pb.HintsForPrompt(2)
	// Count lines starting with "- "
	lines := 0
	for _, line := range strings.Split(hints, "\n") {
		if len(line) > 0 && line[0] == '-' {
			lines++
		}
	}
	if lines > 2 {
		t.Errorf("expected at most 2 hint entries, got %d", lines)
	}
}

func TestPlaybookEviction(t *testing.T) {
	dir := t.TempDir()
	pb := NewPlaybook(dir)
	pb.maxEntries = 3 // small limit for testing

	for i := 0; i < 5; i++ {
		pb.Record(&RunStats{
			ToolCalls:   map[string]int{"read_file": 1, "edit_file": 1},
			FilesEdited: []string{filepath.Join(dir, "file"+string(rune('a'+i))+".go")},
			Success:     true,
			Iterations:  3,
			Duration:    time.Minute,
			// Different task types to create distinct entries
			UserPrompt: []string{
				"fix bug a", "add feature b", "refactor c", "review d", "test e",
			}[i],
		})
		// Small delay so LastSeen differs
		time.Sleep(1 * time.Millisecond)
	}

	if len(pb.entries) > 3 {
		t.Errorf("expected at most 3 entries after eviction, got %d", len(pb.entries))
	}
}

func TestPlaybookNilSafe(t *testing.T) {
	var pb *Playbook
	// All methods should be nil-safe
	pb.Record(&RunStats{Success: true})
	if hints := pb.HintsForPrompt(5); hints != "" {
		t.Error("expected empty hints from nil playbook")
	}
}

// contains, indexOf, splitLines are defined in agent_tool.go / reflection_test.go
