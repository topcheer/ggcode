package agent

import (
	"encoding/json"
	"testing"

	"github.com/topcheer/ggcode/internal/tool"
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

	w1 := s.recordToolCall("run_command", mustJSONIdempot(t, map[string]interface{}{
		"command": "go test ./...",
	}), 1)
	if w1 != "" {
		t.Fatalf("first build should not warn, got: %s", w1)
	}

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

	s.recordToolCall("run_command", mustJSONIdempot(t, map[string]interface{}{
		"command": "go build ./...",
	}), 1)

	s.recordToolCall("edit_file", mustJSONIdempot(t, map[string]interface{}{
		"file_path": "main.go",
	}), 2)

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

	s.recordToolCall("run_command", mustJSONIdempot(t, map[string]interface{}{
		"command": "make test",
	}), 1)

	w2 := s.recordToolCall("run_command", mustJSONIdempot(t, map[string]interface{}{
		"command": "make test",
	}), 2)
	if w2 == "" {
		t.Fatal("first redundant build should warn")
	}

	w3 := s.recordToolCall("run_command", mustJSONIdempot(t, map[string]interface{}{
		"command": "make test",
	}), 3)
	if w3 == "" {
		t.Fatal("second redundant build should warn")
	}

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

	w := s.recordToolCall("run_command", mustJSONIdempot(t, map[string]interface{}{
		"command": "go test ./...",
	}), 3)
	if w != "" {
		t.Fatalf("after reset, first build should not warn, got: %s", w)
	}
}

func TestBuildIdempotency_MultipleBuildTools(t *testing.T) {
	s := newBuildIdempotencyState()

	s.recordToolCall("run_command", mustJSONIdempot(t, map[string]interface{}{
		"command": "go test ./...",
	}), 1)

	w2 := s.recordToolCall("run_command", mustJSONIdempot(t, map[string]interface{}{
		"command": "npm test",
	}), 2)

	if w2 != "" {
		t.Fatalf("different build command with 0 edits must not warn: %q", w2)
	}

	w3 := s.recordToolCall("run_command", mustJSONIdempot(t, map[string]interface{}{
		"command": "npm test",
	}), 3)
	if w3 == "" {
		t.Fatal("same build command re-run with 0 edits should warn")
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
		isBuild, label, _ := detectBuildTestCommand(tt.cmd)
		if isBuild != tt.isBuild {
			t.Errorf("detectBuildTestCommand(%q): isBuild = %v, want %v", tt.cmd, isBuild, tt.isBuild)
		}
		if tt.isBuild && label != tt.label {
			t.Errorf("detectBuildTestCommand(%q): label = %q, want %q", tt.cmd, label, tt.label)
		}
	}
}

func TestDetectBuildTestCommand_EnvVarPrefix(t *testing.T) {

	isBuild, label, key := detectBuildTestCommand("GOOS=linux go build ./...")
	if !isBuild || label != "go build" {
		t.Errorf("env-prefixed go build: isBuild=%v label=%q", isBuild, label)
	}

	if key != "goos=linux|go build ./..." {
		t.Errorf("identity key = %q, want goos=linux|go build ./...", key)
	}
}

func TestDetectBuildTestCommand_CommentPrefix(t *testing.T) {

	isBuild, label, _ := detectBuildTestCommand("# build everything\ngo build ./...")
	if !isBuild || label != "go build" {
		t.Errorf("comment-prefixed go build: isBuild=%v label=%q", isBuild, label)
	}
}

func TestDetectBuildTestCommand_CaseInsensitive(t *testing.T) {
	isBuild, _, _ := detectBuildTestCommand("GO TEST ./...")
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

func TestShellMutatesSourcesCommonWriteSurfaces(t *testing.T) {
	mutating := []string{
		"echo 'package main' > main.go",
		"cat header.txt body.txt >> out.go",
		"tee version.go <<< v2",
		"cp src.go backup.go",
		"mv old.go new.go",
		"git restore internal/tool/file.go",
		"git checkout -- .",
		"git stash",
		"clang-format -i fmt.cc",
		"perl -pi -e 's/a/b/' x.go",
		"dd if=img of=loader.go",
		"install -m 644 gen.go dest.go",
		"patch < fix.diff",
	}
	for _, cmd := range mutating {
		if !shellMutatesSources(cmd) {
			t.Errorf("must be detected as source mutation: %q", cmd)
		}
	}

	for _, cmd := range []string{
		`echo "a>b"`,
		`grep 'foo > bar' file.go`,
		"go build ./...",
		"ls -la",

		"go build ./... > /dev/null",
		"go test ./internal/agent/ > /dev/null 2>&1",
		"cargo build 2>/dev/null",
		"make lint >/dev/stderr",

		`grep 'use > v1.2' readme`,
		`echo "see > v1.2.md" docs`,
	} {
		if shellMutatesSources(cmd) {
			t.Errorf("false positive on non-mutating command: %q", cmd)
		}
	}

	for _, cmd := range []string{
		"echo hi > out.txt",
		"go build ./... > build.log",
		"echo hi >>notes.md",
	} {
		if !shellMutatesSources(cmd) {
			t.Errorf("must be detected as source mutation: %q", cmd)
		}
	}
}

func TestBuildIdempotency_SameLabelDifferentCommand(t *testing.T) {
	s := newBuildIdempotencyState()
	s.recordToolCall("run_command", mustJSONIdempot(t, map[string]interface{}{
		"command": "go test ./internal/a/",
	}), 1)

	w := s.recordToolCall("run_command", mustJSONIdempot(t, map[string]interface{}{
		"command": "go test ./internal/b/",
	}), 2)
	if w != "" {
		t.Fatalf("scenario A: different package target must not warn, got: %s", w)
	}

	w = s.recordToolCall("run_command", mustJSONIdempot(t, map[string]interface{}{
		"command": "go test -run TestFoo ./internal/b/",
	}), 3)
	if w != "" {
		t.Fatalf("scenario C: different -run filter must not warn, got: %s", w)
	}
}

func TestBuildIdempotency_GOOSChangesIdentity(t *testing.T) {
	s := newBuildIdempotencyState()
	s.recordToolCall("run_command", mustJSONIdempot(t, map[string]interface{}{
		"command": "GOOS=linux go build ./...",
	}), 1)
	w := s.recordToolCall("run_command", mustJSONIdempot(t, map[string]interface{}{
		"command": "go build ./...",
	}), 2)
	if w != "" {
		t.Fatalf("native build after cross-compile must not warn, got: %s", w)
	}
}

func TestShellMutatesSources_GoGenerateCrossCommand(t *testing.T) {
	for _, cmd := range []string{
		"go generate ./...",
		"go generate ./internal/gen/",
		"curl --output config.yaml https://example.com/cfg",
	} {
		if !shellMutatesSources(cmd) {
			t.Errorf("shellMutatesSources(%q) = false, want true (#2655 follow-up)", cmd)
		}
	}

	for _, cmd := range []string{
		"git fetch origin",
		"git status",
		"git log --oneline",
	} {
		if shellMutatesSources(cmd) {
			t.Errorf("shellMutatesSources(%q) = true, want false (read-only FP guard)", cmd)
		}
	}

	cc := newCommandCache()
	res := tool.Result{Content: "PASS pre-generation"}
	cmd := "# verify\ngo test ./..."
	cc.put(cmd, "/repo", res)
	if shellMutatesSources("go generate ./...") {
		cc.invalidate()
	}
	if _, hit := cc.get(cmd, "/repo"); hit {
		t.Fatal("cache serves pre-generation `go test` result after `go generate` rewrote sources: stale replay (#2655 follow-up)")
	}
}
