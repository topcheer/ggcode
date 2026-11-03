package memory

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newTestAutoMemory(t *testing.T) *AutoMemory {
	t.Helper()
	am := NewAutoMemory()
	am.dir = t.TempDir()
	return am
}

func TestArchiveOnOverwrite(t *testing.T) {
	am := newTestAutoMemory(t)
	if err := am.SaveMemory("deploy", "v1 use blue-green"); err != nil {
		t.Fatal(err)
	}
	if err := am.SaveMemory("deploy", "v2 use rolling"); err != nil {
		t.Fatal(err)
	}

	if got, _ := am.LoadKey("deploy"); got != "v2 use rolling" {
		t.Fatalf("live = %q, want v2", got)
	}

	vers := am.HistoryVersions("deploy")
	if len(vers) != 1 {
		t.Fatalf("history versions = %d, want 1", len(vers))
	}
	data, err := os.ReadFile(vers[0].Path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "v1 use blue-green" {
		t.Fatalf("archived = %q, want v1", string(data))
	}
}

func TestReadAsOf(t *testing.T) {
	am := newTestAutoMemory(t)
	if err := am.SaveMemory("runbook", "first"); err != nil {
		t.Fatal(err)
	}
	mid := time.Now()

	time.Sleep(10 * time.Millisecond)
	if err := am.SaveMemory("runbook", "second"); err != nil {
		t.Fatal(err)
	}

	if got, _, ok, err := am.ReadMemoryAsOf("runbook", mid); err != nil || !ok {
		t.Fatalf("as_of(mid) err=%v ok=%v", err, ok)
	} else if got != "first" {
		t.Fatalf("as_of(mid) = %q, want first", got)
	}

	if got, _, ok, err := am.ReadMemoryAsOf("runbook", time.Now()); err != nil || !ok {
		t.Fatalf("as_of(now) err=%v ok=%v", err, ok)
	} else if got != "second" {
		t.Fatalf("as_of(now) = %q, want second", got)
	}

	if _, _, ok, err := am.ReadMemoryAsOf("runbook", time.Time{}); err != nil || ok {
		t.Fatalf("as_of(zero) err=%v ok=%v, want ok=false", err, ok)
	}
}

func TestReadAsOfNoHistory(t *testing.T) {
	am := newTestAutoMemory(t)
	if err := am.SaveMemory("fresh", "only"); err != nil {
		t.Fatal(err)
	}
	if got, _, ok, err := am.ReadMemoryAsOf("fresh", time.Now()); err != nil || !ok || got != "only" {
		t.Fatalf("got=%q ok=%v err=%v, want only/true/nil", got, ok, err)
	}
	if vers := am.HistoryVersions("fresh"); len(vers) != 0 {
		t.Fatalf("history = %d, want 0", len(vers))
	}
}

func TestHistoryFilenameCrossPlatform(t *testing.T) {
	am := newTestAutoMemory(t)
	if err := am.SaveMemory("k", "a"); err != nil {
		t.Fatal(err)
	}
	if err := am.SaveMemory("k", "b"); err != nil {
		t.Fatal(err)
	}
	for _, v := range am.HistoryVersions("k") {
		base := filepath.Base(v.Path)
		if strings.ContainsAny(base, `:<>|"*?/\`) {
			t.Fatalf("archive name %q contains Windows-illegal chars", base)
		}
	}
}

func TestCorruptHistoryDegrades(t *testing.T) {
	am := newTestAutoMemory(t)
	if err := am.SaveMemory("k", "v1"); err != nil {
		t.Fatal(err)
	}
	if err := am.SaveMemory("k", "v2"); err != nil {
		t.Fatal(err)
	}

	hdir := filepath.Join(am.dir, historyDirName)
	if err := os.WriteFile(filepath.Join(hdir, "README.txt"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(hdir, "subdir"), 0755); err != nil {
		t.Fatal(err)
	}
	if got, _, ok, err := am.ReadMemoryAsOf("k", time.Now()); err != nil || !ok || got != "v2" {
		t.Fatalf("got=%q ok=%v err=%v, want v2/true/nil", got, ok, err)
	}
}
