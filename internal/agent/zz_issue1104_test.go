package agent

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/tool"
)

func issue1104Result(content string) tool.Result {
	return tool.Result{Content: content}
}

func TestIssue1104_SizeChangeInvalidatesWithinSameMTIMETick(t *testing.T) {
	tmpFile := filepath.Join(t.TempDir(), "coarse.go")
	if err := os.WriteFile(tmpFile, []byte("v1"), 0o644); err != nil {
		t.Fatal(err)
	}
	m := newToolMemo()
	args := []byte(`{"path":"` + tmpFile + `"}`)
	m.put("read_file", args, issue1104Result("v1"))

	if err := os.WriteFile(tmpFile, []byte("much longer v2"), 0o644); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(tmpFile)
	if err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	entry := m.entries[m.key("read_file", args)]
	entry.mtime = info.ModTime()
	m.mu.Unlock()

	if _, hit := m.get("read_file", args); hit {
		t.Fatal("#1104: same-mtime entry with changed file size must be invalidated")
	}
}

func TestIssue1104_UnchangedFileStillCached(t *testing.T) {
	tmpFile := filepath.Join(t.TempDir(), "fine.go")
	if err := os.WriteFile(tmpFile, []byte("content"), 0o644); err != nil {
		t.Fatal(err)
	}
	m := newToolMemo()
	args := []byte(`{"path":"` + tmpFile + `"}`)
	m.put("read_file", args, issue1104Result("content"))

	got, hit := m.get("read_file", args)
	if !hit || got.Content != "content" {
		t.Fatalf("unchanged file must be served from cache, got hit=%v content=%q", hit, got.Content)
	}

	m.invalidateTTLBased()
	if _, hit := m.get("read_file", args); !hit {
		t.Fatal("#1104: invalidateTTLBased must keep valid mtime+size read_file entries")
	}
}

func TestIssue1104_WindowDoesNotAffectTTLEntries(t *testing.T) {
	m := newToolMemo()
	args := []byte(`{"pattern":"x"}`)
	m.put("grep", args, issue1104Result("matches"))

	if _, hit := m.get("grep", args); !hit {
		t.Fatal("TTL entry put just now must be served regardless of file-size checks")
	}
}

func TestIssue1104_UndoEditClassifiedAsTreeMutation(t *testing.T) {
	for _, name := range []string{
		"undo_edit",
		"edit_file", "write_file", "multi_edit_file", "multi_file_edit",
		"multi_file_write", "batch_replace", "lsp_rename", "file_ops",
		"notebook_edit",
		"git_stash", "enter_worktree",
		"git_checkout", "git_reset", "git_revert",
	} {
		if !mutatesSourceTree(name) {
			t.Errorf("mutatesSourceTree(%q) = false, want true", name)
		}
	}

	for _, name := range []string{"read_file", "grep", "lsp_diagnostics", "git_status"} {
		if mutatesSourceTree(name) {
			t.Errorf("mutatesSourceTree(%q) = true, want false", name)
		}
	}
}

func TestIssue1104_CanonicalSupersetNotEnlarged(t *testing.T) {
	if len(sourceMutatingTools) != 9 {
		t.Fatalf("sourceMutatingTools grew to %d members; #737 sync assertions pin it at 9 - extending mutation semantics belongs in mutatesSourceTree instead", len(sourceMutatingTools))
	}
	if sourceMutatingTools["undo_edit"] {
		t.Error("undo_edit must not become a canonical superset member; #737 length-pins the set")
	}
}

func TestIssue1104_EditedEntryInvalidatedAcrossTick(t *testing.T) {
	tmpFile := filepath.Join(t.TempDir(), "edited.go")
	if err := os.WriteFile(tmpFile, []byte("v1"), 0o644); err != nil {
		t.Fatal(err)
	}
	m := newToolMemo()
	args := []byte(`{"path":"` + tmpFile + `"}`)
	m.put("read_file", args, issue1104Result("v1"))

	time.Sleep(10 * time.Millisecond)
	if err := os.WriteFile(tmpFile, []byte("v2"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, hit := m.get("read_file", args); hit {
		t.Fatal("#1104: entry whose file changed must be invalidated")
	}
}
