package harness

import (
	"os"
	"strings"
	"testing"
	"time"
)

// --- Pure function tests ---

func TestSplitCommaInput(t *testing.T) {
	tests := []struct {
		input string
		want  []string
	}{
		{"a,b,c", []string{"a", "b", "c"}},
		{"  a , b , c  ", []string{"a", "b", "c"}},
		{"", nil},
		{"single", []string{"single"}},
		{",,", nil},
		{"a,,b", []string{"a", "b"}},
	}
	for _, tt := range tests {
		got := SplitCommaInput(tt.input)
		if len(got) != len(tt.want) {
			t.Errorf("SplitCommaInput(%q) = %v, want %v", tt.input, got, tt.want)
			continue
		}
		for i := range got {
			if got[i] != tt.want[i] {
				t.Errorf("SplitCommaInput(%q)[%d] = %q, want %q", tt.input, i, got[i], tt.want[i])
			}
		}
	}
}

func TestParseContextSpecs(t *testing.T) {
	specs := ParseContextSpecs("frontend:ui,backend:api")
	if len(specs) != 2 {
		t.Fatalf("expected 2 specs, got %d", len(specs))
	}
	// NormalizeContexts sorts by path, so "api" < "ui"
	if specs[0].Name != "backend" || specs[0].Path != "api" {
		t.Errorf("spec[0] = %+v", specs[0])
	}
	if specs[1].Name != "frontend" || specs[1].Path != "ui" {
		t.Errorf("spec[1] = %+v", specs[1])
	}

	// name-only spec
	specs2 := ParseContextSpecs("mypackage")
	if len(specs2) != 1 || specs2[0].Name != "mypackage" || specs2[0].Path != "" {
		t.Errorf("name-only spec = %+v", specs2)
	}

	// empty
	specs3 := ParseContextSpecs("")
	if len(specs3) != 0 {
		t.Errorf("expected 0 specs for empty input, got %d", len(specs3))
	}
}

func TestIndentText(t *testing.T) {
	got := indentText("hello\nworld", "  ")
	want := "  hello\n  world"
	if got != want {
		t.Errorf("indentText() = %q, want %q", got, want)
	}
	if indentText("", "  ") != "" {
		t.Error("expected empty output for empty input")
	}
	if indentText("single", ">>") != ">>single" {
		t.Error("expected single line to be indented")
	}
}

func TestTruncateHarnessFailureText(t *testing.T) {
	if truncateHarnessFailureText("short", 100) != "short" {
		t.Error("expected short text unchanged")
	}
	long := strings.Repeat("x", 300)
	got := truncateHarnessFailureText(long, 100)
	if len(got) != 100 {
		t.Errorf("expected length 100, got %d", len(got))
	}
	if !strings.HasSuffix(got, "...") {
		t.Error("expected ... suffix")
	}
	// edge: maxLen < 4
	if truncateHarnessFailureText("hello", 3) != "hel" {
		t.Error("expected truncation without ellipsis when maxLen < 4")
	}
}

func TestTruncatePromotionMessage(t *testing.T) {
	if truncatePromotionMessage("short goal") != "short goal" {
		t.Error("expected short goal unchanged")
	}
	long := strings.Repeat("a", 100)
	got := truncatePromotionMessage(long)
	if len(got) > 72 {
		t.Errorf("expected <= 72 chars, got %d", len(got))
	}
	if truncatePromotionMessage("  ") != "" {
		t.Error("expected empty for whitespace-only")
	}
}

func TestFirstNonEmptyText(t *testing.T) {
	if firstNonEmptyText("", "", "hello") != "hello" {
		t.Error("expected first non-empty value")
	}
	if firstNonEmptyText("", "world", "hello") != "world" {
		t.Error("expected first non-empty value")
	}
	if firstNonEmptyText("", "  ", "") != "" {
		t.Error("expected empty when all empty")
	}
}

func TestFirstNonEmptyHarnessFailure(t *testing.T) {
	if firstNonEmptyHarnessFailure("", "fallback") != "fallback" {
		t.Error("expected fallback")
	}
	if firstNonEmptyHarnessFailure("first", "second") != "first" {
		t.Error("expected first non-empty")
	}
}

func TestParseConfigDuration(t *testing.T) {
	if parseConfigDuration("", time.Hour) != time.Hour {
		t.Error("expected default for empty string")
	}
	if parseConfigDuration("30m", time.Hour) != 30*time.Minute {
		t.Error("expected 30m")
	}
	if parseConfigDuration("2h", 0) != 2*time.Hour {
		t.Error("expected 2h")
	}
	if parseConfigDuration("invalid", 5*time.Minute) != 5*time.Minute {
		t.Error("expected fallback for invalid")
	}
}

func TestNormalizeContextPath(t *testing.T) {
	if normalizeContextPath("  foo/bar  ") != "foo/bar" {
		t.Error("expected trimmed and cleaned path")
	}
	if normalizeContextPath(".") != "" {
		t.Error("expected empty for dot")
	}
	if normalizeContextPath("") != "" {
		t.Error("expected empty for empty")
	}
}

func TestNormalizeContexts(t *testing.T) {
	input := []ContextConfig{
		{Name: "alpha", Path: "pkg/a"},
		{Name: "beta", Path: "pkg/b", Owner: "team-b", Commands: []CommandCheck{{Name: "test", Run: "go test"}}},
		{Name: "alpha", Path: "pkg/a"}, // duplicate
		{Name: "", Path: ""},           // empty, skipped
	}
	result := NormalizeContexts(input)
	if len(result) != 2 {
		t.Fatalf("expected 2 after dedup, got %d", len(result))
	}
	if result[1].Owner != "team-b" {
		t.Error("expected owner preserved")
	}
	if len(result[1].Commands) != 1 {
		t.Error("expected commands preserved")
	}
}

func TestResolveContext(t *testing.T) {
	cfg := &Config{
		Contexts: []ContextConfig{
			{Name: "frontend", Path: "web"},
			{Name: "Backend", Path: "api"},
		},
	}

	// by name
	c, err := ResolveContext(cfg, "frontend")
	if err != nil || c == nil || c.Name != "frontend" {
		t.Errorf("ResolveContext(frontend) = %+v, err=%v", c, err)
	}

	// by path
	c, err = ResolveContext(cfg, "api")
	if err != nil || c == nil || c.Name != "Backend" {
		t.Errorf("ResolveContext(api) = %+v, err=%v", c, err)
	}

	// case-insensitive name
	c, err = ResolveContext(cfg, "BACKEND")
	if err != nil || c == nil {
		t.Errorf("ResolveContext(BACKEND) = %+v, err=%v", c, err)
	}

	// unknown
	_, err = ResolveContext(cfg, "unknown")
	if err == nil {
		t.Error("expected error for unknown context")
	}

	// empty
	c, err = ResolveContext(cfg, "")
	if err != nil || c != nil {
		t.Error("expected nil for empty input")
	}

	// nil config
	_, err = ResolveContext(nil, "frontend")
	if err == nil {
		t.Error("expected error for nil config")
	}
}

func TestResolveTaskContext(t *testing.T) {
	cfg := &Config{
		Contexts: []ContextConfig{
			{Name: "web", Path: "web"},
		},
	}

	task := &Task{ContextName: "web"}
	c := ResolveTaskContext(cfg, task)
	if c == nil || c.Name != "web" {
		t.Error("expected context resolved by name")
	}

	if ResolveTaskContext(nil, task) != nil {
		t.Error("expected nil for nil config")
	}
	if ResolveTaskContext(cfg, nil) != nil {
		t.Error("expected nil for nil task")
	}
}

func TestContextMatches(t *testing.T) {
	cfg := ContextConfig{Name: "Frontend", Path: "web"}
	if !contextMatches(cfg, "frontend") {
		t.Error("expected case-insensitive name match")
	}
	if !contextMatches(cfg, "web") {
		t.Error("expected path match")
	}
	if contextMatches(cfg, "backend") {
		t.Error("expected no match")
	}
	if !contextMatches(cfg, "") {
		t.Error("expected match for empty filter")
	}
}

func TestShouldUseWorkerExecution(t *testing.T) {
	tests := []struct {
		mode string
		want bool
	}{
		{"", true},
		{"subagent", true},
		{"worker", true},
		{"direct", false},
		{"Direct", false},
		{"DIRECT", false},
		{"unknown", true},
	}
	for _, tt := range tests {
		cfg := &Config{}
		cfg.Run.ExecutionMode = tt.mode
		got := shouldUseWorkerExecution(cfg)
		if got != tt.want {
			t.Errorf("shouldUseWorkerExecution(%q) = %v, want %v", tt.mode, got, tt.want)
		}
	}
	if !shouldUseWorkerExecution(nil) {
		t.Error("expected true for nil config")
	}
}

func TestClassifyTaskEvent(t *testing.T) {
	task1 := &Task{ID: "t-1", Status: TaskQueued}
	task2 := &Task{ID: "t-1", Status: TaskRunning}

	if classifyTaskEvent(nil, task1) != eventTaskCreated {
		t.Error("expected task.created for nil previous")
	}
	if classifyTaskEvent(task1, task2) != eventTaskStatusChanged {
		t.Error("expected task.status_changed")
	}

	// worker change
	task3 := &Task{ID: "t-1", Status: TaskRunning, WorkerID: "w-1"}
	task4 := &Task{ID: "t-1", Status: TaskRunning, WorkerID: "w-1", WorkerPhase: "tool"}
	if classifyTaskEvent(task3, task4) != eventTaskWorkerChanged {
		t.Error("expected task.worker_changed")
	}

	// review change
	task5 := &Task{ID: "t-1", Status: TaskCompleted, ReviewStatus: ReviewPending}
	task6 := &Task{ID: "t-1", Status: TaskCompleted, ReviewStatus: ReviewApproved}
	if classifyTaskEvent(task5, task6) != eventTaskReviewChanged {
		t.Error("expected task.review_changed")
	}

	// promotion change
	task7 := &Task{ID: "t-1", Status: TaskCompleted, PromotionStatus: ""}
	task8 := &Task{ID: "t-1", Status: TaskCompleted, PromotionStatus: PromotionApplied}
	if classifyTaskEvent(task7, task8) != eventTaskPromotionChanged {
		t.Error("expected task.promotion_changed")
	}

	// generic update
	task9 := &Task{ID: "t-1", Status: TaskRunning, Goal: "a"}
	task10 := &Task{ID: "t-1", Status: TaskRunning, Goal: "b"}
	if classifyTaskEvent(task9, task10) != eventTaskUpdated {
		t.Error("expected task.updated for generic change")
	}
}

func TestSummarizeTaskEvent(t *testing.T) {
	task := &Task{ID: "t-1", Status: TaskQueued}
	s := summarizeTaskEvent(nil, task)
	if !strings.Contains(s, "t-1") || !strings.Contains(s, "created") {
		t.Errorf("unexpected summary: %s", s)
	}

	prev := &Task{ID: "t-1", Status: TaskQueued}
	curr := &Task{ID: "t-1", Status: TaskRunning}
	s = summarizeTaskEvent(prev, curr)
	if !strings.Contains(s, "status") {
		t.Errorf("expected status in summary: %s", s)
	}
}

func TestClassifyReleaseEvent(t *testing.T) {
	plan := &ReleasePlan{BatchID: "b-1"}
	if classifyReleaseEvent(nil, plan) != eventReleasePersisted {
		t.Error("expected release.persisted for no rollout")
	}

	plan.RolloutID = "r-1"
	if classifyReleaseEvent(nil, plan) != eventRolloutWavePersisted {
		t.Error("expected rollout.wave_persisted")
	}

	prev := &ReleasePlan{BatchID: "b-1", RolloutID: "r-1", WaveStatus: ReleaseWaveActive}
	curr := &ReleasePlan{BatchID: "b-1", RolloutID: "r-1", WaveStatus: ReleaseWavePaused}
	if classifyReleaseEvent(prev, curr) != eventRolloutWaveStatus {
		t.Error("expected rollout.wave_status_changed")
	}

	prev2 := &ReleasePlan{BatchID: "b-1", RolloutID: "r-1", GateStatus: ReleaseGatePending}
	curr2 := &ReleasePlan{BatchID: "b-1", RolloutID: "r-1", GateStatus: ReleaseGateApproved}
	if classifyReleaseEvent(prev2, curr2) != eventRolloutWaveGateStatus {
		t.Error("expected rollout.gate_changed")
	}
}

func TestTimesEqual(t *testing.T) {
	if !timesEqual(nil, nil) {
		t.Error("expected nil == nil")
	}
	now := time.Now()
	if timesEqual(&now, nil) {
		t.Error("expected non-nil != nil")
	}
	later := now.Add(time.Second)
	if timesEqual(&now, &later) {
		t.Error("expected different times not equal")
	}
	same := now
	if !timesEqual(&now, &same) {
		t.Error("expected same time equal")
	}
}

func TestNullableText(t *testing.T) {
	if nullableText("") != nil {
		t.Error("expected nil for empty")
	}
	if nullableText("  ") != nil {
		t.Error("expected nil for whitespace")
	}
	if nullableText("hello") != "hello" {
		t.Error("expected string for non-empty")
	}
}

func TestNullableTime(t *testing.T) {
	if nullableTime(nil) != nil {
		t.Error("expected nil for nil time")
	}
	now := time.Now()
	result := nullableTime(&now)
	if result == nil {
		t.Error("expected non-nil for valid time")
	}
}

func TestMarshalSnapshotJSON(t *testing.T) {
	if marshalSnapshotJSON(nil) != nil {
		t.Error("expected nil for nil")
	}
	// empty slice marshals to "[]", which is not "null", so returns "[]"
	if marshalSnapshotJSON([]string{}) == nil {
		t.Error("expected non-nil for empty slice (marshals to [])")
	}
	result := marshalSnapshotJSON([]string{"a", "b"})
	if result == nil {
		t.Error("expected non-nil for non-empty slice")
	}
}

func TestTaskStatusValue(t *testing.T) {
	if taskStatusValue(nil) != "" {
		t.Error("expected empty for nil")
	}
	if taskStatusValue(&Task{Status: TaskRunning}) != string(TaskRunning) {
		t.Error("expected running status string")
	}
}

func TestReleaseWaveStatusValue(t *testing.T) {
	if releaseWaveStatusValue(nil) != "" {
		t.Error("expected empty for nil")
	}
	plan := &ReleasePlan{WaveStatus: ReleaseWaveActive}
	if releaseWaveStatusValue(plan) != string(ReleaseWaveActive) {
		t.Error("expected active status string")
	}
}

func TestReleaseGateStatusValue(t *testing.T) {
	if releaseGateStatusValue(nil) != "" {
		t.Error("expected empty for nil")
	}
	plan := &ReleasePlan{GateStatus: ReleaseGateApproved}
	if releaseGateStatusValue(plan) != string(ReleaseGateApproved) {
		t.Error("expected approved status string")
	}
}

func TestIsPromotionIgnoredPath(t *testing.T) {
	if !isPromotionIgnoredPath(".ggcode/todos.json") {
		t.Error("expected ignored")
	}
	if isPromotionIgnoredPath("main.go") {
		t.Error("expected not ignored")
	}
}

func TestStaleBlockedTasks(t *testing.T) {
	now := time.Now()
	tasks := []*Task{
		{ID: "t-1", Status: TaskBlocked, UpdatedAt: now.Add(-2 * time.Hour)},
		{ID: "t-2", Status: TaskBlocked, UpdatedAt: now.Add(-30 * time.Minute)},
		{ID: "t-3", Status: TaskRunning, UpdatedAt: now.Add(-2 * time.Hour)},
		{ID: "t-4", Status: TaskBlocked, UpdatedAt: now.Add(-48 * time.Hour)},
	}

	stale := staleBlockedTasks(tasks, time.Hour, now)
	if len(stale) != 2 {
		t.Fatalf("expected 2 stale blocked tasks, got %d", len(stale))
	}
	if stale[0].ID != "t-1" || stale[1].ID != "t-4" {
		t.Errorf("stale IDs = %s, %s", stale[0].ID, stale[1].ID)
	}

	// zero threshold
	if len(staleBlockedTasks(tasks, 0, now)) != 0 {
		t.Error("expected no stale tasks with zero threshold")
	}
}

func TestWorkerDriftTasks(t *testing.T) {
	tasks := []*Task{
		{ID: "t-1", Status: TaskRunning, WorkerID: "w-1", WorkerStatus: "running"},
		{ID: "t-2", Status: TaskRunning, WorkerID: "", WorkerStatus: ""},
		{ID: "t-3", Status: TaskRunning, WorkerID: "w-3", WorkerStatus: "completed"},
		{ID: "t-4", Status: TaskRunning, WorkerID: "w-4", WorkerStatus: "failed"},
		{ID: "t-5", Status: TaskCompleted, WorkerID: "", WorkerStatus: ""},
	}

	drift := workerDriftTasks(tasks)
	if len(drift) != 3 {
		t.Fatalf("expected 3 drift tasks, got %d", len(drift))
	}
}

func TestTaskRetryable(t *testing.T) {
	cfg := &Config{}
	cfg.Run.MaxAttempts = 3

	if !taskRetryable(&Task{Status: TaskFailed, Attempt: 1}, cfg) {
		t.Error("expected retryable")
	}
	if taskRetryable(&Task{Status: TaskFailed, Attempt: 3}, cfg) {
		t.Error("expected not retryable (max attempts)")
	}
	if taskRetryable(&Task{Status: TaskRunning, Attempt: 1}, cfg) {
		t.Error("expected not retryable (running)")
	}
	if taskRetryable(nil, cfg) {
		t.Error("expected not retryable (nil)")
	}
}

func TestTaskReviewReady(t *testing.T) {
	if !taskReviewReady(&Task{Status: TaskCompleted, VerificationStatus: VerificationPassed, ReviewStatus: ReviewPending}) {
		t.Error("expected review ready")
	}
	if taskReviewReady(&Task{Status: TaskCompleted, VerificationStatus: VerificationPassed, ReviewStatus: ReviewApproved}) {
		t.Error("expected not ready (already approved)")
	}
	if taskReviewReady(&Task{Status: TaskCompleted, VerificationStatus: VerificationFailed, ReviewStatus: ReviewPending}) {
		t.Error("expected not ready (verification failed)")
	}
	if taskReviewReady(&Task{Status: TaskRunning, VerificationStatus: VerificationPassed, ReviewStatus: ReviewPending}) {
		t.Error("expected not ready (still running)")
	}
	if taskReviewReady(nil) {
		t.Error("expected not ready (nil)")
	}
}

func TestTaskPromotionReady(t *testing.T) {
	if !taskPromotionReady(&Task{Status: TaskCompleted, ReviewStatus: ReviewApproved, PromotionStatus: ""}) {
		t.Error("expected promotion ready")
	}
	if taskPromotionReady(&Task{Status: TaskCompleted, ReviewStatus: ReviewApproved, PromotionStatus: PromotionApplied}) {
		t.Error("expected not ready (already promoted)")
	}
	if taskPromotionReady(&Task{Status: TaskCompleted, ReviewStatus: ReviewPending, PromotionStatus: ""}) {
		t.Error("expected not ready (review not approved)")
	}
	if taskPromotionReady(nil) {
		t.Error("expected not ready (nil)")
	}
}

func TestSummarizeDirtyPaths(t *testing.T) {
	if summarizeDirtyPaths(nil, 8) != "" {
		t.Error("expected empty for nil paths")
	}
	if summarizeDirtyPaths([]string{"a.txt"}, 8) != "a.txt" {
		t.Error("expected single path")
	}
	paths := []string{"a", "b", "c", "d", "e"}
	got := summarizeDirtyPaths(paths, 3)
	if !strings.Contains(got, "+2 more") {
		t.Errorf("expected truncation hint, got: %s", got)
	}
}

func TestShouldIgnoreWorktreeDirtyPath(t *testing.T) {
	// StateRelDir = ".ggcode/harness" — paths under it are ignored
	if !shouldIgnoreWorktreeDirtyPath(".ggcode/harness/state.db", nil) {
		t.Error("expected .ggcode/harness path ignored")
	}
	if !shouldIgnoreWorktreeDirtyPath(".ggcode/todos.json", nil) {
		t.Error("expected .ggcode/todos.json ignored")
	}
	if !shouldIgnoreWorktreeDirtyPath("node_modules/foo.js", []string{"node_modules"}) {
		t.Error("expected shared runtime dir ignored")
	}
	if shouldIgnoreWorktreeDirtyPath("main.go", nil) {
		t.Error("expected main.go not ignored")
	}
	// empty path is ignored
	if !shouldIgnoreWorktreeDirtyPath("", nil) {
		t.Error("expected empty path ignored")
	}
}

func TestBuildWorktreeCheckpointMessage(t *testing.T) {
	msg := buildWorktreeCheckpointMessage([]string{"a.go", "b.go"})
	if !strings.Contains(msg, "checkpoint workspace") {
		t.Errorf("unexpected message: %s", msg)
	}

	// long message truncates to base
	longPaths := make([]string, 50)
	for i := range longPaths {
		longPaths[i] = strings.Repeat("x", 30)
	}
	msg = buildWorktreeCheckpointMessage(longPaths)
	if !strings.HasPrefix(msg, "chore: checkpoint workspace") {
		t.Errorf("expected base message when too long: %s", msg)
	}
}

func TestBuildRunPrompt(t *testing.T) {
	// nil inputs
	if BuildRunPrompt(nil, nil) != "" {
		t.Error("expected empty for nil inputs")
	}

	cfg := DefaultConfig("test-project", "Build the thing")
	cfg.Run.PromptPreamble = "Be careful."
	task, _ := NewTask("Implement feature X", "cli")

	prompt := BuildRunPrompt(cfg, task)
	if !strings.Contains(prompt, "Harness execution context") {
		t.Error("expected harness context header")
	}
	if !strings.Contains(prompt, "Be careful.") {
		t.Error("expected prompt preamble")
	}
	if !strings.Contains(prompt, "Implement feature X") {
		t.Error("expected task goal")
	}
	if !strings.Contains(prompt, "Build the thing") {
		t.Error("expected project goal")
	}
}

func TestBuildRunPromptWithContext(t *testing.T) {
	cfg := DefaultConfig("test-project", "Goal")
	cfg.Contexts = []ContextConfig{
		{Name: "api", Path: "internal/api", Owner: "backend-team",
			Commands: []CommandCheck{{Name: "test", Run: "go test ./...", Optional: true}}},
	}
	task, _ := NewTask("Fix API bug", "cli")
	task.ContextName = "api"

	prompt := BuildRunPrompt(cfg, task)
	if !strings.Contains(prompt, "backend-team") {
		t.Error("expected owner in prompt")
	}
	if !strings.Contains(prompt, "go test ./...") {
		t.Error("expected context validation command in prompt")
	}
}

func TestFormatCheckReport(t *testing.T) {
	if FormatCheckReport(nil) != "No harness check report." {
		t.Error("expected nil message")
	}

	report := &CheckReport{Passed: true}
	got := FormatCheckReport(report)
	if !strings.Contains(got, "passed") {
		t.Errorf("expected passed in output: %s", got)
	}

	report2 := &CheckReport{
		Passed: false,
		Issues: []CheckIssue{
			{Level: "error", Kind: "missing-file", Path: "AGENTS.md", Message: "missing", Fix: "create it"},
		},
		Commands: []CommandResult{
			{Name: "build", Scope: "root", Command: "go build", Success: true, Output: "ok"},
			{Name: "test", Scope: "root", Command: "go test", Success: false, Output: "FAIL"},
		},
	}
	got = FormatCheckReport(report2)
	if !strings.Contains(got, "failed") {
		t.Error("expected failed status")
	}
	if !strings.Contains(got, "missing-file") {
		t.Error("expected issue kind")
	}
	if !strings.Contains(got, "go build") {
		t.Error("expected command info")
	}
}

func TestFormatGCReport(t *testing.T) {
	if FormatGCReport(nil) != "No harness gc report." {
		t.Error("expected nil message")
	}
	report := &GCReport{ArchivedTasks: 3, AbandonedTasks: 1, DeletedLogs: 5, RemovedWorktrees: 2}
	got := FormatGCReport(report)
	if !strings.Contains(got, "archived tasks: 3") {
		t.Errorf("expected archived count: %s", got)
	}
}

func TestFormatRunSummary(t *testing.T) {
	if FormatRunSummary(nil) != "No harness run executed." {
		t.Error("expected nil message")
	}
	summary := &RunSummary{
		Task: &Task{
			ID: "t-1", Status: TaskCompleted, Goal: "done",
			VerificationStatus: VerificationPassed, ReviewStatus: ReviewPending,
			ChangedFiles:           []string{"main.go"},
			VerificationReportPath: "/tmp/report.json",
			LogPath:                "/tmp/t-1.log",
		},
		Result: &RunResult{Output: "all good"},
	}
	got := FormatRunSummary(summary)
	if !strings.Contains(got, "completed") {
		t.Errorf("expected completed: %s", got)
	}
	if !strings.Contains(got, "main.go") {
		t.Error("expected changed file")
	}
	if !strings.Contains(got, "all good") {
		t.Error("expected output")
	}
}

func TestFormatQueueSummary(t *testing.T) {
	if FormatQueueSummary(nil) != "No queued harness tasks were executed." {
		t.Error("expected nil message")
	}
	summary := &RunQueueSummary{
		Executed: []*RunSummary{
			{Task: &Task{ID: "t-1", Status: TaskCompleted, Goal: "first task"}},
			{Task: &Task{ID: "t-2", Status: TaskFailed, Goal: "second task", Attempt: 2, Error: "timeout"}},
		},
	}
	got := FormatQueueSummary(summary)
	if !strings.Contains(got, "t-1") || !strings.Contains(got, "t-2") {
		t.Errorf("expected both tasks: %s", got)
	}
	if !strings.Contains(got, "attempt 2") {
		t.Error("expected attempt info")
	}
	if !strings.Contains(got, "timeout") {
		t.Error("expected error info")
	}
}

func TestFormatReviewList(t *testing.T) {
	if FormatReviewList(nil) != "No harness tasks are waiting for review." {
		t.Error("expected empty message")
	}
	if FormatReviewList([]*Task{}) != "No harness tasks are waiting for review." {
		t.Error("expected empty message for empty list")
	}
	tasks := []*Task{
		{ID: "t-1", Goal: "fix bug", BranchName: "harness-t-1", ChangedFiles: []string{"main.go"}},
	}
	got := FormatReviewList(tasks)
	if !strings.Contains(got, "review queue") || !strings.Contains(got, "t-1") {
		t.Errorf("unexpected: %s", got)
	}
}

func TestFormatPromotionList(t *testing.T) {
	if FormatPromotionList(nil) != "No harness tasks are ready for promotion." {
		t.Error("expected empty message")
	}
	tasks := []*Task{
		{ID: "t-1", Goal: "fix bug", BranchName: "harness-t-1"},
	}
	got := FormatPromotionList(tasks)
	if !strings.Contains(got, "promotion queue") {
		t.Errorf("unexpected: %s", got)
	}
}

func TestFormatDoctorReport(t *testing.T) {
	if FormatDoctorReport(nil) != "No harness doctor report." {
		t.Error("expected nil message")
	}
	report := &DoctorReport{
		Project:    Project{RootDir: "/tmp/test"},
		TotalTasks: 10,
		Structural: &CheckReport{Passed: true},
	}
	got := FormatDoctorReport(report)
	if !strings.Contains(got, "/tmp/test") {
		t.Errorf("expected root dir: %s", got)
	}
	if !strings.Contains(got, "total=10") {
		t.Error("expected task totals")
	}
}

func TestFormatMonitorReport(t *testing.T) {
	if FormatMonitorReport(nil) != "Harness monitor unavailable." {
		t.Error("expected nil message")
	}
	report := &MonitorReport{
		GeneratedAt: time.Now(),
		TaskTotals:  MonitorTaskTotals{Total: 5, Running: 1, Failed: 2},
	}
	got := FormatMonitorReport(report)
	if !strings.Contains(got, "total=5") {
		t.Errorf("expected totals: %s", got)
	}
}

func TestFormatOwnerInbox(t *testing.T) {
	if FormatOwnerInbox(nil) != "No harness owner inbox items." {
		t.Error("expected nil message")
	}
	inbox := &OwnerInbox{}
	if FormatOwnerInbox(inbox) != "No harness owner inbox items." {
		t.Error("expected empty inbox message")
	}
	inbox2 := &OwnerInbox{
		Entries: []OwnerInboxEntry{
			{Owner: "team-a", ReviewReady: []*Task{{ID: "t-1", Goal: "fix"}}},
		},
	}
	got := FormatOwnerInbox(inbox2)
	if !strings.Contains(got, "team-a") || !strings.Contains(got, "review_ready: 1") {
		t.Errorf("unexpected: %s", got)
	}
}

func TestFormatContextReport(t *testing.T) {
	if FormatContextReport(nil) != "No harness contexts found." {
		t.Error("expected nil message")
	}
	report := &ContextReport{
		Summaries: []ContextSummary{
			{Name: "api", Path: "internal/api", TaskCount: 3},
		},
	}
	got := FormatContextReport(report)
	if !strings.Contains(got, "internal/api") {
		t.Errorf("unexpected: %s", got)
	}
}

func TestSummarizeRunExitError(t *testing.T) {
	if summarizeRunExitError(nil, "") != "ggcode exited with an unknown failure" {
		t.Error("expected unknown failure")
	}
	got := summarizeRunExitError(&RunResult{ExitCode: 1, Output: "line1\nline2\nerror: something failed"}, "/tmp/log.txt")
	if !strings.Contains(got, "code 1") {
		t.Errorf("expected exit code: %s", got)
	}
	if !strings.Contains(got, "/tmp/log.txt") {
		t.Error("expected log path")
	}
}

func TestSummarizeHarnessRunFailure(t *testing.T) {
	if summarizeHarnessRunFailure("") != "" {
		t.Error("expected empty for empty output")
	}
	output := "tool: read file\ntool result: contents\n___BEGIN___COMMAND_DONE_MARKER___\nactual error message"
	got := summarizeHarnessRunFailure(output)
	if got != "actual error message" {
		t.Errorf("expected last meaningful line, got: %s", got)
	}
}

func TestSummarizeVerificationFailure(t *testing.T) {
	report := &DeliveryReport{
		Check: &CheckReport{
			Issues: []CheckIssue{{Message: "missing required field"}},
		},
	}
	got := summarizeVerificationFailure(report, "/tmp/report.json")
	if !strings.Contains(got, "missing required field") {
		t.Errorf("expected issue message: %s", got)
	}
	if !strings.Contains(got, "/tmp/report.json") {
		t.Error("expected report path")
	}
}

func TestOwnerForTask(t *testing.T) {
	cfg := &Config{
		Contexts: []ContextConfig{
			{Name: "api", Path: "internal/api", Owner: "team-backend"},
		},
	}
	task := &Task{ContextName: "api"}
	if ownerForTask(cfg, task) != "team-backend" {
		t.Error("expected context owner")
	}
	task2 := &Task{}
	if ownerForTask(cfg, task2) != unownedInboxOwner {
		t.Error("expected unowned")
	}
}

func TestOwnerMatches(t *testing.T) {
	cfg := &Config{
		Contexts: []ContextConfig{
			{Name: "api", Owner: "team-backend"},
		},
	}
	task := &Task{ContextName: "api"}
	if !ownerMatches(cfg, task, "team-backend") {
		t.Error("expected match")
	}
	if ownerMatches(cfg, task, "team-frontend") {
		t.Error("expected no match")
	}
	if !ownerMatches(cfg, task, "") {
		t.Error("expected match for empty owner")
	}
}

func TestMonitorDisplayPath(t *testing.T) {
	if monitorDisplayPath("") != "(unset)" {
		t.Error("expected unset for empty")
	}
	if monitorDisplayPath("/absolute/path") == "(unset)" {
		t.Error("expected path for non-empty")
	}
}

func TestParseMonitorTime(t *testing.T) {
	if !parseMonitorTime("").IsZero() {
		t.Error("expected zero time for empty")
	}
	ts := parseMonitorTime("2024-01-15T10:30:00Z")
	if ts.Year() != 2024 {
		t.Errorf("expected 2024, got %d", ts.Year())
	}
	if parseMonitorTime("invalid").IsZero() != true {
		t.Error("expected zero time for invalid")
	}
}

func TestContextSummaryKey(t *testing.T) {
	if contextSummaryKey("name", "path") != "path" {
		t.Error("expected path preferred")
	}
	if contextSummaryKey("name", "") != "name" {
		t.Error("expected name fallback")
	}
}

func TestShouldPersistHarnessResultLog(t *testing.T) {
	tmp := t.TempDir()
	path := tmp + "/test.log"
	// non-existent file → persist
	if !shouldPersistHarnessResultLog(path) {
		t.Error("expected persist for non-existent file")
	}
	// empty file → persist
	if err := writeTestFile(path, ""); err != nil {
		t.Fatal(err)
	}
	if !shouldPersistHarnessResultLog(path) {
		t.Error("expected persist for empty file")
	}
	// non-empty file → skip
	if err := writeTestFile(path, "existing content"); err != nil {
		t.Fatal(err)
	}
	if shouldPersistHarnessResultLog(path) {
		t.Error("expected skip for non-empty file")
	}
}

func TestRenderConfigTemplate(t *testing.T) {
	cfg := DefaultConfig("myproject", "Build stuff")
	cfg.Project.Deliverables = []string{"Feature A"}
	cfg.Checks.Commands = []CommandCheck{{Name: "test", Run: "go test ./..."}}
	cfg.Contexts = []ContextConfig{
		{Name: "api", Path: "internal/api", Owner: "team", RequireAgent: true},
	}

	output := renderConfigTemplate(cfg)
	if !strings.Contains(output, "myproject") {
		t.Error("expected project name")
	}
	if !strings.Contains(output, "Build stuff") {
		t.Error("expected project goal")
	}
	if !strings.Contains(output, "Feature A") {
		t.Error("expected deliverable")
	}
	if !strings.Contains(output, "go test ./...") {
		t.Error("expected check command")
	}
	if !strings.Contains(output, "internal/api") {
		t.Error("expected context path")
	}
}

func TestRenderAgentsTemplate(t *testing.T) {
	cfg := DefaultConfig("test", "My goal")
	output := renderAgentsTemplate(cfg)
	if !strings.Contains(output, "test") || !strings.Contains(output, "My goal") {
		t.Errorf("unexpected agents template: %s", output)
	}
}

func TestRenderRunbookTemplate(t *testing.T) {
	cfg := DefaultConfig("test", "Build it")
	output := renderRunbookTemplate(cfg)
	if !strings.Contains(output, "Build it") {
		t.Error("expected goal in runbook")
	}
}

func TestRenderContextAgentsTemplate(t *testing.T) {
	cfg := DefaultConfig("test", "Goal")
	ctx := ContextConfig{Name: "api", Path: "internal/api", Owner: "backend-team", Description: "API layer"}
	output := renderContextAgentsTemplate(cfg, ctx)
	if !strings.Contains(output, "api") {
		t.Error("expected context name")
	}
	if !strings.Contains(output, "backend-team") {
		t.Error("expected owner")
	}
	if !strings.Contains(output, "API layer") {
		t.Error("expected description")
	}
}

func TestRenderContextOwnerLine(t *testing.T) {
	if renderContextOwnerLine(ContextConfig{Owner: "team"}) != "- Owner: team" {
		t.Error("expected owner line")
	}
	if renderContextOwnerLine(ContextConfig{Owner: ""}) != "" {
		t.Error("expected empty for no owner")
	}
}

func TestTruncateWorkerText(t *testing.T) {
	if truncateWorkerText("short", 100) != "short" {
		t.Error("expected short text unchanged")
	}
	long := strings.Repeat("x", 200)
	got := truncateWorkerText(long, 50)
	if len(got) != 50 {
		t.Errorf("expected 50 chars, got %d", len(got))
	}
	if truncateWorkerText("hi", 3) != "hi" {
		t.Error("expected short text unchanged when maxLen < 4")
	}
	if truncateWorkerText("abc", 2) != "ab" {
		t.Error("expected truncation without ellipsis when maxLen < 4")
	}
}

func TestSummarizeWorkerOutputLine(t *testing.T) {
	if summarizeWorkerOutputLine("") != "" {
		t.Error("expected empty for empty line")
	}
	if summarizeWorkerOutputLine("  ") != "" {
		t.Error("expected empty for whitespace line")
	}
	if summarizeWorkerOutputLine("progress update") != "progress update" {
		t.Error("expected trimmed line")
	}
}

func TestDiscoverEmptyDir(t *testing.T) {
	_, err := Discover("")
	if err == nil {
		t.Error("expected error for empty dir")
	}
}

func TestDiscoverNonExistentDir(t *testing.T) {
	_, err := Discover("/nonexistent/path/that/does/not/exist")
	if err == nil {
		t.Error("expected error for non-existent dir")
	}
}

func TestDetectContextsInEmptyDir(t *testing.T) {
	tmp := t.TempDir()
	contexts := DetectContexts(tmp)
	// Empty dir has no subdirs → only cmd if it exists
	// Since tmp is empty, no cmd dir → empty
	if len(contexts) != 0 {
		t.Errorf("expected 0 contexts in empty dir, got %d", len(contexts))
	}
}

func TestDiscoverFailsWithoutHarness(t *testing.T) {
	tmp := t.TempDir()
	_, err := Discover(tmp)
	if err == nil {
		t.Error("expected error when no harness project found")
	}
}

func TestProjectFromRoot(t *testing.T) {
	p := projectFromRoot("/tmp/myproject")
	if p.RootDir != "/tmp/myproject" {
		t.Errorf("RootDir = %q", p.RootDir)
	}
	if p.ConfigPath == "" {
		t.Error("expected ConfigPath")
	}
	if p.StateDir == "" {
		t.Error("expected StateDir")
	}
	if p.TasksDir == "" {
		t.Error("expected TasksDir")
	}
	if p.LogsDir == "" {
		t.Error("expected LogsDir")
	}
	if p.ArchiveDir == "" {
		t.Error("expected ArchiveDir")
	}
	if p.WorktreesDir == "" {
		t.Error("expected WorktreesDir")
	}
	if p.EventLogPath == "" {
		t.Error("expected EventLogPath")
	}
	if p.SnapshotPath == "" {
		t.Error("expected SnapshotPath")
	}
}

func TestSummarizeWorkerResult(t *testing.T) {
	if summarizeWorkerResult(nil) != "worker finished" {
		t.Error("expected default for nil result")
	}
	if summarizeWorkerResult(&RunResult{ExitCode: 0, Output: "all done\nline2"}) != "all done" {
		t.Error("expected first non-empty line")
	}
	got := summarizeWorkerResult(&RunResult{ExitCode: 1, Output: "error: bad thing"})
	if !strings.Contains(got, "exit code 1") {
		t.Errorf("expected exit code info: %s", got)
	}
	if summarizeWorkerResult(&RunResult{ExitCode: 0, Output: ""}) != "worker completed" {
		t.Error("expected completed for empty output")
	}
}

// helper
func writeTestFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0644)
}
