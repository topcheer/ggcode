package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteCrashLog(t *testing.T) {
	// ConfigDir derives from HOME; redirect it so tests never touch the
	// real ~/.ggcode.
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	path := WriteCrashLog("test", "boom: simulated")
	if path == "" || strings.HasPrefix(path, "<") {
		t.Fatalf("expected a written log path, got %q", path)
	}
	if want := filepath.Join(tmp, ".ggcode", "crash"); filepath.Dir(path) != want {
		t.Fatalf("expected dir %s, got %s", want, filepath.Dir(path))
	}
	if !strings.HasPrefix(filepath.Base(path), "test-") {
		t.Fatalf("expected component prefix in filename, got %s", filepath.Base(path))
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read crash log: %v", err)
	}
	body := string(data)
	for _, want := range []string{"component: test", "panic:     boom: simulated", "goroutine"} {
		if !strings.Contains(body, want) {
			t.Errorf("crash log missing %q; body:\n%s", want, body)
		}
	}
}

func TestWriteCrashLogNilValue(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	// A nil panic VALUE must not itself panic inside the crash path. (Note:
	// Go 1.21+ converts actual panic(nil) calls to *runtime.PanicNilError,
	// so recover() never yields a bare nil in practice - this guards the
	// direct-call contract of WriteCrashLog.)
	path := WriteCrashLog("test", nil)
	if strings.HasPrefix(path, "<") {
		t.Fatalf("nil panic value must still produce a log, got %q", path)
	}
}
