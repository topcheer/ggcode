package agent

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/tool"
)

// Issue #1125 regression tests.
//
// The memoize cache anchors list_directory entries to the directory's
// mtime/size. In-place child-file writes (gofmt -w, echo >> file) only change
// the child's mtime; most filesystems do NOT refresh the parent directory's
// mtime or size, so every cached signal stays equal while the listed child
// sizes are stale, and there was no TTL backstop to bound the staleness.
//
// Fix under test: directory-listing entries carry a short maxAge hard bound
// layered on top of the fast-path stat comparisons (#1104 style mtime+size),
// while plain file entries keep unbounded mtime+size freshness.

func TestIssue1125_DirListEntryExpiredByMaxAgeBackstop(t *testing.T) {
	tmpDir := t.TempDir()
	child := filepath.Join(tmpDir, "main.go")
	if err := os.WriteFile(child, []byte("package main\n"), 0644); err != nil {
		t.Fatal(err)
	}

	m := newToolMemo()
	args := []byte(`{"path":"` + tmpDir + `"}`)
	m.put("list_directory", args, tool.Result{Content: "main.go\n"})

	// Fresh entry inside the backstop window must still hit (fast path works).
	if _, hit := m.get("list_directory", args); !hit {
		t.Fatal("expected hit for fresh directory listing")
	}

	// Rewind createdAt past memoizeDirListTTL without touching anything on
	// disk, so every mtime/size comparison still matches exactly. This
	// isolates the age backstop from filesystem-specific directory-metadata
	// refresh behavior.
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

	// Shrink the entry's backstop deterministically, then perform an
	// in-place same-length child edit that leaves the directory metadata
	// untouched on filesystems where such writes do not bump the parent
	// dir mtime (the core #1125 scenario).
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

	// An aged-but-unmodified file keeps hitting: the backstop must be scoped
	// to directory listings only.
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

	// sourceMutatingTools fires invalidateTTLBased after a mutation. TTL-based
	// results must be dropped as before; the directory listing survives the
	// sweep because its validity is anchored to the path, but it is now also
	// bounded by the age backstop.
	m.invalidateTTLBased()

	if _, hit := m.get("grep", grepArgs); hit {
		t.Fatal("invalidateTTLBased must keep clearing grep-style TTL entries")
	}
	if _, hit := m.get("list_directory", dirArgs); !hit {
		t.Fatal("fresh listing anchored to its path should survive invalidateTTLBased, matching pre-#1125 behavior")
	}

	// Once the backstop lapses the surviving listing goes away on its own:
	// worst-case staleness is capped at memoizeDirListTTL.
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

	// Verify end-to-end wiring without sleeping through the real backstop:
	// put() must stamp list_directory entries with exactly memoizeDirListTTL,
	// while every non-listing tool stays at zero. Expiry-by-age itself is
	// covered by the shrunken-maxAge tests above.
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
