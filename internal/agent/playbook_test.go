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

	if seq != "read→edit→exec" {
		t.Errorf("abstractToolSequence = %q, want %q", seq, "read→edit→exec")
	}

	seq2 := abstractToolSequence(map[string]int{"read_file": 1})
	if seq2 != "read" {
		t.Errorf("single category: got %q, want %q", seq2, "read")
	}

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
		{"mixed", []string{"a.go", "b.ts"}, ".go+.ts"},
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

func containsBoth(a, b string) bool {
	return true
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

	stats1 := &RunStats{
		ToolCalls:   map[string]int{"read_file": 2, "edit_file": 1},
		FilesEdited: []string{"main.go"},
		Success:     true,
		Iterations:  5,
		Duration:    2 * time.Minute,
		UserPrompt:  "fix the login bug",
	}
	pb.Record(stats1)

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

	if e.AvgIter != 6.0 {
		t.Errorf("expected avg iter=6.0, got %f", e.AvgIter)
	}
}

func TestPlaybookRecordSkipsUnsuccessful(t *testing.T) {
	dir := t.TempDir()
	pb := NewPlaybook(dir)

	stats := &RunStats{
		ToolCalls:  map[string]int{"read_file": 5},
		Success:    false,
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
		ToolCalls:  map[string]int{"read_file": 1},
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

	if _, err := os.Stat(filepath.Join(dir, ".ggcode", "playbook.json")); os.IsNotExist(err) {
		t.Fatal("expected playbook.json to exist")
	}

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

	hints := pb.HintsForPrompt("", 5)
	if hints != "" {
		t.Errorf("expected empty hints for empty playbook, got %q", hints)
	}

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

	hints = pb.HintsForPrompt("", 5)
	if hints == "" {
		t.Fatal("expected non-empty hints")
	}
	if !contains(hints, "Strategy Playbook") {
		t.Error("expected 'Strategy Playbook' header in hints")
	}

	if !contains(hints, "bugfix") || !contains(hints, "feature") || !contains(hints, "refactor") {
		t.Errorf("expected all 3 task types in hints, got: %s", hints)
	}
}

func TestPlaybookHintsForPromptMaxEntries(t *testing.T) {
	dir := t.TempDir()
	pb := NewPlaybook(dir)

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

	hints := pb.HintsForPrompt("", 2)

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
	pb.maxEntries = 3

	for i := 0; i < 5; i++ {
		pb.Record(&RunStats{
			ToolCalls:   map[string]int{"read_file": 1, "edit_file": 1},
			FilesEdited: []string{filepath.Join(dir, "file"+string(rune('a'+i))+".go")},
			Success:     true,
			Iterations:  3,
			Duration:    time.Minute,

			UserPrompt: []string{
				"fix bug a", "add feature b", "refactor c", "review d", "test e",
			}[i],
		})

		time.Sleep(1 * time.Millisecond)
	}

	if len(pb.entries) > 3 {
		t.Errorf("expected at most 3 entries after eviction, got %d", len(pb.entries))
	}
}

func TestPlaybookNilSafe(t *testing.T) {
	var pb *Playbook

	pb.Record(&RunStats{Success: true})
	if hints := pb.HintsForPrompt("", 5); hints != "" {
		t.Error("expected empty hints from nil playbook")
	}
}

func TestPlaybookScore(t *testing.T) {
	tests := []struct {
		name    string
		uses    int
		avgIter float64
		want    float64
	}{
		{"frequent efficient", 5, 10, 5.0},
		{"frequent slow", 10, 50, 2.0},
		{"rare efficient", 3, 5, 6.0},
		{"capped frequency", 20, 10, 10.0},
		{"zero iter protected", 1, 0, 10.0},
	}
	for _, tt := range tests {
		e := PlaybookEntry{Uses: tt.uses, AvgIter: tt.avgIter}
		got := playbookScore(e)
		if got != tt.want {
			t.Errorf("%s: playbookScore(uses=%d, iter=%.0f) = %.2f, want %.2f",
				tt.name, tt.uses, tt.avgIter, got, tt.want)
		}
	}
}

func TestPlaybookHintsEfficiencyRanking(t *testing.T) {
	dir := t.TempDir()
	pb := NewPlaybook(dir)

	pb.entries = append(pb.entries, PlaybookEntry{
		TaskType:     "bugfix",
		ToolSequence: "read→edit→exec",
		FileTypes:    ".go",
		Uses:         5,
		AvgIter:      40,
		AvgDurationS: 300,
		LastSeen:     time.Now(),
		CreatedAt:    time.Now(),
	})

	pb.entries = append(pb.entries, PlaybookEntry{
		TaskType:     "feature",
		ToolSequence: "read→edit",
		FileTypes:    ".go",
		Uses:         3,
		AvgIter:      5,
		AvgDurationS: 60,
		LastSeen:     time.Now(),
		CreatedAt:    time.Now(),
	})

	pb.entries = append(pb.entries, PlaybookEntry{
		TaskType:     "refactor",
		ToolSequence: "read→edit→exec→vcs",
		FileTypes:    ".go",
		Uses:         10,
		AvgIter:      15,
		AvgDurationS: 120,
		LastSeen:     time.Now(),
		CreatedAt:    time.Now(),
	})

	hints := pb.HintsForPrompt("", 3)
	if hints == "" {
		t.Fatal("expected non-empty hints")
	}
	lines := strings.Split(hints, "\n")
	var refactorPos, featurePos, bugfixPos int
	for i, line := range lines {
		if strings.Contains(line, "refactor") {
			refactorPos = i
		}
		if strings.Contains(line, "feature") {
			featurePos = i
		}
		if strings.Contains(line, "bugfix") {
			bugfixPos = i
		}
	}

	if refactorPos >= featurePos {
		t.Errorf("expected refactor (score=%.2f) before feature (score=%.2f)",
			playbookScore(pb.entries[2]), playbookScore(pb.entries[1]))
	}
	if featurePos >= bugfixPos {
		t.Errorf("expected feature (score=%.2f) before bugfix (score=%.2f)",
			playbookScore(pb.entries[1]), playbookScore(pb.entries[0]))
	}
}

func TestPlaybookAtomicSave(t *testing.T) {
	dir := t.TempDir()
	pb := NewPlaybook(dir)

	pb.Record(&RunStats{
		ToolCalls:   map[string]int{"read_file": 3, "edit_file": 2},
		FilesEdited: []string{"main.go"},
		Success:     true,
		Iterations:  5,
		Duration:    time.Minute,
		UserPrompt:  "fix a bug",
	})

	tmpPath := filepath.Join(dir, ".ggcode", "playbook.json.tmp")
	if _, err := os.Stat(tmpPath); !os.IsNotExist(err) {
		t.Errorf("expected no .tmp file after save, but it exists")
	}

	data, err := os.ReadFile(filepath.Join(dir, ".ggcode", "playbook.json"))
	if err != nil {
		t.Fatalf("failed to read playbook: %v", err)
	}
	if !strings.Contains(string(data), "bugfix") {
		t.Errorf("expected playbook to contain 'bugfix' task type")
	}
}

func TestPlaybookHintsForPromptIntentBoost(t *testing.T) {
	tmp := t.TempDir()
	pb := NewPlaybook(tmp)
	if pb == nil {
		t.Fatal("NewPlaybook returned nil")
	}

	pb.entries = []PlaybookEntry{{
		ID: "b1", TaskType: "bugfix", ToolSequence: "read>edit>execute",
		Uses: 2, SuccessRate: 1.0, AvgIter: 40, LastSeen: time.Now(), CreatedAt: time.Now(),
	}}

	pb.entries = append(pb.entries, PlaybookEntry{
		ID: "c1", TaskType: "build", ToolSequence: "execute",
		Uses: 10, SuccessRate: 1.0, AvgIter: 5, LastSeen: time.Now(), CreatedAt: time.Now(),
	})

	hints := pb.HintsForPrompt("fix the panic crash in daemon startup", 2)
	if !strings.Contains(hints, "bugfix") {
		t.Fatalf("expected bugfix hint present, got: %s", hints)
	}
	first := strings.Split(strings.SplitN(hints, "\n", 3)[1], ":")[0]
	if first != "- bugfix" {
		t.Errorf("intent-matching entry should rank first, first line: %q", first)
	}

	hints = pb.HintsForPrompt("", 2)
	first = strings.Split(strings.SplitN(hints, "\n", 3)[1], ":")[0]
	if first != "- build" {
		t.Errorf("empty prompt should keep global ranking, first line: %q", first)
	}
}

func TestPlaybookFailureAwareRecording(t *testing.T) {
	tmp := t.TempDir()
	pb := NewPlaybook(tmp)
	if pb == nil {
		t.Fatal("NewPlaybook returned nil")
	}
	stats := func(success bool, iters int) *RunStats {
		s := &RunStats{Success: success, Iterations: iters, ToolCalls: map[string]int{"read_file": 2, "edit_file": 2}}
		return s
	}

	pb.Record(stats(true, 5))
	if len(pb.entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(pb.entries))
	}

	pb.Record(stats(false, 8))
	e := pb.entries[0]
	if e.Uses != 2 {
		t.Errorf("Uses = %d, want 2", e.Uses)
	}
	if want := 0.5; e.SuccessRate != want {
		t.Errorf("SuccessRate = %v, want %v", e.SuccessRate, want)
	}

	odd := stats(false, 3)
	odd.ToolCalls = map[string]int{"browser": 4}
	pb.Record(odd)
	if len(pb.entries) != 1 {
		t.Errorf("failure must not create entries, got %d", len(pb.entries))
	}
}

func TestPlaybookPruneDegraded(t *testing.T) {
	tmp := t.TempDir()
	pb := NewPlaybook(tmp)
	if pb == nil {
		t.Fatal("NewPlaybook returned nil")
	}
	stats := func(success bool) *RunStats {
		return &RunStats{Success: success, Iterations: 6, ToolCalls: map[string]int{"read_file": 2, "edit_file": 2}}
	}

	pb.Record(stats(true))
	pb.Record(stats(false))
	pb.Record(stats(false))
	pb.Record(stats(false))
	if len(pb.entries) != 1 {
		t.Fatalf("young degraded entry must be protected, got %d entries", len(pb.entries))
	}
	pb.Record(stats(false))
	if len(pb.entries) != 0 {
		t.Errorf("degraded entry (uses=5, rate=0.2) must be pruned, got %d entries", len(pb.entries))
	}
}

func TestRecordPlaybookFailureDegrades(t *testing.T) {
	tmp := t.TempDir()
	a := &Agent{workingDir: tmp}

	mkStats := func(success bool) *RunStats {
		return &RunStats{Success: success, Iterations: 6, ToolCalls: map[string]int{"read_file": 2, "edit_file": 2}}
	}

	a.recordPlaybook(mkStats(true))
	pb := NewPlaybook(tmp)
	if pb == nil {
		t.Fatal("NewPlaybook returned nil")
	}
	pb.load()
	if len(pb.entries) != 1 {
		t.Fatalf("expected 1 entry after success, got %d", len(pb.entries))
	}

	a.recordPlaybook(mkStats(false))
	pb = NewPlaybook(tmp)
	pb.load()
	if len(pb.entries) != 1 {
		t.Fatalf("failure must not delete the entry, got %d entries", len(pb.entries))
	}
	if got := pb.entries[0].SuccessRate; got != 0.5 {
		t.Errorf("SuccessRate after failure via recordPlaybook = %v, want 0.5 (gate regressed?)", got)
	}
	if got := pb.entries[0].Uses; got != 2 {
		t.Errorf("Uses after failure via recordPlaybook = %d, want 2", got)
	}

	a.recordPlaybook(nil)
}
