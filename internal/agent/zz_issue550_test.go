package agent

// Feature tests for issue #550 (ver-37 probe findings): D1/D2 change
// reconciliation, B1 verify-coverage short-dir scopes, A2/A3 scope-narrow
// outcome gating, C1 cross-file-impact sibling exclusion.
//
// NOTE: zz_ver37_probe_test.go belongs to a parallel review session and
// asserts PRE-fix behavior for some of these; it is intentionally left
// untouched here — its owner cleans it up.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --- D1: absolute-path double prefix in normalizeReconcilePath ---

func TestIssue550_NormalizeReconcilePath_AbsoluteMatchesRelative(t *testing.T) {
	wd := "/w/repo"
	abs := normalizeReconcilePath(wd, "/w/repo/internal/agent/foo.go")
	rel := normalizeReconcilePath(wd, "internal/agent/foo.go")
	if abs != rel {
		t.Fatalf("D1: absolute and git-relative forms of the same file must normalize equal: abs=%q rel=%q", abs, rel)
	}
	if rel != "internal/agent/foo.go" {
		t.Fatalf("D1: expected repo-root-relative canonical form, got %q", rel)
	}
}

func TestIssue550_NormalizeReconcilePath_OutsideRepoStaysAbsolute(t *testing.T) {
	got := normalizeReconcilePath("/w/repo", "/tmp/other/x.go")
	if got != "/tmp/other/x.go" {
		t.Fatalf("D1: path outside workingDir must keep its absolute form, got %q", got)
	}
}

func TestIssue550_NormalizeReconcilePath_TrimsPadding(t *testing.T) {
	if got := normalizeReconcilePath("/w/repo", " internal/agent/foo.go\n"); got != "internal/agent/foo.go" {
		t.Fatalf("D2: whitespace-padded path must normalize cleanly, got %q", got)
	}
}

func TestIssue550_ReconcileAbsolutePathEditNotFlagged(t *testing.T) {
	dir := initGitRepo(t)
	foo := filepath.Join(dir, "internal", "agent", "foo.go")
	if err := os.MkdirAll(filepath.Dir(foo), 0o755); err != nil {
		t.Fatal(err)
	}
	mustWriteCR(t, foo, "package agent\n")
	runGitCR(t, dir, "add", ".")
	runGitCR(t, dir, "commit", "-m", "init")

	mustWriteCR(t, foo, "package agent\n\nfunc Foo() {}\n")

	a := &Agent{changeReconcile: newChangeReconcileState(), workingDir: dir}
	stats := &RunStats{
		ToolCalls:   map[string]int{"edit_file": 1},
		FilesEdited: []string{foo}, // absolute, as edit_file records it
	}
	msg := a.checkChangeReconcile(stats)
	if msg != "" {
		t.Fatalf("D1: absolute-path edit of the only changed file must reconcile, got: %s", msg)
	}
}

// --- B1: short dir token inflating to ALL ---

func TestIssue550_ShortDirTokenIsScopedNotAll(t *testing.T) {
	cases := []struct {
		cmd  string
		want []string
	}{
		{"go test ./db/", []string{"db"}},
		{"go build ./a", []string{"a"}},
		{"go test ./x/y/", []string{"x/y"}},
		{"go test ./...", []string{"ALL"}},
		{"go test", []string{"."}},
	}
	for _, c := range cases {
		got := coverageExtractVerifyScopes(c.cmd)
		if len(got) != len(c.want) {
			t.Errorf("B1: coverageExtractVerifyScopes(%q) = %v, want %v", c.cmd, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("B1: coverageExtractVerifyScopes(%q)[%d] = %q, want %q", c.cmd, i, got[i], c.want[i])
			}
		}
	}
}

func TestIssue550_ShortDirScopeDoesNotVerifyUnrelatedPackages(t *testing.T) {
	s := newEditCoverageState()
	s.recordToolCall("edit_file", `{"file_path":"/w/repo/internal/agent/a.go"}`)
	s.recordToolCall("edit_file", `{"file_path":"/w/repo/internal/config/b.go"}`)
	msg := s.recordToolCall("run_command", `{"command":"go test ./db/"}`)
	if msg == "" {
		t.Fatal("B1: `go test ./db/` must not mark unrelated edited packages VERIFIED (ALL inflation)")
	}
	for _, pkg := range []string{"internal/agent", "internal/config"} {
		if !strings.Contains(msg, pkg) {
			t.Errorf("B1: warning must list %s as UNVERIFIED, got: %s", pkg, msg)
		}
	}
}

// --- A2/A3: scope-narrow outcome text and exit-code gating ---

func TestIssue550_ScopeNarrowMessageReportsRealOutcome(t *testing.T) {
	s := newScopeNarrowState()
	s.recordVerificationCommand("run_command", "go test ./...", "FAIL  pkg [build failed]", true)
	s.recordVerificationCommand("run_command", "go test ./internal/agent/", "ok  internal/agent", false)
	msg := s.recordVerificationCommand("run_command", "go test -run TestZ ./internal/agent/", "FAIL  TestZ", true)
	if msg == "" {
		t.Fatal("A2: narrowing with middle-pass must still fire")
	}
	if !strings.Contains(msg, "go test -run TestZ ./internal/agent/` -> failed") {
		t.Fatalf("A2: newest command actually FAILED — message must not claim passed: %s", msg)
	}
	if !strings.Contains(msg, "go test ./internal/agent/` -> passed") {
		t.Fatalf("A2: middle command passed — message should say passed: %s", msg)
	}
}

func TestIssue550_PassedGatedByExitCodeNotTestNames(t *testing.T) {
	s := newScopeNarrowState()
	out := "ok  internal/agent  0.5s --- PASS: TestHandleErrorFallback"
	s.recordVerificationCommand("run_command", "go test ./...", out, false)
	if !s.history[len(s.history)-1].passed {
		t.Fatal("A3: exit-code-0 run of a test whose NAME contains 'Error' must be judged passed")
	}

	// Whole-trajectory guard: three passing runs with Error-ish names must
	// not be misread as failures (previously silenced the detector).
	s2 := newScopeNarrowState()
	s2.recordVerificationCommand("run_command", "go test ./...", out, false)
	s2.recordVerificationCommand("run_command", "go test ./internal/agent/", out, false)
	if msg := s2.recordVerificationCommand("run_command", "go test -run TestHandleErrorFallback ./internal/agent/", "ok  0.1s", false); msg != "" {
		t.Fatalf("A3: all-pass trajectory must not fire despite Error-named tests: %s", msg)
	}
}

// --- C1: sibling scan must exclude co-edited files ---

func TestIssue550_CrossImpactStillFiresForUneditedSibling(t *testing.T) {
	dir := initGitRepo(t)
	aGo := filepath.Join(dir, "a.go")
	bGo := filepath.Join(dir, "b.go")
	mustWriteCR(t, aGo, "package p\n\nfunc Shared() {}\n")
	mustWriteCR(t, bGo, "package p\n\nfunc Use() { Shared() }\n")
	runGitCR(t, dir, "add", ".")
	runGitCR(t, dir, "commit", "-m", "init")

	// Agent removes Shared from a.go; sibling b.go (NOT edited) still
	// references it — this must still be flagged.
	mustWriteCR(t, aGo, "package p\n\nfunc Other() {}\n")

	ag := &Agent{crossFileImpact: newCrossFileImpactState(), workingDir: dir}
	msg := ag.checkCrossFileImpact(&RunStats{FilesEdited: []string{"a.go"}})
	if msg == "" {
		t.Fatal("C1: unedited sibling referencing a removed symbol must still be flagged")
	}
	if !strings.Contains(msg, "b.go") {
		t.Fatalf("C1: expected b.go listed as affected, got: %s", msg)
	}
}

func TestIssue550_CrossImpactExcludesCoEditedSibling(t *testing.T) {
	dir := initGitRepo(t)
	aGo := filepath.Join(dir, "a.go")
	bGo := filepath.Join(dir, "b.go")
	mustWriteCR(t, aGo, "package p\n\nfunc Shared() {}\n")
	mustWriteCR(t, bGo, "package p\n\nfunc Use() { Shared() }\n")
	runGitCR(t, dir, "add", ".")
	runGitCR(t, dir, "commit", "-m", "init")

	// Symbol MOVE: definition leaves a.go and lands in b.go — both files
	// edited this run, nothing unedited references the removed symbol.
	mustWriteCR(t, aGo, "package p\n\nfunc Other() {}\n")
	mustWriteCR(t, bGo, "package p\n\nfunc Shared() {}\n\nfunc Use() { Shared() }\n")

	ag := &Agent{crossFileImpact: newCrossFileImpactState(), workingDir: dir}
	msg := ag.checkCrossFileImpact(&RunStats{FilesEdited: []string{"a.go", "b.go"}})
	if msg != "" {
		t.Fatalf("C1: co-edited sibling must be excluded from the scan (symbol-move false positive), got: %s", msg)
	}
}
