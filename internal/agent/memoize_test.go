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
