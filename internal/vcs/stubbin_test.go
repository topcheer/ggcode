package vcs

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// This file covers the hg/jj/svn backends and the git error/edge branches that
// the integration tests cannot reach without the real binaries installed.
// Strategy: install fake `git`/`hg`/`jj`/`svn` executables at the front of
// PATH. The stub records each invocation ("$*") into $STUB_LOG, prints a
// subcommand-specific canned output from $STUB_* environment variables, and
// exits with the matching $STUB_*_CODE. This makes command construction and
// output parsing fully deterministic and binary-independent.

const stubSh = `#!/bin/sh
printf '%s\n' "$*" >> "$STUB_LOG"
out=""
code="${STUB_CODE:-0}"
matched=""
case "$1" in
	status)
		matched=1
		if [ "$2" = "--porcelain" ]; then
			out="$STUB_STATUS_PORCELAIN"; code="${STUB_STATUS_PORCELAIN_CODE:-0}"
		else
			out="$STUB_STATUS"; code="${STUB_STATUS_CODE:-0}"
		fi
		;;
	rev-parse)
		matched=1
		if [ "$2" = "--abbrev-ref" ]; then
			out="$STUB_BRANCH"; code="${STUB_BRANCH_CODE:-0}"
		else
			out="$STUB_SHA"; code="${STUB_SHA_CODE:-0}"
		fi
		;;
	rev-list)
		matched=1
		out="$STUB_AB"; code="${STUB_AB_CODE:-0}"
		;;
	diff)
		matched=1
		out="$STUB_DIFF"; code="${STUB_DIFF_CODE:-0}"
		;;
	describe)
		matched=1
		out="$STUB_DESC"; code="${STUB_DESC_CODE:-0}"
		;;
	bookmark)
		matched=1
		out="$STUB_BM"; code="${STUB_BM_CODE:-0}"
		;;
esac
if [ -z "$matched" ]; then
	out="$STUB_OUT"
	code="${STUB_CODE:-0}"
fi
printf '%s' "$out"
if [ "$code" != "0" ] && [ -n "$STUB_ERR" ]; then
	printf '%s' "$STUB_ERR" >&2
fi
exit "$code"
`

// installStub puts a fake executable with the given name at the front of PATH
// and returns the path of its invocation log.
func installStub(t *testing.T, name string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("PATH stubs require a POSIX shell")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(stubSh), 0o755); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(dir, "calls.log")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("STUB_LOG", logPath)
	return logPath
}

func readCalls(t *testing.T, logPath string) []string {
	t.Helper()
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read stub log: %v", err)
	}
	var calls []string
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if strings.TrimSpace(line) != "" {
			calls = append(calls, line)
		}
	}
	return calls
}

func stubCtx() context.Context { return context.Background() }

// --- Mercurial ---

func TestHgMetadata(t *testing.T) {
	hg := Mercurial{}
	if hg.Name() != "hg" || hg.DisplayName() != "Mercurial" {
		t.Fatalf("unexpected identity: %q / %q", hg.Name(), hg.DisplayName())
	}
}

func TestHgStatus(t *testing.T) {
	log := installStub(t, "hg")
	t.Setenv("STUB_STATUS", "M foo.txt\n")
	out, err := (Mercurial{}).Status(stubCtx(), t.TempDir())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !strings.Contains(out, "foo.txt") {
		t.Errorf("status output = %q, want foo.txt", out)
	}
	if calls := readCalls(t, log); len(calls) != 1 || calls[0] != "status" {
		t.Errorf("calls = %v, want [status]", calls)
	}
}

func TestHgDiffFileFilter(t *testing.T) {
	log := installStub(t, "hg")
	t.Setenv("STUB_DIFF", "patch\n")
	// hg has no staging area: cached must be ignored, file must be passed.
	out, err := (Mercurial{}).Diff(stubCtx(), t.TempDir(), true, "a.txt")
	if err != nil || out != "patch\n" {
		t.Fatalf("Diff = %q, %v", out, err)
	}
	if calls := readCalls(t, log); len(calls) != 1 || calls[0] != "diff -- a.txt" {
		t.Errorf("calls = %v, want [diff -- a.txt]", calls)
	}
}

func TestHgLogCounts(t *testing.T) {
	log := installStub(t, "hg")
	if _, err := (Mercurial{}).Log(stubCtx(), t.TempDir(), 0); err != nil {
		t.Fatalf("Log: %v", err)
	}
	calls := readCalls(t, log)
	if len(calls) != 1 || !strings.HasPrefix(calls[0], "log -l 10 ") {
		t.Errorf("default count call = %v, want log -l 10", calls)
	}
	if _, err := (Mercurial{}).Log(stubCtx(), t.TempDir(), 3); err != nil {
		t.Fatalf("Log: %v", err)
	}
	calls = readCalls(t, log)
	if !strings.HasPrefix(calls[1], "log -l 3 ") {
		t.Errorf("explicit count call = %q, want prefix log -l 3", calls[1])
	}
}

func TestHgAddCommit(t *testing.T) {
	log := installStub(t, "hg")
	if _, err := (Mercurial{}).Add(stubCtx(), t.TempDir(), []string{"a", "b"}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if _, err := (Mercurial{}).Commit(stubCtx(), t.TempDir(), "msg"); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	calls := readCalls(t, log)
	if calls[0] != "add -- a b" {
		t.Errorf("add call = %q", calls[0])
	}
	if calls[1] != "commit -m msg" {
		t.Errorf("commit call = %q", calls[1])
	}
}

func TestHgCurrentBranch(t *testing.T) {
	installStub(t, "hg")
	t.Setenv("STUB_OUT", " default \n")
	branch, err := (Mercurial{}).CurrentBranch(stubCtx(), t.TempDir())
	if err != nil || branch != "default" {
		t.Fatalf("CurrentBranch = %q, %v; want default", branch, err)
	}
}

func TestHgIsClean(t *testing.T) {
	installStub(t, "hg")
	dir := t.TempDir()
	t.Setenv("STUB_STATUS", "")
	clean, err := (Mercurial{}).IsClean(stubCtx(), dir)
	if err != nil || !clean {
		t.Fatalf("clean repo: clean=%v err=%v, want true/nil", clean, err)
	}
	t.Setenv("STUB_STATUS", "M x\n")
	clean, err = (Mercurial{}).IsClean(stubCtx(), dir)
	if err != nil || clean {
		t.Fatalf("dirty repo: clean=%v err=%v, want false/nil", clean, err)
	}
}

func TestHgCheckoutCreateThenUpdate(t *testing.T) {
	log := installStub(t, "hg")
	if _, err := (Mercurial{}).Checkout(stubCtx(), t.TempDir(), "feat", true, "abc123"); err != nil {
		t.Fatalf("Checkout: %v", err)
	}
	calls := readCalls(t, log)
	if len(calls) != 2 || calls[0] != "bookmark feat -r abc123" || calls[1] != "update feat" {
		t.Fatalf("calls = %v, want [bookmark feat -r abc123 update feat]", calls)
	}
}

func TestHgCheckoutCreateNoStartPoint(t *testing.T) {
	log := installStub(t, "hg")
	if _, err := (Mercurial{}).Checkout(stubCtx(), t.TempDir(), "feat", true, ""); err != nil {
		t.Fatalf("Checkout: %v", err)
	}
	if calls := readCalls(t, log); calls[0] != "bookmark feat" {
		t.Errorf("first call = %q, want 'bookmark feat'", calls[0])
	}
}

func TestHgCheckoutPlainUpdate(t *testing.T) {
	log := installStub(t, "hg")
	if _, err := (Mercurial{}).Checkout(stubCtx(), t.TempDir(), "main", false, ""); err != nil {
		t.Fatalf("Checkout: %v", err)
	}
	if calls := readCalls(t, log); len(calls) != 1 || calls[0] != "update main" {
		t.Errorf("calls = %v, want [update main]", calls)
	}
}

func TestHgCheckoutBookmarkFailureAbortsUpdate(t *testing.T) {
	log := installStub(t, "hg")
	t.Setenv("STUB_BM_CODE", "1")
	t.Setenv("STUB_BM", "")
	t.Setenv("STUB_ERR", "abort: unknown revision")
	_, err := (Mercurial{}).Checkout(stubCtx(), t.TempDir(), "feat", true, "bad")
	if err == nil || !strings.Contains(err.Error(), "unknown revision") {
		t.Fatalf("err = %v, want stderr propagated", err)
	}
	if calls := readCalls(t, log); len(calls) != 1 {
		t.Errorf("update must not run after bookmark failure, calls = %v", calls)
	}
}

// --- Jujutsu ---

func TestJjMetadata(t *testing.T) {
	jj := Jujutsu{}
	if jj.Name() != "jj" || jj.DisplayName() != "Jujutsu" {
		t.Fatalf("unexpected identity: %q / %q", jj.Name(), jj.DisplayName())
	}
}

func TestJjStatusSummaryPreferred(t *testing.T) {
	log := installStub(t, "jj")
	t.Setenv("STUB_DIFF", "M src/a.go\nA b.txt\n")
	out, err := (Jujutsu{}).Status(stubCtx(), t.TempDir())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !strings.Contains(out, "src/a.go") || !strings.Contains(out, "b.txt") {
		t.Errorf("status = %q, want summary lines", out)
	}
	calls := readCalls(t, log)
	if len(calls) != 1 || calls[0] != "diff -r @ --summary" {
		t.Errorf("calls = %v, want [diff -r @ --summary]", calls)
	}
}

func TestJjStatusFallsBackToSt(t *testing.T) {
	log := installStub(t, "jj")
	t.Setenv("STUB_DIFF_CODE", "1")
	t.Setenv("STUB_OUT", "Working copy changes\n")
	out, err := (Jujutsu{}).Status(stubCtx(), t.TempDir())
	if err != nil || !strings.Contains(out, "Working copy changes") {
		t.Fatalf("Status = %q, %v", out, err)
	}
	calls := readCalls(t, log)
	if len(calls) != 2 || calls[1] != "st" {
		t.Errorf("calls = %v, want fallback to st", calls)
	}
}

func TestJjDiffFileFilter(t *testing.T) {
	log := installStub(t, "jj")
	if _, err := (Jujutsu{}).Diff(stubCtx(), t.TempDir(), false, "f.go"); err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if calls := readCalls(t, log); calls[0] != "diff -- f.go" {
		t.Errorf("call = %q, want 'diff -- f.go'", calls[0])
	}
}

func TestJjLogCounts(t *testing.T) {
	log := installStub(t, "jj")
	if _, err := (Jujutsu{}).Log(stubCtx(), t.TempDir(), -1); err != nil {
		t.Fatalf("Log: %v", err)
	}
	calls := readCalls(t, log)
	if !strings.HasPrefix(calls[0], "log -n 10 --no-graph") {
		t.Errorf("default call = %q, want -n 10", calls[0])
	}
	if _, err := (Jujutsu{}).Log(stubCtx(), t.TempDir(), 2); err != nil {
		t.Fatalf("Log: %v", err)
	}
	calls = readCalls(t, log)
	if !strings.HasPrefix(calls[1], "log -n 2 --no-graph") {
		t.Errorf("explicit call = %q, want -n 2", calls[1])
	}
}

func TestJjAddTracksFiles(t *testing.T) {
	log := installStub(t, "jj")
	if _, err := (Jujutsu{}).Add(stubCtx(), t.TempDir(), []string{"x"}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if calls := readCalls(t, log); calls[0] != "file track -- x" {
		t.Errorf("call = %q, want 'file track -- x'", calls[0])
	}
}

func TestJjCommitDescribeThenNew(t *testing.T) {
	log := installStub(t, "jj")
	t.Setenv("STUB_DESC", "described\n")
	t.Setenv("STUB_OUT", "new change\n")
	out, err := (Jujutsu{}).Commit(stubCtx(), t.TempDir(), "hello")
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if !strings.Contains(out, "described") || !strings.Contains(out, "new change") {
		t.Errorf("Commit output = %q, want both outputs", out)
	}
	calls := readCalls(t, log)
	if len(calls) != 2 || calls[0] != "describe -m hello" || calls[1] != "new" {
		t.Errorf("calls = %v, want [describe -m hello new]", calls)
	}
}

func TestJjCommitDescribeFailure(t *testing.T) {
	log := installStub(t, "jj")
	t.Setenv("STUB_DESC_CODE", "1")
	t.Setenv("STUB_ERR", "boom")
	if _, err := (Jujutsu{}).Commit(stubCtx(), t.TempDir(), "x"); err == nil {
		t.Fatal("want describe failure to propagate")
	}
	if calls := readCalls(t, log); len(calls) != 1 {
		t.Errorf("new must not run after describe failure, calls = %v", calls)
	}
}

func TestJjCurrentBranch(t *testing.T) {
	installStub(t, "jj")
	t.Setenv("STUB_OUT", " abcdef \n")
	branch, err := (Jujutsu{}).CurrentBranch(stubCtx(), t.TempDir())
	if err != nil || branch != "abcdef" {
		t.Fatalf("CurrentBranch = %q, %v", branch, err)
	}
	t.Setenv("STUB_OUT", "\n")
	branch, err = (Jujutsu{}).CurrentBranch(stubCtx(), t.TempDir())
	if err != nil || branch != "main" {
		t.Fatalf("empty change id fallback = %q, %v; want main", branch, err)
	}
}

func TestJjIsCleanAllPaths(t *testing.T) {
	installStub(t, "jj")
	dir := t.TempDir()

	t.Setenv("STUB_DIFF", "")
	clean, err := (Jujutsu{}).IsClean(stubCtx(), dir)
	if err != nil || !clean {
		t.Fatalf("summary empty: clean=%v err=%v, want true/nil", clean, err)
	}

	t.Setenv("STUB_DIFF", "M x\n")
	clean, err = (Jujutsu{}).IsClean(stubCtx(), dir)
	if err != nil || clean {
		t.Fatalf("summary dirty: clean=%v err=%v, want false/nil", clean, err)
	}

	// Old jj (<0.26): --summary unsupported; both historical wordings of the
	// clean marker must be accepted (#1853).
	t.Setenv("STUB_DIFF_CODE", "1")
	for _, wording := range []string{
		"Working copy changes: The working copy has no changes",
		"The working copy is clean",
	} {
		t.Setenv("STUB_OUT", "some prose\n"+wording+"\n")
		clean, err = (Jujutsu{}).IsClean(stubCtx(), dir)
		if err != nil || !clean {
			t.Fatalf("fallback %q: clean=%v err=%v, want true/nil", wording, clean, err)
		}
	}

	// Fallback path with a non-clean prose output stays dirty.
	t.Setenv("STUB_OUT", "Working copy changes:\nM file\n")
	clean, err = (Jujutsu{}).IsClean(stubCtx(), dir)
	if err != nil || clean {
		t.Fatalf("fallback dirty prose: clean=%v err=%v, want false/nil", clean, err)
	}

	// Both commands failing is an error, never a silent clean.
	t.Setenv("STUB_OUT", "")
	t.Setenv("STUB_CODE", "1")
	t.Setenv("STUB_ERR", "no repo")
	clean, err = (Jujutsu{}).IsClean(stubCtx(), dir)
	if err == nil || clean {
		t.Fatalf("both fail: clean=%v err=%v, want false/error", clean, err)
	}
}

func TestJjCheckout(t *testing.T) {
	log := installStub(t, "jj")
	if _, err := (Jujutsu{}).Checkout(stubCtx(), t.TempDir(), "feat", true, "abc"); err != nil {
		t.Fatalf("Checkout create: %v", err)
	}
	calls := readCalls(t, log)
	if len(calls) != 2 || calls[0] != "bookmark create feat -r abc" || calls[1] != "new feat" {
		t.Fatalf("create calls = %v, want [bookmark create feat -r abc new feat]", calls)
	}

	log = installStub(t, "jj")
	if _, err := (Jujutsu{}).Checkout(stubCtx(), t.TempDir(), "feat", false, ""); err != nil {
		t.Fatalf("Checkout switch: %v", err)
	}
	if calls := readCalls(t, log); len(calls) != 1 || calls[0] != "new feat" {
		t.Errorf("switch calls = %v, want [new feat]", calls)
	}
}

func TestJjCheckoutBookmarkFailure(t *testing.T) {
	log := installStub(t, "jj")
	t.Setenv("STUB_BM_CODE", "1")
	t.Setenv("STUB_BM", "")
	if _, err := (Jujutsu{}).Checkout(stubCtx(), t.TempDir(), "feat", true, ""); err == nil {
		t.Fatal("want bookmark failure to propagate")
	}
	if calls := readCalls(t, log); len(calls) != 1 {
		t.Errorf("switch must not run after bookmark failure, calls = %v", calls)
	}
}

// --- Subversion ---

func TestSvnMetadata(t *testing.T) {
	svn := Subversion{}
	if svn.Name() != "svn" || svn.DisplayName() != "Subversion" {
		t.Fatalf("unexpected identity: %q / %q", svn.Name(), svn.DisplayName())
	}
}

func TestSvnStatus(t *testing.T) {
	log := installStub(t, "svn")
	t.Setenv("STUB_STATUS", "M a\n")
	if _, err := (Subversion{}).Status(stubCtx(), t.TempDir()); err != nil {
		t.Fatalf("Status: %v", err)
	}
	if calls := readCalls(t, log); calls[0] != "status" {
		t.Errorf("call = %q, want status", calls[0])
	}
}

func TestSvnDiffIgnoresCached(t *testing.T) {
	log := installStub(t, "svn")
	if _, err := (Subversion{}).Diff(stubCtx(), t.TempDir(), true, "f"); err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if calls := readCalls(t, log); calls[0] != "diff -- f" {
		t.Errorf("call = %q, want 'diff -- f'", calls[0])
	}
}

func TestSvnLogNormalizesAndCounts(t *testing.T) {
	log := installStub(t, "svn")
	sep := strings.Repeat("-", 72)
	t.Setenv("STUB_OUT", strings.Join([]string{
		sep, "r3 | alice | 2026-01-03 | 1", "", "newest change", "", sep,
		"r2 | bob | 2026-01-02 | 1", "", "middle change", "", sep,
	}, "\n"))
	out, err := (Subversion{}).Log(stubCtx(), t.TempDir(), 2)
	if err != nil {
		t.Fatalf("Log: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 2 {
		t.Fatalf("log lines = %d (%q), want 2 normalized entries", len(lines), out)
	}
	if !strings.HasPrefix(lines[0], "r3 | alice | 2026-01-03 | 1 | newest change") {
		t.Errorf("entry format = %q, want header + ' | ' + first message line", lines[0])
	}
	calls := readCalls(t, log)
	if !strings.Contains(calls[0], "log -r HEAD:1 -l 2") {
		t.Errorf("call = %q, want newest-first traversal with -l 2", calls[0])
	}
	if _, err := (Subversion{}).Log(stubCtx(), t.TempDir(), 0); err != nil {
		t.Fatalf("Log default: %v", err)
	}
	if calls := readCalls(t, log); !strings.Contains(calls[1], "-l 10") {
		t.Errorf("default call = %q, want -l 10", calls[1])
	}
}

func TestSvnAddCommit(t *testing.T) {
	log := installStub(t, "svn")
	if _, err := (Subversion{}).Add(stubCtx(), t.TempDir(), []string{"f"}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if _, err := (Subversion{}).Commit(stubCtx(), t.TempDir(), "m"); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	calls := readCalls(t, log)
	if calls[0] != "add -- f" {
		t.Errorf("add call = %q", calls[0])
	}
	if calls[1] != "commit -m m --non-interactive" {
		t.Errorf("commit call = %q, want --non-interactive flag", calls[1])
	}
}

func TestSvnCurrentBranchURLParsing(t *testing.T) {
	installStub(t, "svn")
	for url, want := range map[string]string{
		"https://svn.example.com/repo/branches/feature-x": "feature-x",
		"https://svn.example.com/repo/trunk":              "trunk",
		"https://svn.example.com/repo":                    "repo",
	} {
		t.Setenv("STUB_OUT", url+"\n")
		got, err := (Subversion{}).CurrentBranch(stubCtx(), t.TempDir())
		if err != nil || got != want {
			t.Errorf("url %s: got %q err %v, want %q", url, got, err, want)
		}
	}

	installStub(t, "svn")
	t.Setenv("STUB_CODE", "1")
	t.Setenv("STUB_ERR", "not a working copy")
	if _, err := (Subversion{}).CurrentBranch(stubCtx(), t.TempDir()); err == nil {
		t.Error("want error on svn info failure")
	}
}

func TestSvnIsCleanFiltersExternals(t *testing.T) {
	installStub(t, "svn")
	dir := t.TempDir()
	cases := []struct {
		name   string
		status string
		want   bool
	}{
		{"empty", "", true},
		{"externals-def", "X vendor/lib\n", true},
		{"externals-progress", "> vendor/lib\n", true},
		{"recursive-external", "Performing status on external item at 'vendor'.\n", true},
		{"local-mod", "M src/a.go\n", false},
		{"unversioned-counts", "? notes.txt\n", false},
		{"externals-plus-mod", "X vendor\nM src/a.go\n", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("STUB_STATUS", tc.status)
			got, err := (Subversion{}).IsClean(stubCtx(), dir)
			if err != nil || got != tc.want {
				t.Errorf("status %q: got %v err %v, want %v", tc.status, got, err, tc.want)
			}
		})
	}

	installStub(t, "svn")
	t.Setenv("STUB_STATUS_CODE", "1")
	t.Setenv("STUB_ERR", "not a working copy")
	if clean, err := (Subversion{}).IsClean(stubCtx(), dir); err == nil || clean {
		t.Errorf("status failure: got %v err %v, want false/error", clean, err)
	}
}

func TestSvnCheckoutNotSupported(t *testing.T) {
	_, err := (Subversion{}).Checkout(stubCtx(), t.TempDir(), "b", false, "")
	if !errors.Is(err, ErrCheckoutNotSupported) {
		t.Fatalf("err = %v, want ErrCheckoutNotSupported", err)
	}
}

// --- Git branches not covered by the integration tests ---

func TestGitCheckout(t *testing.T) {
	log := installStub(t, "git")

	if _, err := (Git{}).Checkout(stubCtx(), t.TempDir(), "feat", true, "abc"); err != nil {
		t.Fatalf("create with start point: %v", err)
	}
	calls := readCalls(t, log)
	if calls[0] != "checkout -b feat abc" {
		t.Errorf("call = %q, want 'checkout -b feat abc'", calls[0])
	}

	if _, err := (Git{}).Checkout(stubCtx(), t.TempDir(), "feat", true, ""); err != nil {
		t.Fatalf("create without start point: %v", err)
	}
	calls = readCalls(t, log)
	if calls[1] != "checkout -b feat" {
		t.Errorf("call = %q, want 'checkout -b feat'", calls[1])
	}

	if _, err := (Git{}).Checkout(stubCtx(), t.TempDir(), "main", false, ""); err != nil {
		t.Fatalf("switch: %v", err)
	}
	calls = readCalls(t, log)
	if calls[2] != "checkout main" {
		t.Errorf("call = %q, want 'checkout main'", calls[2])
	}
}

func TestGitAheadBehind(t *testing.T) {
	installStub(t, "git")
	dir := t.TempDir()

	// rev-list --left-right --count @{upstream}...HEAD prints "<behind>\t<ahead>".
	cases := []struct {
		out           string
		ahead, behind int
		ok            bool
		code          string
	}{
		{out: "0\t2\n", ahead: 2, behind: 0, ok: true},
		{out: "3\t0\n", ahead: 0, behind: 3, ok: true},
		{out: "1\t4", ahead: 4, behind: 1, ok: true},
		{out: "0\t0\n", ahead: 0, behind: 0, ok: true},
		{out: "only-one-field\n", ok: false},
		{out: "a\tb\n", ok: false},
		{code: "1", ok: false}, // no upstream configured
	}
	for _, tc := range cases {
		t.Run(tc.out, func(t *testing.T) {
			t.Setenv("STUB_AB", tc.out)
			if tc.code != "" {
				t.Setenv("STUB_AB_CODE", tc.code)
			}
			ahead, behind, ok := (Git{}).AheadBehind(stubCtx(), dir)
			if ahead != tc.ahead || behind != tc.behind || ok != tc.ok {
				t.Errorf("out=%q: got (%d,%d,%v), want (%d,%d,%v)",
					tc.out, ahead, behind, ok, tc.ahead, tc.behind, tc.ok)
			}
		})
	}
}

func TestGitIsCleanError(t *testing.T) {
	installStub(t, "git")
	t.Setenv("STUB_STATUS_PORCELAIN_CODE", "1")
	t.Setenv("STUB_ERR", "not a git repository")
	clean, err := (Git{}).IsClean(stubCtx(), t.TempDir())
	if err == nil || clean || !strings.Contains(err.Error(), "not a git repository") {
		t.Fatalf("got (%v, %v), want (false, stderr error)", clean, err)
	}
}

func TestGitDiffFileFilter(t *testing.T) {
	log := installStub(t, "git")
	t.Setenv("STUB_DIFF", "patch\n")
	out, err := (Git{}).Diff(stubCtx(), t.TempDir(), false, "a.go")
	if err != nil || out != "patch\n" {
		t.Fatalf("Diff = %q, %v", out, err)
	}
	if calls := readCalls(t, log); calls[0] != "diff -- a.go" {
		t.Errorf("call = %q, want 'diff -- a.go'", calls[0])
	}
}

func TestGitLogDefaultCount(t *testing.T) {
	log := installStub(t, "git")
	if _, err := (Git{}).Log(stubCtx(), t.TempDir(), 0); err != nil {
		t.Fatalf("Log: %v", err)
	}
	if calls := readCalls(t, log); !strings.HasSuffix(calls[0], "-10") {
		t.Errorf("call = %q, want default -10", calls[0])
	}
}

func TestGitCurrentBranchDetachedHead(t *testing.T) {
	installStub(t, "git")
	t.Setenv("STUB_BRANCH", "HEAD")
	t.Setenv("STUB_SHA", "9e99288f\n")
	branch, err := (Git{}).CurrentBranch(stubCtx(), t.TempDir())
	if err != nil || branch != "9e99288f" {
		t.Fatalf("detached HEAD = %q, %v; want short sha", branch, err)
	}

	// Empty short sha keeps the literal HEAD label rather than an empty one.
	t.Setenv("STUB_SHA", "")
	branch, err = (Git{}).CurrentBranch(stubCtx(), t.TempDir())
	if err != nil || branch != "HEAD" {
		t.Fatalf("empty sha fallback = %q, %v; want HEAD", branch, err)
	}
}

// --- runVCSCmd error paths ---

func TestRunVCSCmdExitErrorIncludesStderr(t *testing.T) {
	installStub(t, "git")
	t.Setenv("STUB_STATUS_CODE", "128")
	t.Setenv("STUB_ERR", "fatal: bad object")
	_, err := (Git{}).Status(stubCtx(), t.TempDir())
	if err == nil {
		t.Fatal("want error")
	}
	if !strings.Contains(err.Error(), "git status --short") ||
		!strings.Contains(err.Error(), "fatal: bad object") {
		t.Errorf("err = %v, want command name + stderr", err)
	}
}

func TestRunVCSCmdMissingBinary(t *testing.T) {
	// Not on PATH: exec fails with a non-ExitError, which must wrap the cause.
	_, err := runVCSCmd(stubCtx(), t.TempDir(), "definitely-not-a-real-vcs-binary", "status")
	if err == nil {
		t.Fatal("want error")
	}
	if !strings.Contains(err.Error(), "definitely-not-a-real-vcs-binary") {
		t.Errorf("err = %v, want wrapped lookup failure", err)
	}
}

// --- Summary branches ---

func TestSummaryNotARepo(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("relies on temp dir not living inside a repo")
	}
	got := Summary(stubCtx(), t.TempDir())
	if got != "not a version-controlled repository" {
		t.Errorf("Summary = %q", got)
	}
}

func TestSummaryGitAheadBehind(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("PATH stubs require a POSIX shell")
	}
	installStub(t, "git")
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("STUB_BRANCH", "main\n")
	t.Setenv("STUB_STATUS_PORCELAIN", "")
	t.Setenv("STUB_AB", "3\t2\n")

	// rev-list --left-right --count @{upstream}...HEAD prints "<behind>\t<ahead>".
	got := Summary(stubCtx(), dir)
	for _, want := range []string{"in a Git repository", "on main", "clean working tree", "3 behind", "2 ahead"} {
		if !strings.Contains(got, want) {
			t.Errorf("Summary = %q, want substring %q", got, want)
		}
	}
}

func TestSummaryGitDirtyCount(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("PATH stubs require a POSIX shell")
	}
	installStub(t, "git")
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("STUB_BRANCH", "main\n")
	t.Setenv("STUB_STATUS_PORCELAIN", " M a.go\n?? b.txt\n")
	t.Setenv("STUB_STATUS", " M a.go\n?? b.txt\n")
	t.Setenv("STUB_AB_CODE", "1") // no upstream

	got := Summary(stubCtx(), dir)
	if !strings.Contains(got, "2 uncommitted file(s)") {
		t.Errorf("Summary = %q, want dirty count 2", got)
	}
}

func TestDetectOrGitReturnsDetectedVCS(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, ".hg"), 0o755); err != nil {
		t.Fatal(err)
	}
	if v := DetectOrGit(dir); v.Name() != "hg" {
		t.Errorf("DetectOrGit = %v, want hg (detected, not fallback)", v)
	}
}
