package memory

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadForPrompt_InlinesPersistentMemories(t *testing.T) {
	dir := t.TempDir()
	am := &AutoMemory{dir: dir}

	writeMem(t, dir, "build-process-impl", "Run: make build\nUse Go 1.26.")

	writeMemOld(t, dir, "impl-task-old", "old task", 40*24*time.Hour)

	writeMem(t, dir, "research-2026-07-14-r3", "latest research findings")

	inline, indexOnly, err := am.LoadForPrompt()
	if err != nil {
		t.Fatalf("LoadForPrompt error: %v", err)
	}

	if len(inline) != 1 {
		t.Fatalf("expected 1 inline entry, got %d", len(inline))
	}
	if inline[0].Key != "build-process-impl" {
		t.Errorf("expected build-process-impl inline, got %s", inline[0].Key)
	}
	if inline[0].Content == "" {
		t.Error("inline entry should have non-empty content")
	}

	if len(indexOnly) != 1 {
		t.Fatalf("expected 1 index-only entry, got %d: %v", len(indexOnly), indexOnly)
	}
	if indexOnly[0] != "research-2026-07-14-r3" {
		t.Errorf("expected research entry in index, got %s", indexOnly[0])
	}
}

func TestLoadForPrompt_OversizedPersistentIsIndexOnly(t *testing.T) {
	dir := t.TempDir()
	am := &AutoMemory{dir: dir}

	big := make([]byte, maxInlineBytes+1)
	for i := range big {
		big[i] = 'x'
	}
	writeMem(t, dir, "big-architecture-design", string(big))

	inline, indexOnly, err := am.LoadForPrompt()
	if err != nil {
		t.Fatalf("LoadForPrompt error: %v", err)
	}

	if len(inline) != 0 {
		t.Errorf("oversized entry should not be inlined, got %d", len(inline))
	}
	if len(indexOnly) != 1 {
		t.Fatalf("expected 1 index-only, got %d", len(indexOnly))
	}
}

func TestLoadForPrompt_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	am := &AutoMemory{dir: dir}

	inline, indexOnly, err := am.LoadForPrompt()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(inline) != 0 || len(indexOnly) != 0 {
		t.Errorf("expected empty results for empty dir")
	}
}

func TestLoadForPrompt_BudgetEnforced(t *testing.T) {
	dir := t.TempDir()
	am := &AutoMemory{dir: dir}

	for i := 0; i < 10; i++ {
		content := "x"
		writeMemNamed(t, dir, persistentKeyName(i), content)
	}

	inline, _, err := am.LoadForPrompt()
	if err != nil {
		t.Fatalf("error: %v", err)
	}

	if len(inline) != 10 {
		t.Errorf("expected 10 inlined entries (budget not exceeded), got %d", len(inline))
	}
}

func writeMem(t *testing.T, dir, key, content string) {
	t.Helper()
	path := filepath.Join(dir, key+".md")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func writeMemNamed(t *testing.T, dir, key, content string) {
	t.Helper()
	writeMem(t, dir, key, content)
}

func writeMemOld(t *testing.T, dir, key, content string, age time.Duration) {
	t.Helper()
	path := filepath.Join(dir, key+".md")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-age)
	if err := os.Chtimes(path, past, past); err != nil {
		t.Fatal(err)
	}
}

func persistentKeyName(i int) string {

	return persistentPrefix(i) + "-impl"
}

func persistentPrefix(i int) string {
	letters := "abcdefghij"
	if i < len(letters) {
		return string(letters[i])
	}
	return "z"
}
