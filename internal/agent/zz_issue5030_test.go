package agent

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Issue #5030: the adversarial review gate (codeChangedInRun) keys on
// sourceMutatingTools, but extractPathsFromToolCall had no case for
// lsp_rename, so an lsp_rename-only run passed the gate with empty
// RunStats.FilesEdited and collectAdversarialDiff fell back to the whole
// shared-tree diff — re-introducing the #5018 cross-agent misattribution
// in that window. Every gate-passing tool must record its edited paths.

func gitCmd5030(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// TestIssue5030GateToolsRecordPaths is the drift guard: every tool that
// passes the adversarial gate (sourceMutatingTools) must populate
// FilesEdited, otherwise the scoped diff silently degrades to the
// whole-tree fallback on shared worktrees.
func TestIssue5030GateToolsRecordPaths(t *testing.T) {
	cases := []struct {
		tool string
		args string
	}{
		{"edit_file", `{"path":"a.go","new_text":"x"}`},
		{"write_file", `{"path":"b.go","content":"x"}`},
		{"multi_edit_file", `{"path":"c.go","edits":[]}`},
		{"multi_file_edit", `{"files":[{"path":"d.go","new_text":"x"}]}`},
		{"multi_file_write", `{"files":[{"path":"e.go","content":"x"}]}`},
		{"batch_replace", `{"pattern":"x","replacement":"y","files":["f.go"]}`},
		{"lsp_rename", `{"path":"g.go","line":3,"character":7,"new_name":"H"}`},
		{"file_ops", `{"operations":[{"action":"move","source":"h.go","destination":"i.go"}]}`},
		{"notebook_edit", `{"notebook_path":"j.ipynb","new_source":["x"]}`},
	}
	for _, tc := range cases {
		var stats RunStats
		stats.recordToolCall(tc.tool)
		extractPathsFromToolCall(tc.tool, []byte(tc.args), &stats)
		if !codeChangedInRun(&stats) {
			t.Errorf("%s must pass the adversarial gate (sourceMutatingTools)", tc.tool)
		}
		if len(stats.FilesEdited) == 0 {
			t.Errorf("%s passes the gate but records no FilesEdited (#5030): whole-tree fallback would misattribute shared-tree edits", tc.tool)
		}
	}
}

// TestIssue5030EveryGateToolHasPathRecorder iterates sourceMutatingTools
// itself with a superset of all known path-arg keys: any FUTURE tool added
// to the gate map without a path-recording case in extractPathsFromToolCall
// fails here instead of silently degrading to the whole-tree fallback.
func TestIssue5030EveryGateToolHasPathRecorder(t *testing.T) {
	super := `{"path":"p1.go","file_path":"p2.go","files":["p3.go",{"path":"p7.go"}],"notebook_path":"p4.ipynb","operations":[{"action":"move","source":"p5.go","destination":"p6.go"}]}`
	for tool := range sourceMutatingTools {
		var stats RunStats
		stats.recordToolCall(tool)
		extractPathsFromToolCall(tool, []byte(super), &stats)
		if !codeChangedInRun(&stats) {
			t.Errorf("%s must pass the adversarial gate", tool)
		}
		if len(stats.FilesEdited) == 0 {
			t.Errorf("%s is in sourceMutatingTools but records no FilesEdited (#5030): add an extractPathsFromToolCall case", tool)
		}
	}
}

// TestIssue5030LspRenameRunScopedDiff reproduces the reported scenario with
// the lsp_rename path populated: a shared tree where this agent renamed in
// mine.go while another agent has theirs.go in flight — the adversarial diff
// must not contain the foreign edit.
func TestIssue5030LspRenameRunScopedDiff(t *testing.T) {
	dir := t.TempDir()
	gitCmd5030(t, dir, "init", "-q")
	os.WriteFile(filepath.Join(dir, "mine.go"), []byte("package p\n\nconst A = 1\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "theirs.go"), []byte("package p\n\nconst B = 2\n"), 0o644)
	gitCmd5030(t, dir, "add", ".")
	gitCmd5030(t, dir, "commit", "-q", "-m", "base")

	os.WriteFile(filepath.Join(dir, "mine.go"), []byte("package p\n\nconst A2 = 1\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "theirs.go"), []byte("package p\n\nconst B = 99\n"), 0o644)

	var stats RunStats
	extractPathsFromToolCall("lsp_rename", []byte(`{"path":"mine.go","line":3,"character":7,"new_name":"A2"}`), &stats)
	if len(stats.FilesEdited) == 0 {
		t.Fatal("lsp_rename must record FilesEdited")
	}

	a := &Agent{}
	a.SetWorkingDir(dir)
	diff := a.collectAdversarialDiff(stats.FilesEdited)
	if !strings.Contains(diff, "const A2 = 1") {
		t.Fatalf("scoped diff must contain this run's lsp_rename edit to mine.go:\n%s", diff)
	}
	if strings.Contains(diff, "const B = 99") {
		t.Fatalf("scoped diff must NOT attribute another agent's theirs.go edit (#5030):\n%s", diff)
	}
}
