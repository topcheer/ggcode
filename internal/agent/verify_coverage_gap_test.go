package agent

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCoverageRecordEditFiles(t *testing.T) {
	s := newEditCoverageState()
	s.recordToolCall("edit_file", jsonStr(t, map[string]string{"file_path": "/workspace/internal/agent/foo.go"}))
	s.recordToolCall("edit_file", jsonStr(t, map[string]string{"file_path": "/workspace/internal/config/bar.go"}))

	if len(s.editedFiles) != 2 {
		t.Fatalf("expected 2 edited files, got %d", len(s.editedFiles))
	}
}

func TestCoverageWriteFilePath(t *testing.T) {
	s := newEditCoverageState()
	s.recordToolCall("write_file", jsonStr(t, map[string]string{"path": "/workspace/internal/tool/baz.go"}))

	if len(s.editedFiles) != 1 {
		t.Fatalf("expected 1 edited file, got %d", len(s.editedFiles))
	}
	if !s.editedFiles["/workspace/internal/tool/baz.go"] {
		t.Fatal("write_file path not tracked")
	}
}

func TestCoverageMultiFileWrite(t *testing.T) {
	s := newEditCoverageState()
	args := jsonStr(t, map[string]interface{}{
		"files": []map[string]string{
			{"path": "/workspace/internal/agent/a.go"},
			{"path": "/workspace/internal/config/b.go"},
		},
	})
	s.recordToolCall("multi_file_write", args)
	if len(s.editedFiles) != 2 {
		t.Fatalf("expected 2 edited files from multi_file_write, got %d", len(s.editedFiles))
	}
}

func TestCoverageNoGapWhenFullScope(t *testing.T) {
	s := newEditCoverageState()
	s.recordToolCall("edit_file", jsonStr(t, map[string]string{"file_path": "/workspace/internal/agent/foo.go"}))
	s.recordToolCall("edit_file", jsonStr(t, map[string]string{"file_path": "/workspace/internal/config/bar.go"}))

	// Full scope ./... covers everything
	warn := s.recordToolCall("run_command", jsonStr(t, map[string]string{"command": "go test ./..."}))
	if warn != "" {
		t.Fatalf("expected no warning for ./... scope, got: %s", warn)
	}
}

func TestCoverageGapDetected(t *testing.T) {
	s := newEditCoverageState()
	s.recordToolCall("edit_file", jsonStr(t, map[string]string{"file_path": "/workspace/internal/agent/foo.go"}))
	s.recordToolCall("edit_file", jsonStr(t, map[string]string{"file_path": "/workspace/internal/config/bar.go"}))

	warn := s.recordToolCall("run_command", jsonStr(t, map[string]string{"command": "go test ./internal/agent/"}))
	if warn == "" {
		t.Fatal("expected coverage gap warning but got none")
	}
	if !strings.Contains(warn, "internal/config") {
		t.Fatalf("warning should mention uncovered package internal/config, got: %s", warn)
	}
}

func TestCoverageGapMaxWarns(t *testing.T) {
	s := newEditCoverageState()
	s.recordToolCall("edit_file", jsonStr(t, map[string]string{"file_path": "/workspace/internal/agent/foo.go"}))
	s.recordToolCall("edit_file", jsonStr(t, map[string]string{"file_path": "/workspace/internal/config/bar.go"}))

	// First narrow verify - should warn
	w1 := s.recordToolCall("run_command", jsonStr(t, map[string]string{"command": "go test ./internal/agent/"}))
	if w1 == "" {
		t.Fatal("expected first warning")
	}

	// Second narrow verify (different command) - should warn
	w2 := s.recordToolCall("run_command", jsonStr(t, map[string]string{"command": "go build ./internal/agent/"}))
	if w2 == "" {
		t.Fatal("expected second warning")
	}

	// Third - should be capped
	w3 := s.recordToolCall("run_command", jsonStr(t, map[string]string{"command": "go vet ./internal/agent/"}))
	if w3 != "" {
		t.Fatal("expected no third warning due to cap")
	}
}

func TestCoverageGapDedupSameCmd(t *testing.T) {
	s := newEditCoverageState()
	s.recordToolCall("edit_file", jsonStr(t, map[string]string{"file_path": "/workspace/internal/agent/foo.go"}))
	s.recordToolCall("edit_file", jsonStr(t, map[string]string{"file_path": "/workspace/internal/config/bar.go"}))

	// Same command twice - only warn once
	w1 := s.recordToolCall("run_command", jsonStr(t, map[string]string{"command": "go test ./internal/agent/"}))
	if w1 == "" {
		t.Fatal("expected first warning")
	}
	w2 := s.recordToolCall("run_command", jsonStr(t, map[string]string{"command": "go test ./internal/agent/"}))
	if w2 != "" {
		t.Fatal("expected no duplicate warning for same command")
	}
}

func TestCoverageNoGapSinglePackage(t *testing.T) {
	s := newEditCoverageState()
	s.recordToolCall("edit_file", jsonStr(t, map[string]string{"file_path": "/workspace/internal/agent/foo.go"}))
	s.recordToolCall("edit_file", jsonStr(t, map[string]string{"file_path": "/workspace/internal/agent/bar.go"}))

	// Both files in same package - no gap
	warn := s.recordToolCall("run_command", jsonStr(t, map[string]string{"command": "go test ./internal/agent/"}))
	if warn != "" {
		t.Fatalf("expected no warning for single package, got: %s", warn)
	}
}

func TestCoverageNonVerifyCommand(t *testing.T) {
	s := newEditCoverageState()
	s.recordToolCall("edit_file", jsonStr(t, map[string]string{"file_path": "/workspace/internal/agent/foo.go"}))
	s.recordToolCall("edit_file", jsonStr(t, map[string]string{"file_path": "/workspace/internal/config/bar.go"}))

	// Non-verification command should not trigger
	warn := s.recordToolCall("run_command", jsonStr(t, map[string]string{"command": "ls -la"}))
	if warn != "" {
		t.Fatalf("expected no warning for non-verify command, got: %s", warn)
	}
}

func TestCoverageFileToPackage(t *testing.T) {
	cases := []struct {
		file, pkg string
	}{
		{"/workspace/internal/agent/foo.go", "/workspace/internal/agent"},
		{"./internal/config/bar.go", "internal/config"},
		{"baz.go", ""},
		{"./x/y/z.go", "x/y"},
	}
	for _, c := range cases {
		got := coverageFileToPackage(c.file)
		if got != c.pkg {
			t.Errorf("coverageFileToPackage(%q) = %q, want %q", c.file, got, c.pkg)
		}
	}
}

func TestCoveragePkgInScope(t *testing.T) {
	if !coveragePkgInScope("internal/agent", "ALL") {
		t.Error("ALL scope should cover everything")
	}
	if coveragePkgInScope("internal/agent", ".") {
		t.Error("bare scope should not cover internal/agent")
	}
	if !coveragePkgInScope("internal/agent", "internal/agent") {
		t.Error("exact match should cover")
	}
	if !coveragePkgInScope("internal/agent/sub", "internal/agent") {
		t.Error("subdir should be covered by parent scope")
	}
	if coveragePkgInScope("internal/config", "internal/agent") {
		t.Error("sibling package should NOT be covered")
	}
}

func TestCoverageExtractVerifyScope(t *testing.T) {
	cases := []struct {
		cmd, scope string
	}{
		{"go test ./...", "ALL"},
		{"go test ./internal/agent/", "internal/agent"},
		{"go build ./internal/config", "internal/config"},
		{"go test", "."},
		{"npm test", "."},
		{"ls -la", ""},
		{"go vet ./internal/...", "ALL"},
		{"go test ./internal/agent/", "internal/agent"},
	}
	for _, c := range cases {
		got := coverageExtractVerifyScope(c.cmd)
		if got != c.scope {
			t.Errorf("coverageExtractVerifyScope(%q) = %q, want %q", c.cmd, got, c.scope)
		}
	}
}

func TestCoverageReset(t *testing.T) {
	s := newEditCoverageState()
	s.recordToolCall("edit_file", jsonStr(t, map[string]string{"file_path": "/workspace/internal/agent/foo.go"}))
	s.recordToolCall("edit_file", jsonStr(t, map[string]string{"file_path": "/workspace/internal/config/bar.go"}))
	s.recordToolCall("run_command", jsonStr(t, map[string]string{"command": "go test ./internal/agent/"}))
	s.reset()
	if len(s.editedFiles) != 0 || len(s.verifiedPkgs) != 0 || s.warnCount != 0 {
		t.Fatal("reset did not clear state")
	}
}

func TestCoverageFileOpsPaths(t *testing.T) {
	s := newEditCoverageState()
	args := jsonStr(t, map[string]interface{}{
		"operations": []map[string]string{
			{"action": "move", "source": "/workspace/internal/agent/old.go"},
		},
	})
	s.recordToolCall("file_ops", args)
	if len(s.editedFiles) != 1 {
		t.Fatalf("expected 1 file from file_ops, got %d", len(s.editedFiles))
	}
}

func TestCoverageThreePackagesPartialVerify(t *testing.T) {
	s := newEditCoverageState()
	s.recordToolCall("edit_file", jsonStr(t, map[string]string{"file_path": "internal/agent/a.go"}))
	s.recordToolCall("edit_file", jsonStr(t, map[string]string{"file_path": "internal/config/b.go"}))
	s.recordToolCall("edit_file", jsonStr(t, map[string]string{"file_path": "internal/tool/c.go"}))

	warn := s.recordToolCall("run_command", jsonStr(t, map[string]string{"command": "go test ./internal/agent/"}))
	if warn == "" {
		t.Fatal("expected warning with 3 packages and partial verify")
	}
	// Should mention both uncovered packages
	if !strings.Contains(warn, "internal/config") {
		t.Error("should mention internal/config")
	}
	if !strings.Contains(warn, "internal/tool") {
		t.Error("should mention internal/tool")
	}
}

func jsonStr(t *testing.T, v interface{}) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
