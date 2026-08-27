package agent

// zz_issue1104_test.go -- pins the two cache-invalidation gaps fixed for
// issue #1104:
//
//  1. Coarse mtime granularity windows: toolMemo compared ModTime equality
//     only, so on ext3/HFS+/NFS/FAT32 filesystems (1-2s ticks) an edit
//     performed right after a cached read left mtime unchanged and the
//     stale pre-edit content was served back. The fix records the file
//     size alongside mtime and invalidates when either signal drifts.
//  2. undo_edit never invalidated anything: checkpoint restore rewrites
//     files on disk, but undo_edit was absent from every mutation set, so
//     memoize/speculator/command caches survived an undo.

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

// TestIssue1104_SizeChangeInvalidatesWithinSameMTIMETick simulates a coarse
// filesystem: after put + real content change, the frozen-tick illusion is
// forced by pinning the recorded entry mtime onto the edited file's current
// ModTime. Only the size comparison can then catch the drift, so the cache
// MUST miss.
func TestIssue1104_SizeChangeInvalidatesWithinSameMTIMETick(t *testing.T) {
	tmpFile := filepath.Join(t.TempDir(), "coarse.go")
	if err := os.WriteFile(tmpFile, []byte("v1"), 0o644); err != nil {
		t.Fatal(err)
	}
	m := newToolMemo()
	args := []byte(`{"path":"` + tmpFile + `"}`)
	m.put("read_file", args, issue1104Result("v1"))

	// Real disk write with different length.
	if err := os.WriteFile(tmpFile, []byte("much longer v2"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Force the coarse-tick illusion: make the snapshot's mtime equal to
	// what os.Stat now reports, exactly as an ext3/HFS+/FAT32 tick would.
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

// TestIssue1104_UnchangedFileStillCached: without an edit the mtime+size pair
// stays equal and normal caching resumes - no regression for happy-path hits,
// including immediately-after-put reads (invalidateTTLBased survival path).
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

	// Same-tick untouched re-reads also stay hits (the failing sequence of
	// the pre-fix invalidation experiments would have missed here).
	m.invalidateTTLBased()
	if _, hit := m.get("read_file", args); !hit {
		t.Fatal("#1104: invalidateTTLBased must keep valid mtime+size read_file entries")
	}
}

// TestIssue1104_WindowDoesNotAffectTTLEntries: the mtime+size check only
// guards file-based entries; TTL-based entries (grep, LSP, git) keep their
// normal validity rules.
func TestIssue1104_WindowDoesNotAffectTTLEntries(t *testing.T) {
	m := newToolMemo()
	args := []byte(`{"pattern":"x"}`)
	m.put("grep", args, issue1104Result("matches"))

	if _, hit := m.get("grep", args); !hit {
		t.Fatal("TTL entry put just now must be served regardless of file-size checks")
	}
}

// TestIssue1104_UndoEditClassifiedAsTreeMutation: undo_edit restores files
// via checkpoint writes and must classify as a source-tree mutation exactly
// like direct edit tools and git mutations, driving speculator/memo/command
// invalidation through mutatesSourceTree.
func TestIssue1104_UndoEditClassifiedAsTreeMutation(t *testing.T) {
	for _, name := range []string{
		"undo_edit",
		"edit_file", "write_file", "multi_edit_file", "multi_file_edit",
		"multi_file_write", "batch_replace", "lsp_rename", "file_ops",
		"notebook_edit",               // canonical members stay classified
		"git_stash", "enter_worktree", // partial git mutations
		"git_checkout", "git_reset", "git_revert", // whole-tree git
	} {
		if !mutatesSourceTree(name) {
			t.Errorf("mutatesSourceTree(%q) = false, want true", name)
		}
	}
	// Read-only tools stay excluded.
	for _, name := range []string{"read_file", "grep", "lsp_diagnostics", "git_status"} {
		if mutatesSourceTree(name) {
			t.Errorf("mutatesSourceTree(%q) = true, want false", name)
		}
	}
}

// TestIssue1104_CanonicalSupersetNotEnlarged: undo_edit was deliberately NOT
// added to sourceMutatingTools because #737/#153 pin its 9-tool membership;
// this guards against a future accidental merge of the two sets.
func TestIssue1104_CanonicalSupersetNotEnlarged(t *testing.T) {
	if len(sourceMutatingTools) != 9 {
		t.Fatalf("sourceMutatingTools grew to %d members; #737 sync assertions pin it at 9 - extending mutation semantics belongs in mutatesSourceTree instead", len(sourceMutatingTools))
	}
	if sourceMutatingTools["undo_edit"] {
		t.Error("undo_edit must not become a canonical superset member; #737 length-pins the set")
	}
}

// TestIssue1104_EditedEntryInvalidatedAcrossTick: regression coverage for the
// original failure sequence end-to-end - cached read, real write whose mtime
// moves forward, then get must miss even without the coarse-FS illusion.
func TestIssue1104_EditedEntryInvalidatedAcrossTick(t *testing.T) {
	tmpFile := filepath.Join(t.TempDir(), "edited.go")
	if err := os.WriteFile(tmpFile, []byte("v1"), 0o644); err != nil {
		t.Fatal(err)
	}
	m := newToolMemo()
	args := []byte(`{"path":"` + tmpFile + `"}`)
	m.put("read_file", args, issue1104Result("v1"))

	time.Sleep(10 * time.Millisecond) // widen odds of a fresh mtime tick
	if err := os.WriteFile(tmpFile, []byte("v2"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, hit := m.get("read_file", args); hit {
		t.Fatal("#1104: entry whose file changed must be invalidated")
	}
}
