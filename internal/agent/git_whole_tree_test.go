package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/topcheer/ggcode/internal/tool"
)

func TestGitWholeTreeToolsContainsCheckout(t *testing.T) {
	expected := []string{"git_checkout", "git_reset", "git_revert"}
	for _, name := range expected {
		if !gitWholeTreeTools[name] {
			t.Errorf("gitWholeTreeTools should contain %q", name)
		}
	}
}

func TestGitWholeTreeToolsDisjointFromGitFileModifying(t *testing.T) {
	for name := range gitWholeTreeTools {
		if gitFileModifyingTools[name] {
			t.Errorf("%q in both gitWholeTreeTools and gitFileModifyingTools", name)
		}
	}
}

func TestMemoizeInvalidateAllClearsEntries(t *testing.T) {
	m := newToolMemo()
	tmpDir := t.TempDir()
	realFile := filepath.Join(tmpDir, "a.go")
	if err := os.WriteFile(realFile, []byte("package main"), 0644); err != nil {
		t.Fatal(err)
	}

	// mtime-based entry (read_file with real file)
	m.put("read_file", json.RawMessage(`{"path":"`+realFile+`"}`), tool.Result{Content: "content-a"})
	// TTL-based entry (grep - no path)
	m.put("grep", json.RawMessage(`{"pattern":"TODO"}`), tool.Result{Content: "grep-results"})

	if _, ok := m.get("read_file", json.RawMessage(`{"path":"`+realFile+`"}`)); !ok {
		t.Fatal("expected read_file entry to exist before invalidation")
	}
	if _, ok := m.get("grep", json.RawMessage(`{"pattern":"TODO"}`)); !ok {
		t.Fatal("expected grep entry to exist before invalidation")
	}

	// invalidateAll should clear everything
	m.invalidateAll()

	if _, ok := m.get("read_file", json.RawMessage(`{"path":"`+realFile+`"}`)); ok {
		t.Error("expected read_file entry to be cleared after invalidateAll")
	}
	if _, ok := m.get("grep", json.RawMessage(`{"pattern":"TODO"}`)); ok {
		t.Error("expected grep entry to be cleared after invalidateAll")
	}
}

func TestMemoizeInvalidateTTLBasedKeepsMtimeEntries(t *testing.T) {
	m := newToolMemo()
	tmpDir := t.TempDir()
	realFile := filepath.Join(tmpDir, "b.go")
	if err := os.WriteFile(realFile, []byte("package main"), 0644); err != nil {
		t.Fatal(err)
	}

	m.put("read_file", json.RawMessage(`{"path":"`+realFile+`"}`), tool.Result{Content: "content-b"})
	m.put("grep", json.RawMessage(`{"pattern":"FIXME"}`), tool.Result{Content: "grep-b"})

	// invalidateTTLBased should keep mtime entries, clear TTL entries
	m.invalidateTTLBased()

	if _, ok := m.get("read_file", json.RawMessage(`{"path":"`+realFile+`"}`)); !ok {
		t.Error("expected read_file mtime entry to survive invalidateTTLBased")
	}
	if _, ok := m.get("grep", json.RawMessage(`{"pattern":"FIXME"}`)); ok {
		t.Error("expected grep TTL entry to be cleared by invalidateTTLBased")
	}
}

func TestMemoizeInvalidateAllAllowsRepopulation(t *testing.T) {
	m := newToolMemo()
	tmpDir := t.TempDir()
	realFile := filepath.Join(tmpDir, "c.go")
	if err := os.WriteFile(realFile, []byte("package main"), 0644); err != nil {
		t.Fatal(err)
	}

	m.put("read_file", json.RawMessage(`{"path":"`+realFile+`"}`), tool.Result{Content: "content-c"})
	m.put("grep", json.RawMessage(`{"pattern":"XXX"}`), tool.Result{Content: "grep-c"})

	m.invalidateAll()

	// After invalidation, new puts should work
	m.put("read_file", json.RawMessage(`{"path":"`+realFile+`"}`), tool.Result{Content: "content-d"})
	if _, ok := m.get("read_file", json.RawMessage(`{"path":"`+realFile+`"}`)); !ok {
		t.Error("expected new entry after re-population to be retrievable")
	}
}
