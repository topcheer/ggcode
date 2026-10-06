package update

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// zz_issue2806_test.go guards against the self-reporting "other install"
// recurrence (#2806): FindOtherInstalls compared bare Abs paths, so on Linux
// (where /proc/self/exe returns the RESOLVED real path) a linuxbrew-style
// bin/ggcode symlink to the running binary reported ITSELF as another
// install with a misleading warning.

// TestIssue2806ResolvePathFollowsSymlink: a symlink and its target must
// resolve to the same canonical path - the equality the skip-ourselves check
// depends on.
func TestIssue2806ResolvePathFollowsSymlink(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "real", "ggcode")
	if err := os.MkdirAll(filepath.Dir(real), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(real, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "bin", "ggcode")
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlink creation unavailable: %v", err)
	}
	if resolvePath(link) != resolvePath(real) {
		t.Errorf("symlink %q and target %q resolve differently: %q vs %q - self-report recurrence (#2806)",
			link, real, resolvePath(link), resolvePath(real))
	}
}

// TestIssue2806ResolvePathPlainFile: a fully-resolved result must be
// idempotent (resolving again changes nothing) and absolute. Note /var is
// itself a symlink to /private/var on macOS, so the canonical form may
// legitimately differ from the naive Abs spelling.
func TestIssue2806ResolvePathPlainFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "ggcode")
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := resolvePath(p)
	if !filepath.IsAbs(got) {
		t.Errorf("resolvePath(plain) = %q, not absolute", got)
	}
	if again := resolvePath(got); again != got {
		t.Errorf("resolvePath not idempotent: %q then %q", got, again)
	}
}

// TestIssue2806NoBareAbsComparison pins the source-level invariant: the
// skip-ourselves and dedup comparisons must go through resolvePath
// (EvalSymlinks-aware), not bare filepath.Abs.
func TestIssue2806NoBareAbsComparison(t *testing.T) {
	src, err := os.ReadFile("detect.go")
	if err != nil {
		t.Fatalf("read detect.go: %v", err)
	}
	s := string(src)
	fnStart := strings.Index(s, "func FindOtherInstalls")
	if fnStart < 0 {
		t.Fatal("FindOtherInstalls not found")
	}
	fn := s[fnStart:]
	if end := strings.Index(fn, "\n}\n"); end > 0 {
		fn = fn[:end]
	}
	if strings.Contains(fn, "filepath.Abs(currentPath)") || strings.Contains(fn, "filepath.Abs(path)") {
		t.Error("bare Abs comparison inside FindOtherInstalls (#2806 recurrence) - must use resolvePath")
	}
	if !strings.Contains(fn, "resolvePath(currentPath)") || !strings.Contains(fn, "resolvePath(path)") {
		t.Error("FindOtherInstalls does not resolve both sides via resolvePath")
	}
}
