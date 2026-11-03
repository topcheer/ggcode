package util

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestMain(m *testing.M) {
	SetDebugLogFn(func(category, format string, args ...interface{}) {})
	os.Exit(m.Run())
}

func TestFormatToolDetailPassthrough(t *testing.T) {

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

	_, err := FileLock(filepath.Join(t.TempDir(), "no-such-dir", "x.lock"))
	if err == nil {
		t.Fatal("expected error for unopenable lock path")
	}
}

func TestAcquireWithRetryPermanentErrorNotRetried(t *testing.T) {

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
