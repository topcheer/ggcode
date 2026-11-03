package agent

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/tool"
)

func TestIssue1125_DirListEntryExpiredByMaxAgeBackstop(t *testing.T) {
	tmpDir := t.TempDir()
	child := filepath.Join(tmpDir, "main.go")
	if err := os.WriteFile(child, []byte("package main\n"), 0644); err != nil {
		t.Fatal(err)
	}

	m := newToolMemo()
	args := []byte(`{"path":"` + tmpDir + `"}`)
	m.put("list_directory", args, tool.Result{Content: "main.go\n"})

	if _, hit := m.get("list_directory", args); !hit {
		t.Fatal("expected hit for fresh directory listing")
	}

	m.mu.Lock()
	entry, ok := m.entries[m.key("list_directory", args)]
	if !ok {
		m.mu.Unlock()
		t.Fatal("entry missing after put")
	}
	entry.createdAt = time.Now().Add(-2 * memoizeDirListTTL)
	m.mu.Unlock()

	if _, hit := m.get("list_directory", args); hit {
		t.Fatal("#1125: expected miss after directory-listing TTL backstop expiry even though dir mtime/size unchanged")
	}
}

func TestIssue1125_InPlaceChildEditDoesNotExtendFreshness(t *testing.T) {
	tmpDir := t.TempDir()
	child := filepath.Join(tmpDir, "a.txt")
	if err := os.WriteFile(child, []byte("aaaaaaaa"), 0644); err != nil {
		t.Fatal(err)
	}

	m := newToolMemo()
	args := []byte(`{"path":"` + tmpDir + `"}`)
	m.put("list_directory", args, tool.Result{Content: "a.txt\n"})

	m.mu.Lock()
	entry, ok := m.entries[m.key("list_directory", args)]
	if !ok {
		m.mu.Unlock()
		t.Fatal("entry missing after put")
	}
	entry.maxAge = 20 * time.Millisecond
	m.mu.Unlock()

	time.Sleep(60 * time.Millisecond)
	if err := os.WriteFile(child, []byte("bbbbbbbb"), 0644); err != nil {
		t.Fatal(err)
	}

	if _, hit := m.get("list_directory", args); hit {
		t.Fatal("#1125: expected stale listing to be refused once the age backstop lapses")
	}
}

func TestIssue1125_ReadFileKeepsUnboundedMtimeFreshness(t *testing.T) {
	testFile := filepath.Join(t.TempDir(), "x.go")
	if err := os.WriteFile(testFile, []byte("package x\n"), 0644); err != nil {
		t.Fatal(err)
	}

	m := newToolMemo()
	args := []byte(`{"path":"` + testFile + `"}`)
	m.put("read_file", args, tool.Result{Content: "package x\n"})

	m.mu.Lock()
	entry, ok := m.entries[m.key("read_file", args)]
	var maxAge time.Duration
	if ok {
		maxAge = entry.maxAge
	}
	m.mu.Unlock()
	if !ok {
		t.Fatal("entry missing after put")
	}
	if maxAge != 0 {
		t.Fatalf("#1125 scope guard: read_file must stay on pure mtime+size invalidation, got maxAge=%v", maxAge)
	}

	m.mu.Lock()
	m.entries[m.key("read_file", args)].createdAt = time.Now().Add(-memoizeDirListTTL)
	m.mu.Unlock()
	if _, hit := m.get("read_file", args); !hit {
		t.Fatal("#1125 must not add an age limit to read_file entries")
	}
}

func TestIssue1125_InvalidateTTLBasedStillCoversListingsLifecycle(t *testing.T) {
	tmpDir := t.TempDir()
	child := filepath.Join(tmpDir, "f.go")
	if err := os.WriteFile(child, []byte("package f\n"), 0644); err != nil {
		t.Fatal(err)
	}

	m := newToolMemo()
	dirArgs := []byte(`{"path":"` + tmpDir + `"}`)
	grepArgs := []byte(`{"pattern":"func","path":"` + tmpDir + `"}`)

	m.put("list_directory", dirArgs, tool.Result{Content: "f.go\n"})
	m.put("grep", grepArgs, tool.Result{Content: "match\n"})

	m.invalidateTTLBased()

	if _, hit := m.get("grep", grepArgs); hit {
		t.Fatal("invalidateTTLBased must keep clearing grep-style TTL entries")
	}
	if _, hit := m.get("list_directory", dirArgs); !hit {
		t.Fatal("fresh listing anchored to its path should survive invalidateTTLBased, matching pre-#1125 behavior")
	}

	m.mu.Lock()
	m.entries[m.key("list_directory", dirArgs)].createdAt = time.Now().Add(-2 * memoizeDirListTTL)
	m.mu.Unlock()
	if _, hit := m.get("list_directory", dirArgs); hit {
		t.Fatal("#1125: expired listing must not be served after surviving invalidateTTLBased")
	}
}

func TestIssue1125_ConstantScopedToDirectoryListingsOnly(t *testing.T) {
	tmpDir := t.TempDir()
	if err := os.Mkdir(filepath.Join(tmpDir, "sub"), 0755); err != nil {
		t.Fatal(err)
	}

	m := newToolMemo()
	dirArgs := []byte(`{"path":"` + filepath.Join(tmpDir, "sub") + `"}`)
	m.put("list_directory", dirArgs, tool.Result{Content: ""})
	readArgs := []byte(`{"path":"` + filepath.Join(tmpDir, "sub", "f.go") + `"}`)
	m.put("read_file", readArgs, tool.Result{Content: "x"})

	m.mu.Lock()
	listingEntry, ok := m.entries[m.key("list_directory", dirArgs)]
	var listingMaxAge time.Duration
	if ok {
		listingMaxAge = listingEntry.maxAge
	}
	readEntry, ok2 := m.entries[m.key("read_file", readArgs)]
	var readFileMaxAge time.Duration
	if ok2 {
		readFileMaxAge = readEntry.maxAge
	}
	m.mu.Unlock()
	if !ok || !ok2 {
		t.Fatal("entries missing after puts")
	}
	if listingMaxAge != memoizeDirListTTL {
		t.Fatalf("#1125: list_directory maxAge=%v, want %v; put() backstop not wired", listingMaxAge, memoizeDirListTTL)
	}
	if readFileMaxAge != 0 {
		t.Fatalf("#1125: read_file must not gain an age bound, got %v", readFileMaxAge)
	}
}
