package vcs

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSubversionIsCleanIgnoresExternals pins #1407-B: 'X' (svn:externals
// definition) lines are NOT local modifications - without filtering, a
// working copy with externals reports dirty forever despite zero local
// changes. Builds a real svn checkout with an externals property via the
// svn binary (skips when unavailable).
func TestSubversionIsCleanIgnoresExternals(t *testing.T) {
	if _, err := exec.LookPath("svn"); err != nil {
		t.Skip("svn binary not installed")
	}
	base := t.TempDir()
	wc := filepath.Join(base, "wc")
	// Create a repo + checkout with one committed externals definition.
	if out, err := runTestCmd("svnadmin", "create", filepath.Join(base, "repo")); err != nil {
		t.Skipf("svnadmin create failed: %v (%s)", err, out)
	}
	repoURL := "file://" + filepath.Join(base, "repo") // TempDir is absolute; file://+abs = file:///abs
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
	_ = os.RemoveAll(filepath.Join(wc, "ext")) // do not even materialize it

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
