package util

// sa-147 coverage hardening: pins real behavior of util primitives that the
// existing suite leaves uncovered. Tests only - no production code changes.
// Theory anchor: CVE-2025-47906 (Go LookPath PATH handling) and the
// cross-platform shell/path pitfalls family - utility-layer edge branches
// (env fallbacks, WSL bash skipping, lock timeouts, dangling symlinks) are
// exactly where silent misbehavior hides (see dev.to "Two Cross-Platform
// Bugs in Our Go CLI", 2025).

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// TestMain injects a debug-log sink before any test runs, so the first
// DetectShell() call exercises the debugLogFn branch of the cache bootstrap
// (in production SetDebugLogFn is called once at startup, before any shell
// probe - mirrored here).
func TestMain(m *testing.M) {
	SetDebugLogFn(func(category, format string, args ...interface{}) {})
	os.Exit(m.Run())
}

func TestFormatToolDetailPassthrough(t *testing.T) {
	// paths.go API-compat shims must return text unchanged whatever the
	// baseDir. Pins the no-op contract the rendering layer relies on.
	for _, tc := range []struct{ text, base string }{
		{"", "/base"},
		{"/abs/path/main.go", "/base"},
		{"detail with /workspace/file.txt inside", "/workspace"},
		{"multi\nline\n /base/inner.go", "/base"},
	} {
		if got := FormatToolDetail(tc.text, tc.base); got != tc.text {
			t.Errorf("FormatToolDetail(%q,%q) = %q, want unchanged", tc.text, tc.base, got)
		}
		if got := RelativizePaths(tc.text, tc.base); got != tc.text {
			t.Errorf("RelativizePaths(%q,%q) = %q, want unchanged", tc.text, tc.base, got)
		}
	}
}

func TestSetDebugLogFnSwappable(t *testing.T) {
	var mu sync.Mutex
	var got []string
	fn := func(category, format string, args ...interface{}) {
		mu.Lock()
		defer mu.Unlock()
		got = append(got, category)
	}
	prev := debugLogFn
	SetDebugLogFn(fn)
	defer SetDebugLogFn(prev)
	if debugLogFn == nil {
		t.Fatal("debugLogFn must be set after SetDebugLogFn")
	}
	debugLogFn("shell", "probe %d", 1)
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 1 || got[0] != "shell" {
		t.Fatalf("captured = %v, want [shell]", got)
	}
}

// #1842 companion: when PowerShell is absent, the bash.exe probe must SKIP
// the System32 WSL entry point and settle on a real Git Bash on the next
// candidate, instead of mislabeling WSL as "git-bash".
func TestDetectShellWindowsBashSkipsWSLSystem32(t *testing.T) {
	spec, err := detectShell("windows",
		func(name string) (string, error) {
			switch name {
			case "bash.exe":
				return `C:\Windows\System32\bash.exe`, nil
			case "bash":
				return `C:\Program Files\Git\bin\bash.exe`, nil
			default:
				return "", errors.New("not found: " + name)
			}
		},
		func(string) (os.FileInfo, error) { return nil, os.ErrNotExist },
		func(string) string { return "" },
	)
	if err != nil {
		t.Fatalf("detectShell() error = %v", err)
	}
	if spec.Name != "git-bash" {
		t.Fatalf("expected git-bash after WSL skip, got %q (%s)", spec.Name, spec.Path)
	}
	if spec.Path != `C:\Program Files\Git\bin\bash.exe` {
		t.Fatalf("expected real Git Bash, got %q", spec.Path)
	}
	if len(spec.Args) != 1 || spec.Args[0] != "-c" {
		t.Fatalf("expected [-c] args, got %v", spec.Args)
	}
}

// Companion to the all-missing case: WSL is the ONLY bash visible, so both
// probes are rejected and the documented "no supported shell" error wins -
// never a mislabeled WSL spec.
func TestDetectShellWindowsOnlyWSLBashErrors(t *testing.T) {
	_, err := detectShell("windows",
		func(name string) (string, error) {
			if name == "bash.exe" || name == "bash" {
				return `C:\Windows\System32\` + name, nil
			}
			return "", errors.New("not found: " + name)
		},
		func(string) (os.FileInfo, error) { return nil, os.ErrNotExist },
		func(string) string { return "" },
	)
	if err == nil {
		t.Fatal("WSL-only bash must not satisfy Windows shell detection")
	}
}

func TestFileLockUnopenablePathErrors(t *testing.T) {
	// Parent directory absent: OpenFile must fail fast with the raw error
	// (no retry storm, no panic) - callers degrade to unlocked merge.
	_, err := FileLock(filepath.Join(t.TempDir(), "no-such-dir", "x.lock"))
	if err == nil {
		t.Fatal("expected error for unopenable lock path")
	}
}

func TestAcquireWithRetryPermanentErrorNotRetried(t *testing.T) {
	// A non-busy failure is permanent: acquireWithRetry must return it
	// as-is immediately, not burn the timeout budget retrying.
	attempts := 0
	sentinel := errors.New("permanent: EACCES")
	err := acquireWithRetry(func() error {
		attempts++
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("permanent error must pass through unchanged, got %v", err)
	}
	if attempts != 1 {
		t.Fatalf("permanent error retried %d times, want 1", attempts)
	}
}

func TestHomeDirUserProfileFallback(t *testing.T) {
	// HOME unset/empty: Unix falls through os.UserHomeDir to USERPROFILE;
	// Windows reads USERPROFILE directly. Either way the fallback wins.
	tmp := t.TempDir()
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", tmp)
	if got := HomeDir(); got != tmp {
		t.Fatalf("HomeDir() with empty HOME = %q, want USERPROFILE %q", got, tmp)
	}
	if got := ConfigDir(); got != filepath.Join(tmp, ".ggcode") {
		t.Fatalf("ConfigDir() = %q, want %q", got, filepath.Join(tmp, ".ggcode"))
	}
}

// Companion to #1359: a DANGLING symlink (target absent) cannot be resolved.
// EvalSymlinks fails, so the rename replaces the link itself with a regular
// file - reads of it were failing anyway; writes must not error out.
func TestAtomicWriteFileDanglingSymlink(t *testing.T) {
	dir := t.TempDir()
	link := filepath.Join(dir, "dangling.txt")
	if err := os.Symlink(filepath.Join(dir, "gone.txt"), link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if err := AtomicWriteFile(link, []byte("RESURRECTED"), 0o600); err != nil {
		t.Fatalf("AtomicWriteFile on dangling symlink: %v", err)
	}
	fi, err := os.Lstat(link)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		t.Fatal("dangling link still a symlink; expected replacement by regular file")
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("expected defaultMode 0600, got %o", fi.Mode().Perm())
	}
	data, err := os.ReadFile(link)
	if err != nil || string(data) != "RESURRECTED" {
		t.Fatalf("content = %q err = %v", data, err)
	}
}
