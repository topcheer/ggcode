package vcs

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSubversionIsCleanIgnoresExternals(t *testing.T) {
	if _, err := exec.LookPath("svn"); err != nil {
		t.Skip("svn binary not installed")
	}
	base := t.TempDir()
	wc := filepath.Join(base, "wc")

	if out, err := runTestCmd("svnadmin", "create", filepath.Join(base, "repo")); err != nil {
		t.Skipf("svnadmin create failed: %v (%s)", err, out)
	}
	repoURL := "file://" + filepath.Join(base, "repo")
	if _, err := runTestCmd("svn", "checkout", repoURL, wc); err != nil {
		t.Skipf("svn checkout failed: %v", err)
	}
	if _, err := runTestCmd("svn", "propset", "svn:externals", "ext "+repoURL, wc); err != nil {
		t.Skipf("propset failed (needs repo Layout): %v", err)
	}
	if _, err := runTestCmd("svn", "commit", "-m", "ext", wc); err != nil {
		t.Skipf("commit failed: %v", err)
	}
	if _, err := runTestCmd("svn", "update", wc); err != nil {
		t.Skipf("update failed: %v", err)
	}
	_ = os.RemoveAll(filepath.Join(wc, "ext"))

	clean, err := Subversion{}.IsClean(context.Background(), wc)
	if err != nil {
		t.Fatal(err)
	}
	if !clean {
		out, _ := Subversion{}.Status(context.Background(), wc)
		t.Fatalf("externals-only working copy must be clean, status:\n%s", strings.TrimSpace(out))
	}
}

func runTestCmd(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func TestNormalizeSvnLogSingleLineEntries(t *testing.T) {
	raw := `------------------------------------------------------------------------
r42 | alice | 2026-09-01 10:00:00 +0800 (Mon, 01 Sep 2026) | 1 line

Fix login redirect
------------------------------------------------------------------------
r41 | bob | 2026-08-31 09:00:00 +0800 (Sun, 31 Aug 2026) | 2 lines

Refactor config loader

and tests
------------------------------------------------------------------------
`
	got := normalizeSvnLog(raw)
	lines := strings.Split(strings.TrimSpace(got), "\n")
	if len(lines) != 2 {
		t.Fatalf("want 2 one-line entries, got %d: %q", len(lines), got)
	}
	if !strings.Contains(lines[0], "r42") || !strings.Contains(lines[0], "Fix login redirect") {
		t.Errorf("entry 0 malformed: %q", lines[0])
	}
	if !strings.Contains(lines[1], "r41") || !strings.Contains(lines[1], "Refactor config loader") {
		t.Errorf("entry 1 malformed: %q", lines[1])
	}
	for _, l := range lines {
		if strings.Contains(l, "----") {
			t.Errorf("separator leaked into output: %q", l)
		}
	}
}
