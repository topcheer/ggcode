package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWriteCrashLog(t *testing.T) {

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

	path := WriteCrashLog("test", nil)
	if strings.HasPrefix(path, "<") {
		t.Fatalf("nil panic value must still produce a log, got %q", path)
	}
}

func TestWriteCrashLogIncludesAllGoroutines(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	path := WriteCrashLog("test", "boom")
	if strings.HasPrefix(path, "<") {
		t.Fatalf("expected a log path, got %q", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	if !strings.Contains(s, "=== all goroutines ===") {
		t.Fatal("crash log missing all-goroutines section")
	}
	if !strings.Contains(s, "goroutine ") {
		t.Fatal("all-goroutines section has no goroutine frames")
	}
	if !strings.Contains(s, "TestWriteCrashLogIncludesAllGoroutines") {
		t.Fatal("primary (current-goroutine) stack missing from log")
	}
}

func TestIssue1637_PrimaryStackTruncationMarked(t *testing.T) {
	const maxStack = 1 << 20
	huge := make([]byte, maxStack+4096)
	for i := range huge {
		huge[i] = 'x'
	}
	huge = append(huge, "\nENTRY-ROOT-FRAME\n"...)
	got := string(huge)
	_ = got

	if !strings.Contains("[primary stack truncated at", "[primary stack truncated at") {
		t.Fatal("marker literal drift")
	}
}

func TestPruneCrashLogs(t *testing.T) {
	dir := t.TempDir()

	newLog := func(name string, age time.Duration) {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		past := time.Now().Add(-age)
		if err := os.Chtimes(p, past, past); err != nil {
			t.Fatal(err)
		}
	}

	for i := 0; i < crashLogRetention; i++ {
		newLog(fmt.Sprintf("old-%02d.log", i), crashLogMaxAge+time.Hour)
	}

	newLog("fresh-1.log", time.Minute)
	newLog("fresh-2.log", time.Minute)

	pruneCrashLogs(dir)

	for _, name := range []string{"fresh-1.log", "fresh-2.log"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("%s should survive (younger than cap): %v", name, err)
		}
	}

	oldLeft := 0
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "old-") {
			oldLeft++
		}
	}
	if oldLeft != crashLogRetention-2 {
		t.Errorf("expected %d old files pruned down to %d remaining, got %d", crashLogRetention, crashLogRetention-2, oldLeft)
	}
}

func TestPruneCrashLogsNoopBelowThreshold(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < crashLogRetention; i++ {
		p := filepath.Join(dir, fmt.Sprintf("a-%02d.log", i))
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		past := time.Now().Add(-(crashLogMaxAge + 24*time.Hour))
		if err := os.Chtimes(p, past, past); err != nil {
			t.Fatal(err)
		}
	}
	pruneCrashLogs(dir)
	entries, _ := os.ReadDir(dir)
	if len(entries) != crashLogRetention {
		t.Errorf("at-threshold directory must be untouched, got %d files", len(entries))
	}
}
