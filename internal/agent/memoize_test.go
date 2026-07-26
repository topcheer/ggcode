package agent

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/tool"
)

func TestMemoize_FileBasedHit(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.go")
	os.WriteFile(testFile, []byte("package main\n"), 0644)

	m := newToolMemo()
	args := []byte(`{"path":"` + testFile + `"}`)

	// First call: miss, store result
	result := tool.Result{Content: "file contents here"}
	m.put("read_file", args, result)

	// Second call: should hit (file unchanged)
	got, hit := m.get("read_file", args)
	if !hit {
		t.Fatal("expected memo hit for unchanged file")
	}
	if got.Content != result.Content {
		t.Fatalf("got %q, want %q", got.Content, result.Content)
	}
	if m.hits != 1 {
		t.Fatalf("expected 1 hit, got %d", m.hits)
	}
}

func TestMemoize_FileChangedMiss(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.go")
	os.WriteFile(testFile, []byte("package main\n"), 0644)

	m := newToolMemo()
	args := []byte(`{"path":"` + testFile + `"}`)

	// Store result
	m.put("read_file", args, tool.Result{Content: "old contents"})

	// Modify file mtime (simulate edit)
	time.Sleep(10 * time.Millisecond)
	os.WriteFile(testFile, []byte("package main\nfunc newFunc() {}\n"), 0644)

	// Should miss (file changed)
	_, hit := m.get("read_file", args)
	if hit {
		t.Fatal("expected memo miss after file modification")
	}
}

func TestMemoize_TTLExpiry(t *testing.T) {
	m := newToolMemo()
	args := []byte(`{"pattern":"TODO","path":"."}`)

	// Store result with very short effective TTL
	m.put("grep", args, tool.Result{Content: "found TODO"})

	// Immediate: should hit (within TTL)
	_, hit := m.get("grep", args)
	if !hit {
		t.Fatal("expected memo hit within TTL")
	}

	// Manually expire: set createdAt to past
	m.mu.Lock()
	for _, e := range m.entries {
		e.createdAt = time.Now().Add(-2 * memoizeSearchTTL)
	}
	m.mu.Unlock()

	// Should miss (expired)
	_, hit = m.get("grep", args)
	if hit {
		t.Fatal("expected memo miss after TTL expiry")
	}
}

func TestMemoize_NotCachedOnError(t *testing.T) {
	m := newToolMemo()
	args := []byte(`{"path":"/nonexistent"}`)

	// Error results should not be cached
	m.put("read_file", args, tool.Result{Content: "error", IsError: true})

	_, hit := m.get("read_file", args)
	if hit {
		t.Fatal("error results should not be cached")
	}
}

func TestMemoize_LRU_Eviction(t *testing.T) {
	m := newToolMemo()

	// Fill cache to capacity
	for i := 0; i < memoizeMaxEntries; i++ {
		args := []byte(`{"pattern":"query` + string(rune('a'+i)) + `","path":"."}`)
		m.put("grep", args, tool.Result{Content: "result"})
	}

	if len(m.entries) != memoizeMaxEntries {
		t.Fatalf("expected %d entries, got %d", memoizeMaxEntries, len(m.entries))
	}

	// Add one more — should evict the oldest
	args := []byte(`{"pattern":"newquery","path":"."}`)
	m.put("grep", args, tool.Result{Content: "new result"})

	if len(m.entries) > memoizeMaxEntries {
		t.Fatalf("entries exceeded max: %d", len(m.entries))
	}

	// The oldest entry should be gone
	oldArgs := []byte(`{"pattern":"querya","path":"."}`)
	_ = oldArgs
	m.mu.Lock()
	_, exists := m.entries[m.key("grep", oldArgs)]
	m.mu.Unlock()
	if exists {
		t.Fatal("oldest entry should have been evicted")
	}
}

func TestMemoize_Reset(t *testing.T) {
	m := newToolMemo()
	args := []byte(`{"pattern":"TODO","path":"."}`)
	m.put("grep", args, tool.Result{Content: "found"})

	m.reset()

	if len(m.entries) != 0 {
		t.Fatalf("expected 0 entries after reset, got %d", len(m.entries))
	}
}

func TestMemoize_DirectoryMtime(t *testing.T) {
	tmpDir := t.TempDir()
	args := []byte(`{"path":"` + tmpDir + `"}`)

	m := newToolMemo()
	m.put("list_directory", args, tool.Result{Content: "file1.go\nfile2.go"})

	// Should hit initially
	_, hit := m.get("list_directory", args)
	if !hit {
		t.Fatal("expected hit for unchanged directory")
	}

	// Add a file to the directory (changes mtime)
	time.Sleep(10 * time.Millisecond)
	newFile := filepath.Join(tmpDir, "new.go")
	os.WriteFile(newFile, []byte("test"), 0644)

	// Should miss now
	_, hit = m.get("list_directory", args)
	if hit {
		t.Fatal("expected miss after directory modification")
	}
}

func TestMemoize_Stats(t *testing.T) {
	m := newToolMemo()
	args := []byte(`{"pattern":"test","path":"."}`)

	// Miss
	m.get("grep", args)
	if m.misses != 1 {
		t.Fatalf("expected 1 miss, got %d", m.misses)
	}

	// Store and hit
	m.put("grep", args, tool.Result{Content: "found"})
	m.get("grep", args)
	if m.hits != 1 {
		t.Fatalf("expected 1 hit, got %d", m.hits)
	}
}

func TestExtractJSONStringField(t *testing.T) {
	tests := []struct {
		name  string
		input string
		field string
		want  string
	}{
		{"simple", `{"path":"/foo/bar.go"}`, "path", "/foo/bar.go"},
		{"with spaces", `{"path": "/foo/bar.go"}`, "path", "/foo/bar.go"},
		{"missing field", `{"other":"value"}`, "path", ""},
		{"empty input", ``, "path", ""},
		{"escaped quote", `{"path":"/foo\"bar"}`, "path", `/foo\"bar`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractJSONStringField([]byte(tt.input), tt.field)
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

// TestMemoize_PutDuplicateKeyNoLRUDuplication verifies that putting the same
// key multiple times does not create duplicate entries in the LRU order slice.
// Before the fix, each put would append the key to m.order without removing
// the old occurrence, causing premature eviction and unbounded order growth.
func TestMemoize_PutDuplicateKeyNoLRUDuplication(t *testing.T) {
	m := newToolMemo()
	args := []byte(`{"pattern":"TODO","path":"."}`)

	// Put the same key multiple times
	for i := 0; i < 10; i++ {
		m.put("grep", args, tool.Result{Content: "result"})
	}

	// Should have exactly 1 entry and 1 order entry (no duplicates)
	if len(m.entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(m.entries))
	}
	if len(m.order) != 1 {
		t.Fatalf("expected order length 1 (no duplicates), got %d", len(m.order))
	}

	// Should still get a hit after repeated puts
	_, hit := m.get("grep", args)
	if !hit {
		t.Fatal("expected hit after repeated puts")
	}
}

func TestMemoize_InvalidateTTLBased(t *testing.T) {
	tmpFile := filepath.Join(t.TempDir(), "test.go")
	os.WriteFile(tmpFile, []byte("package main"), 0644)

	m := newToolMemo()

	// Add mtime-based entry (read_file)
	readArgs := []byte(`{"path":"` + tmpFile + `"}`)
	m.put("read_file", readArgs, tool.Result{Content: "file content"})

	// Add TTL-based entries (grep, LSP, git)
	m.put("grep", []byte(`{"pattern":"func","path":"."}`), tool.Result{Content: "match1"})
	m.put("lsp_diagnostics", []byte(`{"path":"`+tmpFile+`"}`), tool.Result{Content: "no errors"})
	m.put("git_status", []byte(`{}`), tool.Result{Content: "clean"})

	// All 4 entries should exist before invalidation
	if len(m.entries) != 4 {
		t.Fatalf("expected 4 entries before invalidation, got %d", len(m.entries))
	}

	// Invalidate TTL-based entries (simulates file edit)
	m.invalidateTTLBased()

	// mtime-based entry (read_file) should survive
	_, readHit := m.get("read_file", readArgs)
	if !readHit {
		t.Fatal("expected read_file (mtime-based) to survive invalidateTTLBased")
	}

	// TTL-based entries should be cleared
	_, grepHit := m.get("grep", []byte(`{"pattern":"func","path":"."}`))
	if grepHit {
		t.Fatal("expected grep (TTL-based) to be cleared by invalidateTTLBased")
	}

	_, lspHit := m.get("lsp_diagnostics", []byte(`{"path":"`+tmpFile+`"}`))
	if lspHit {
		t.Fatal("expected lsp_diagnostics (TTL-based) to be cleared")
	}

	_, gitHit := m.get("git_status", []byte(`{}`))
	if gitHit {
		t.Fatal("expected git_status (TTL-based) to be cleared")
	}
}
