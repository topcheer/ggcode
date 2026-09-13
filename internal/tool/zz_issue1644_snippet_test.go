package tool

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// #1644 case 3: load() must hand out snapshots, never the shared cache
// pointer - concurrent doSave/doGet/doDelete must not race callers holding
// a load() result.
func TestIssue1644LoadReturnsSnapshotNotSharedCache(t *testing.T) {
	dir := t.TempDir()
	tool := &CmdSnippetTool{WorkingDir: dir, filePath: filepath.Join(dir, "snippets.json")}
	if _, err := tool.doSave("alpha", "echo hi", "", nil); err != nil {
		t.Fatal(err)
	}
	a, _ := tool.load()
	b, _ := tool.load()
	if a == b || a.Entries == nil {
		t.Fatal("load() must return independent snapshots")
	}
	// mutate snapshot A; snapshot B and the cache must be unaffected
	a.Entries[0].Name = "mutated"
	b2, _ := tool.load()
	if b2.Entries[0].Name != "alpha" {
		t.Fatalf("snapshot mutation leaked into cache: %q", b2.Entries[0].Name)
	}
}

// #1644 case 3: concurrent mixed actions must not lose updates (TOCTOU:
// the whole read-modify-write now shares one lock window).
func TestIssue1644ConcurrentMixedActionsNoLostUpdate(t *testing.T) {
	dir := t.TempDir()
	tool := &CmdSnippetTool{WorkingDir: dir, filePath: filepath.Join(dir, "snippets.json")}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, _ = tool.doSave(fmt.Sprintf("snip-%d", i), fmt.Sprintf("echo %d", i), "", nil)
		}(i)
	}
	wg.Wait()
	store, err := tool.load()
	if err != nil {
		t.Fatal(err)
	}
	if len(store.Entries) != 8 {
		t.Fatalf("lost updates under concurrency: want 8 entries, got %d", len(store.Entries))
	}
}

// #1644 case 5: a persist failure during doGet (use-count write) must be
// surfaced, not silently discarded.
func TestIssue1644DoGetPersistsErrorSurfaced(t *testing.T) {
	dir := t.TempDir()
	tool := &CmdSnippetTool{WorkingDir: dir, filePath: filepath.Join(dir, "snippets.json")}
	if _, err := tool.doSave("alpha", "echo hi", "", nil); err != nil {
		t.Fatal(err)
	}
	// break the store path so persist fails (parent is a file)
	tool.filePath = filepath.Join(dir, "blocker", "snippets.json")
	if err := os.WriteFile(filepath.Join(dir, "blocker"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	// prime the cache with a good load first so the failure hits persist, not load
	if _, err := tool.load(); err != nil {
		t.Fatal(err)
	}
	res, err := tool.doGet("alpha")
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatalf("persist failure must surface as IsError, got: %s", res.Content)
	}
	if !strings.Contains(res.Content, "failed to persist") {
		t.Fatalf("unexpected error content: %s", res.Content)
	}
}

// #1644 case 6: tag truncation must tell the caller what was kept.
func TestIssue1644TagTruncationNoted(t *testing.T) {
	dir := t.TempDir()
	tool := &CmdSnippetTool{WorkingDir: dir, filePath: filepath.Join(dir, "snippets.json")}
	tags := make([]string, cmdSnippetMaxTags+3)
	for i := range tags {
		tags[i] = fmt.Sprintf("t%d", i)
	}
	res, err := tool.doSave("tagged", "echo hi", "", tags)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Content, "tags truncated") {
		t.Fatalf("silent truncation: %s", res.Content)
	}
}

// Regression: normal save/get/delete round-trip still works via mutate.
func TestIssue1644MutateRoundTrip(t *testing.T) {
	dir := t.TempDir()
	tool := &CmdSnippetTool{WorkingDir: dir, filePath: filepath.Join(dir, "snippets.json")}
	if _, err := tool.doSave("alpha", "echo hi", "", nil); err != nil {
		t.Fatal(err)
	}
	res, err := tool.doGet("alpha")
	if err != nil || res.IsError {
		t.Fatalf("get failed: %v %s", err, res.Content)
	}
	if !strings.Contains(res.Content, "echo hi") {
		t.Fatalf("get lost command: %s", res.Content)
	}
	res, err = tool.doDelete("alpha")
	if err != nil || res.IsError {
		t.Fatalf("delete failed: %v %s", err, res.Content)
	}
	data, err := os.ReadFile(tool.filePath)
	if err != nil {
		t.Fatal(err)
	}
	var store cmdSnippetStore
	if err := json.Unmarshal(data, &store); err != nil {
		t.Fatal(err)
	}
	if len(store.Entries) != 0 {
		t.Fatalf("delete not persisted: %d entries", len(store.Entries))
	}
}
