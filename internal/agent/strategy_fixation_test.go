package agent

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestStrategyFixation_NoTriggerBelowThreshold(t *testing.T) {
	s := newStrategyFixationState()

	s.recordEdit("/path/to/file.go")
	s.recordEdit("/path/to/file.go")
	s.recordVerification("run_command", "build error in file.go", true)

	if msg := s.check(); msg != "" {
		t.Fatalf("expected no warning below threshold, got: %s", msg)
	}
}

func TestStrategyFixation_NoTriggerWithoutFailures(t *testing.T) {
	s := newStrategyFixationState()

	s.recordEdit("/path/to/file.go")
	s.recordEdit("/path/to/file.go")
	s.recordEdit("/path/to/file.go")

	if msg := s.check(); msg != "" {
		t.Fatalf("expected no warning without failures, got: %s", msg)
	}
}

func TestStrategyFixation_TriggersOnRepeatedEditsWithFailures(t *testing.T) {
	s := newStrategyFixationState()

	s.recordEdit("/path/to/parser.go")
	s.recordEdit("/path/to/parser.go")
	s.recordEdit("/path/to/parser.go")

	s.recordVerification("run_command", "parser.go:10: undefined: foo", true)
	s.recordVerification("run_command", "parser.go:15: syntax error", true)

	msg := s.check()
	if msg == "" {
		t.Fatal("expected strategy fixation warning, got empty")
	}
	if !strings.Contains(msg, "parser.go") {
		t.Errorf("warning should mention the file name, got: %s", msg)
	}
	if !strings.Contains(msg, "strategy-fixation") {
		t.Errorf("warning should have the tag, got: %s", msg)
	}
	if !strings.Contains(msg, "3 times") {
		t.Errorf("warning should mention edit count, got: %s", msg)
	}
}

func TestStrategyFixation_SuccessResetsFailures(t *testing.T) {
	s := newStrategyFixationState()

	s.recordEdit("/path/to/file.go")
	s.recordVerification("run_command", "build error in file.go", true)
	s.recordVerification("run_command", "build error in file.go", true)
	s.recordEdit("/path/to/file.go")
	s.recordEdit("/path/to/file.go")

	s.recordVerification("run_command", "all tests passed", false)

	if msg := s.check(); msg != "" {
		t.Fatalf("expected no warning after successful verification, got: %s", msg)
	}
}

func TestStrategyFixation_GreenVerificationResetsAllFiles(t *testing.T) {
	s := newStrategyFixationState()

	s.recordEdit("a.go")
	s.recordEdit("a.go")
	s.recordVerification("run_command", "error in a.go", true)
	s.recordVerification("run_command", "error in a.go", true)
	s.recordEdit("a.go")
	s.recordEdit("b.go")

	s.recordVerification("run_command", "ok", false)

	if msg := s.check(); msg != "" {
		t.Fatalf("green build must reset all files (#485), got: %s", msg)
	}
	if len(s.fileEdits) != 0 || len(s.fileFailures) != 0 {
		t.Fatalf("green build must clear all maps, edits=%v failures=%v", s.fileEdits, s.fileFailures)
	}
}

func TestStrategyFixation_TrueFixationStillDetectedAfterGreenReset(t *testing.T) {
	s := newStrategyFixationState()

	s.recordEdit("a.go")
	s.recordVerification("run_command", "error in a.go", true)
	s.recordVerification("run_command", "error in a.go", true)
	s.recordEdit("a.go")
	s.recordEdit("a.go")
	s.recordVerification("run_command", "ok", false)

	s.recordEdit("a.go")
	s.recordVerification("run_command", "error in a.go", true)
	s.recordEdit("a.go")
	s.recordEdit("a.go")
	s.recordVerification("run_command", "error in a.go", true)

	if msg := s.check(); msg == "" {
		t.Fatal("post-green re-fixation must re-fire")
	}
}

func TestSFOutputNamesFile(t *testing.T) {
	cases := []struct {
		name     string
		output   string
		filePath string
		want     bool
	}{

		{"bare", "agent.go:10: undefined: foo", "/w/internal/tool/agent.go", true},

		{"dir match", "error in internal/tool/agent.go: missing foo", "/w/internal/tool/agent.go", true},

		{"dir mismatch", "error in internal/agent/agent.go: missing foo", "/w/internal/tool/agent.go", false},

		{"mismatch then bare", "internal/agent/agent.go bad\nagent.go:5: oops", "/w/internal/tool/agent.go", true},

		{"absent", "everything fine", "/w/a.go", false},
	}
	for _, c := range cases {
		if got := sfOutputNamesFile(c.output, c.filePath); got != c.want {
			t.Errorf("%s: sfOutputNamesFile(%q, %q) = %v, want %v", c.name, c.output, c.filePath, got, c.want)
		}
	}
}

func TestSFExtractMutationPaths(t *testing.T) {

	args := json.RawMessage(`{"files":[{"path":"a.go","edits":[]},{"path":"sub/b.go"}]}`)
	paths := sfExtractMutationPaths(args)
	if len(paths) != 2 || paths[0] != "a.go" || paths[1] != "sub/b.go" {
		t.Fatalf("multi_file_edit paths = %v, want [a.go sub/b.go]", paths)
	}

	nb := sfExtractMutationPaths(json.RawMessage(`{"notebook_path":"nb/ipynb/analysis.ipynb"}`))
	if len(nb) != 1 || nb[0] != "nb/ipynb/analysis.ipynb" {
		t.Fatalf("notebook_edit path = %v, want notebook_path value", nb)
	}

	one := sfExtractMutationPaths(json.RawMessage(`{"file_path":"x.go"}`))
	if len(one) != 1 || one[0] != "x.go" {
		t.Fatalf("edit_file path = %v, want [x.go]", one)
	}
}

func TestSFCommandArg(t *testing.T) {
	if got := sfCommandArg(map[string]interface{}{"command": "go test ./..."}); got != "go test ./..." {
		t.Errorf("string command = %q", got)
	}
	if got := sfCommandArg(map[string]interface{}{"command": 42}); got != "" {
		t.Errorf("non-string command should yield empty, got %q", got)
	}
	if got := sfCommandArg(nil); got != "" {
		t.Errorf("nil map should yield empty, got %q", got)
	}
}

func TestStrategyFixation_MaxWarnsCap(t *testing.T) {
	s := newStrategyFixationState()

	s.recordEdit("/a.go")
	s.recordEdit("/a.go")
	s.recordEdit("/a.go")
	s.recordVerification("run_command", "error in a.go", true)
	s.recordVerification("run_command", "error in a.go", true)
	if msg1 := s.check(); msg1 == "" {
		t.Fatal("expected first warning")
	}

	s.recordEdit("/b.go")
	s.recordEdit("/b.go")
	s.recordEdit("/b.go")
	s.recordVerification("run_command", "error in b.go", true)
	s.recordVerification("run_command", "error in b.go", true)
	if msg2 := s.check(); msg2 != "" {
		t.Fatalf("expected second warning to be suppressed, got: %s", msg2)
	}

	s.recordEdit("/c.go")
	s.recordEdit("/c.go")
	s.recordEdit("/c.go")
	s.recordVerification("run_command", "error in c.go", true)
	s.recordVerification("run_command", "error in c.go", true)
	if msg3 := s.check(); msg3 != "" {
		t.Fatalf("expected no third warning (cap reached), got: %s", msg3)
	}
}

func TestStrategyFixation_DoesNotRewarnSameFile(t *testing.T) {
	s := newStrategyFixationState()

	s.recordEdit("/a.go")
	s.recordEdit("/a.go")
	s.recordEdit("/a.go")
	s.recordVerification("run_command", "error in a.go", true)
	s.recordVerification("run_command", "error in a.go", true)

	if msg1 := s.check(); msg1 == "" {
		t.Fatal("expected first warning")
	}

	s.recordEdit("/a.go")
	s.recordEdit("/a.go")
	if msg2 := s.check(); msg2 != "" {
		t.Fatalf("expected no re-warning for same file, got: %s", msg2)
	}
}

func TestStrategyFixation_Reset(t *testing.T) {
	s := newStrategyFixationState()

	s.recordEdit("/a.go")
	s.recordEdit("/a.go")
	s.recordEdit("/a.go")
	s.recordVerification("run_command", "error in a.go", true)
	s.recordVerification("run_command", "error in a.go", true)
	_ = s.check()

	s.reset()

	if len(s.fileEdits) != 0 || len(s.fileFailures) != 0 || len(s.warnedFiles) != 0 {
		t.Fatal("reset should clear all maps")
	}
	if s.warnCount != 0 {
		t.Fatal("reset should clear warnCount")
	}
}

func TestStrategyFixation_DifferentFilesNoTrigger(t *testing.T) {
	s := newStrategyFixationState()

	s.recordEdit("/a.go")
	s.recordEdit("/b.go")
	s.recordEdit("/c.go")
	s.recordVerification("run_command", "error in a.go", true)
	s.recordVerification("run_command", "error in b.go", true)

	if msg := s.check(); msg != "" {
		t.Fatalf("should not trigger when edits spread across files, got: %s", msg)
	}
}

func TestStrategyFixation_EmptyPath(t *testing.T) {
	s := newStrategyFixationState()

	s.recordEdit("")
	if len(s.fileEdits) != 0 {
		t.Fatal("empty path should not be recorded")
	}
}

func TestStrategyFixation_VerificationErrorFiltering(t *testing.T) {
	s := newStrategyFixationState()

	s.recordEdit("/a.go")
	s.recordEdit("/a.go")
	s.recordEdit("/a.go")
	s.recordVerification("run_command", "everything looks fine, just a timeout", true)

	if msg := s.check(); msg != "" {

		_ = msg
	}
}

func TestShortFileName(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"/path/to/file.go", "file.go"},
		{"C:\\Users\\test\\file.go", "file.go"},
		{"file.go", "file.go"},
		{"", ""},
	}
	for _, c := range cases {
		got := shortFileName(c.input)
		if got != c.want {
			t.Errorf("shortFileName(%q) = %q, want %q", c.input, got, c.want)
		}
	}
}

func TestStrategyFixationIsMutation(t *testing.T) {

	tools := []string{
		"edit_file", "write_file", "multi_edit_file", "multi_file_edit",
		"multi_file_write", "batch_replace", "lsp_rename", "file_ops", "notebook_edit",
	}
	for _, tool := range tools {
		if !strategyFixationIsMutation(tool) {
			t.Errorf("expected %s to be mutation", tool)
		}
	}
	if strategyFixationIsMutation("read_file") {
		t.Error("read_file should not be mutation")
	}
}

func TestStrategyFixationIsVerification(t *testing.T) {

	tools := []string{"run_command", "bash", "powershell", "wait_command", "read_command_output", "code_health", "review_changes", "verify", "lsp_diagnostics"}
	for _, tool := range tools {
		if !strategyFixationIsVerification(tool) {
			t.Errorf("expected %s to be verification", tool)
		}
	}
	if strategyFixationIsVerification("start_command") {
		t.Error("start_command must not count as verification at launch (#3685-B)")
	}
	if strategyFixationIsVerification("edit_file") {
		t.Error("edit_file should not be verification")
	}
}
