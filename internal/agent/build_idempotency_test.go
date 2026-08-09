package agent

import (
	"encoding/json"
	"testing"
)

func mustJSONIdempot(t *testing.T, v map[string]interface{}) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestBuildIdempotency_RedundantBuildNoEdits(t *testing.T) {
	s := newBuildIdempotencyState()

	// Iteration 1: first build -- no warning expected.
	w1 := s.recordToolCall("run_command", mustJSONIdempot(t, map[string]interface{}{
		"command": "go test ./...",
	}), 1)
	if w1 != "" {
		t.Fatalf("first build should not warn, got: %s", w1)
	}

	// Iteration 2: no edits, second build -- should warn.
	w2 := s.recordToolCall("run_command", mustJSONIdempot(t, map[string]interface{}{
		"command": "go test ./...",
	}), 2)
	if w2 == "" {
		t.Fatal("second build with 0 edits should warn")
	}
	if !contains(w2, "Build Idempotency") {
		t.Errorf("warning should contain header, got: %s", w2)
	}
	if !contains(w2, "go test") {
		t.Errorf("warning should mention command label, got: %s", w2)
	}
}

func TestBuildIdempotency_BuildAfterEditNoWarning(t *testing.T) {
	s := newBuildIdempotencyState()

	// First build.
	s.recordToolCall("run_command", mustJSONIdempot(t, map[string]interface{}{
		"command": "go build ./...",
	}), 1)

	// Edit a file.
	s.recordToolCall("edit_file", mustJSONIdempot(t, map[string]interface{}{
		"file_path": "main.go",
	}), 2)

	// Second build -- should NOT warn because there was an edit.
	w3 := s.recordToolCall("run_command", mustJSONIdempot(t, map[string]interface{}{
		"command": "go build ./...",
	}), 3)
	if w3 != "" {
		t.Fatalf("build after edit should not warn, got: %s", w3)
	}
}

func TestBuildIdempotency_NonBuildCommandNoWarning(t *testing.T) {
	s := newBuildIdempotencyState()

	s.recordToolCall("run_command", mustJSONIdempot(t, map[string]interface{}{
		"command": "ls -la",
	}), 1)

	w2 := s.recordToolCall("run_command", mustJSONIdempot(t, map[string]interface{}{
		"command": "ls -la",
	}), 2)
	if w2 != "" {
		t.Fatalf("non-build command should not warn, got: %s", w2)
	}
}

func TestBuildIdempotency_MaxWarnings(t *testing.T) {
	s := newBuildIdempotencyState()

	// First build.
	s.recordToolCall("run_command", mustJSONIdempot(t, map[string]interface{}{
		"command": "make test",
	}), 1)

	// Redundant build 1 -- should warn.
	w2 := s.recordToolCall("run_command", mustJSONIdempot(t, map[string]interface{}{
		"command": "make test",
	}), 2)
	if w2 == "" {
		t.Fatal("first redundant build should warn")
	}

	// Redundant build 2 -- should warn.
	w3 := s.recordToolCall("run_command", mustJSONIdempot(t, map[string]interface{}{
		"command": "make test",
	}), 3)
	if w3 == "" {
		t.Fatal("second redundant build should warn")
	}

	// Redundant build 3 -- should NOT warn (max 2 warnings).
	w4 := s.recordToolCall("run_command", mustJSONIdempot(t, map[string]interface{}{
		"command": "make test",
	}), 4)
	if w4 != "" {
		t.Fatalf("third redundant build should not warn (max reached), got: %s", w4)
	}
}

func TestBuildIdempotency_Reset(t *testing.T) {
	s := newBuildIdempotencyState()

	s.recordToolCall("run_command", mustJSONIdempot(t, map[string]interface{}{
		"command": "go test ./...",
	}), 1)
	s.recordToolCall("run_command", mustJSONIdempot(t, map[string]interface{}{
		"command": "go test ./...",
	}), 2)

	s.reset()

	// After reset, a build should not warn.
	w := s.recordToolCall("run_command", mustJSONIdempot(t, map[string]interface{}{
		"command": "go test ./...",
	}), 3)
	if w != "" {
		t.Fatalf("after reset, first build should not warn, got: %s", w)
	}
}

func TestBuildIdempotency_MultipleBuildTools(t *testing.T) {
	s := newBuildIdempotencyState()

	// go test then npm test -- different commands but still 0 edits.
	s.recordToolCall("run_command", mustJSONIdempot(t, map[string]interface{}{
		"command": "go test ./...",
	}), 1)

	w2 := s.recordToolCall("run_command", mustJSONIdempot(t, map[string]interface{}{
		"command": "npm test",
	}), 2)
	// Different build command but still 0 edits -- should warn.
	if w2 == "" {
		t.Fatal("second different build with 0 edits should warn")
	}
}

func TestBuildIdempotency_WriteFileCountsAsEdit(t *testing.T) {
	s := newBuildIdempotencyState()

	s.recordToolCall("run_command", mustJSONIdempot(t, map[string]interface{}{
		"command": "go test ./...",
	}), 1)

	s.recordToolCall("write_file", mustJSONIdempot(t, map[string]interface{}{
		"path":    "new.go",
		"content": "package main",
	}), 2)

	// Build after write_file -- should NOT warn.
	w3 := s.recordToolCall("run_command", mustJSONIdempot(t, map[string]interface{}{
		"command": "go test ./...",
	}), 3)
	if w3 != "" {
		t.Fatalf("build after write_file should not warn, got: %s", w3)
	}
}

func TestBuildIdempotency_StartCommand(t *testing.T) {
	s := newBuildIdempotencyState()

	s.recordToolCall("start_command", mustJSONIdempot(t, map[string]interface{}{
		"command": "go build ./...",
	}), 1)

	w2 := s.recordToolCall("start_command", mustJSONIdempot(t, map[string]interface{}{
		"command": "go build ./...",
	}), 2)
	if w2 == "" {
		t.Fatal("start_command redundant build should warn")
	}
}

func TestDetectBuildTestCommand(t *testing.T) {
	tests := []struct {
		cmd     string
		isBuild bool
		label   string
	}{
		{"go build ./...", true, "go build"},
		{"go test ./...", true, "go test"},
		{"go vet ./...", true, "go vet"},
		{"make test", true, "make test"},
		{"npm test", true, "npm test"},
		{"npm run build", true, "npm build"},
		{"cargo test", true, "cargo test"},
		{"pytest -v", true, "pytest"},
		{"python -m pytest", true, "pytest"},
		{"yarn build", true, "yarn build"},
		{"ls -la", false, ""},
		{"git status", false, ""},
		{"cat file.txt", false, ""},
		{"echo hello", false, ""},
	}

	for _, tt := range tests {
		isBuild, label := detectBuildTestCommand(tt.cmd)
		if isBuild != tt.isBuild {
			t.Errorf("detectBuildTestCommand(%q): isBuild = %v, want %v", tt.cmd, isBuild, tt.isBuild)
		}
		if tt.isBuild && label != tt.label {
			t.Errorf("detectBuildTestCommand(%q): label = %q, want %q", tt.cmd, label, tt.label)
		}
	}
}

func TestDetectBuildTestCommand_EnvVarPrefix(t *testing.T) {
	// Commands with env var prefixes should still be detected.
	isBuild, label := detectBuildTestCommand("GOOS=linux go build ./...")
	if !isBuild || label != "go build" {
		t.Errorf("env-prefixed go build: isBuild=%v label=%q", isBuild, label)
	}
}

func TestDetectBuildTestCommand_CommentPrefix(t *testing.T) {
	// Commands with leading comment lines should still be detected.
	isBuild, label := detectBuildTestCommand("# build everything\ngo build ./...")
	if !isBuild || label != "go build" {
		t.Errorf("comment-prefixed go build: isBuild=%v label=%q", isBuild, label)
	}
}

func TestDetectBuildTestCommand_CaseInsensitive(t *testing.T) {
	isBuild, _ := detectBuildTestCommand("GO TEST ./...")
	if !isBuild {
		t.Error("uppercase GO TEST should be detected")
	}
}

func TestBuildIdempot_Itoa(t *testing.T) {
	tests := []struct {
		input int
		want  string
	}{
		{0, "0"},
		{1, "1"},
		{10, "10"},
		{42, "42"},
		{100, "100"},
		{-1, "-1"},
	}
	for _, tt := range tests {
		got := itoaIdempot(tt.input)
		if got != tt.want {
			t.Errorf("itoaIdempot(%d) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func containsIdempot(s, substr string) bool {
	return len(s) >= len(substr) && indexOfIdempot(s, substr) >= 0
}

func indexOfIdempot(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}
