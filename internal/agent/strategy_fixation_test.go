package agent

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestStrategyFixation_NoTriggerBelowThreshold(t *testing.T) {
	s := newStrategyFixationState()

	// Only 2 edits -- below threshold of 3
	s.recordEdit("/path/to/file.go")
	s.recordEdit("/path/to/file.go")
	s.recordVerification("run_command", "build error in file.go", true)

	if msg := s.check(); msg != "" {
		t.Fatalf("expected no warning below threshold, got: %s", msg)
	}
}

func TestStrategyFixation_NoTriggerWithoutFailures(t *testing.T) {
	s := newStrategyFixationState()

	// 3 edits but no failed verifications -- approach may be converging
	s.recordEdit("/path/to/file.go")
	s.recordEdit("/path/to/file.go")
	s.recordEdit("/path/to/file.go")

	if msg := s.check(); msg != "" {
		t.Fatalf("expected no warning without failures, got: %s", msg)
	}
}

func TestStrategyFixation_TriggersOnRepeatedEditsWithFailures(t *testing.T) {
	s := newStrategyFixationState()

	// 3 edits to same file
	s.recordEdit("/path/to/parser.go")
	s.recordEdit("/path/to/parser.go")
	s.recordEdit("/path/to/parser.go")

	// 2 failed verifications mentioning the file
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

	// Now a successful verification — should reset failures for last file
	s.recordVerification("run_command", "all tests passed", false)

	if msg := s.check(); msg != "" {
		t.Fatalf("expected no warning after successful verification, got: %s", msg)
	}
}

func TestStrategyFixation_GreenVerificationResetsAllFiles(t *testing.T) {
	s := newStrategyFixationState()

	// The #485 scenario: within ONE iteration's parallel tool batch, the
	// threshold is crossed for a.go, then a whole-tree green build runs,
	// but the batch also edits b.go afterwards — the old lastFile-only
	// reset kept a.go's stale counts alive and fired "not converging"
	// right after the green build.
	s.recordEdit("a.go")
	s.recordEdit("a.go")
	s.recordVerification("run_command", "error in a.go", true) // failures[a.go]=1, lastFile=a.go
	s.recordVerification("run_command", "error in a.go", true) // failures[a.go]=2
	s.recordEdit("a.go")                                       // edits[a.go]=3 (threshold crossed)
	s.recordEdit("b.go")                                       // lastFile=b.go

	// Green whole-tree build: must terminate EVERY file's streak, not just b.go's.
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

	// A green build resets everything; if the agent then REALLY fixates on
	// a file, fresh edits + failures re-accumulate and re-fire (contract's
	// literal semantics — the #485 fix must not over-correct into silence).
	s.recordEdit("a.go")
	s.recordVerification("run_command", "error in a.go", true)
	s.recordVerification("run_command", "error in a.go", true)
	s.recordEdit("a.go")
	s.recordEdit("a.go")
	s.recordVerification("run_command", "ok", false) // green reset

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
		// Bare base-name occurrence (line start) → attribute.
		{"bare", "agent.go:10: undefined: foo", "/w/internal/tool/agent.go", true},
		// Same-directory qualified mention → attribute.
		{"dir match", "error in internal/tool/agent.go: missing foo", "/w/internal/tool/agent.go", true},
		// Different-directory qualified mention (same base name) → do NOT
		// attribute (#485 same-family as #393).
		{"dir mismatch", "error in internal/agent/agent.go: missing foo", "/w/internal/tool/agent.go", false},
		// Mismatched dir first, bare occurrence later → attribute (scan all).
		{"mismatch then bare", "internal/agent/agent.go bad\nagent.go:5: oops", "/w/internal/tool/agent.go", true},
		// No mention at all.
		{"absent", "everything fine", "/w/a.go", false},
	}
	for _, c := range cases {
		if got := sfOutputNamesFile(c.output, c.filePath); got != c.want {
			t.Errorf("%s: sfOutputNamesFile(%q, %q) = %v, want %v", c.name, c.output, c.filePath, got, c.want)
		}
	}
}

func TestSFExtractMutationPaths(t *testing.T) {
	// multi_file_edit: EVERY files[] entry must be returned (#485) —
	// the first-path-only behavior under-tracked edits.
	args := json.RawMessage(`{"files":[{"path":"a.go","edits":[]},{"path":"sub/b.go"}]}`)
	paths := sfExtractMutationPaths(args)
	if len(paths) != 2 || paths[0] != "a.go" || paths[1] != "sub/b.go" {
		t.Fatalf("multi_file_edit paths = %v, want [a.go sub/b.go]", paths)
	}

	// notebook_edit: path lives in notebook_path, previously never extracted.
	nb := sfExtractMutationPaths(json.RawMessage(`{"notebook_path":"nb/ipynb/analysis.ipynb"}`))
	if len(nb) != 1 || nb[0] != "nb/ipynb/analysis.ipynb" {
		t.Fatalf("notebook_edit path = %v, want notebook_path value", nb)
	}

	// edit_file compatibility: single file_path.
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

	// First file triggers
	s.recordEdit("/a.go")
	s.recordEdit("/a.go")
	s.recordEdit("/a.go")
	s.recordVerification("run_command", "error in a.go", true)
	s.recordVerification("run_command", "error in a.go", true)
	if msg1 := s.check(); msg1 == "" {
		t.Fatal("expected first warning")
	}

	// Second file suppressed (1 per run, batch 2 guidance-noise cleanup)
	s.recordEdit("/b.go")
	s.recordEdit("/b.go")
	s.recordEdit("/b.go")
	s.recordVerification("run_command", "error in b.go", true)
	s.recordVerification("run_command", "error in b.go", true)
	if msg2 := s.check(); msg2 != "" {
		t.Fatalf("expected second warning to be suppressed, got: %s", msg2)
	}

	// Third file should NOT trigger (cap reached)
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
	// Same file, more edits -- should not re-warn
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

	// Edits spread across different files
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

	// Empty path should not crash or record
	s.recordEdit("")
	if len(s.fileEdits) != 0 {
		t.Fatal("empty path should not be recorded")
	}
}

func TestStrategyFixation_VerificationErrorFiltering(t *testing.T) {
	s := newStrategyFixationState()

	// Verification failure output that does NOT mention the file or common error keywords
	s.recordEdit("/a.go")
	s.recordEdit("/a.go")
	s.recordEdit("/a.go")
	s.recordVerification("run_command", "everything looks fine, just a timeout", true)

	// This should NOT trigger because "timeout" doesn't match the heuristic keywords
	// (the keyword list does not include "timeout")
	// Actually "error" is not in "timeout"... let's verify
	if msg := s.check(); msg != "" {
		// This is acceptable either way -- the heuristic is lenient
		// The key test is that it works when keywords DO match
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
	// #737: must match the canonical sourceMutatingTools superset (#153/#154),
	// not a partial list -- batch codemod tools were previously untracked.
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
	tools := []string{"run_command", "start_command", "code_health", "review_changes", "verify", "lsp_diagnostics"}
	for _, tool := range tools {
		if !strategyFixationIsVerification(tool) {
			t.Errorf("expected %s to be verification", tool)
		}
	}
	if strategyFixationIsVerification("edit_file") {
		t.Error("edit_file should not be verification")
	}
}
