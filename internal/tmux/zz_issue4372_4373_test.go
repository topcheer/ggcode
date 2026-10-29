package tmux

// #4372 + #4373 pinning tests.
//
// #4372: ListPaneIDs must pass -a (server-wide pane enumeration). Without it
// list-panes only covers the current window, and Refresh marked managed panes
// in other windows as stale (CaptureAll skipped them, Restore re-created
// duplicates). A fake tmux binary records its argv so the test asserts the
// exact flags without needing a real server.
//
// #4373: saveWorkspaceState must create the store directory BEFORE opening
// the flock sidecar. The old order (lock -> ... -> MkdirAll) made the very
// first Save fail with ENOENT forever on a custom store path or fresh
// environment, because FileLock's O_CREATE cannot create parent dirs.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIssue4372ListPaneIDsPassesAllServerFlag(t *testing.T) {
	dir := t.TempDir()
	fake := filepath.Join(dir, "fake-tmux")
	argvFile := filepath.Join(dir, "argv")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"" + argvFile + "\"\nprintf '%%0\\n%%1\\n'\n"
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Skipf("shell-script fake binary: %v", err)
	}
	c := &Client{bin: fake}

	ids, err := c.ListPaneIDs(context.Background())
	if err != nil {
		t.Fatalf("ListPaneIDs: %v", err)
	}
	if len(ids) != 2 || ids["%0"] == struct{}{} == false || ids["%1"] == struct{}{} == false {
		t.Fatalf("expected ids %%0 and %%1, got %v", ids)
	}

	raw, err := os.ReadFile(argvFile)
	if err != nil {
		t.Fatalf("fake tmux was never invoked: %v", err)
	}
	argv := strings.Fields(string(raw))
	hasAll := false
	for _, a := range argv {
		if a == "-a" {
			hasAll = true
		}
	}
	if !hasAll {
		t.Fatalf("list-panes invoked without -a (argv=%q) - panes in other windows would be marked stale (#4372)", argv)
	}
}

func TestIssue4373SaveCreatesStoreDirBeforeLock(t *testing.T) {
	// Parent directory deliberately does NOT exist: the flock sidecar open
	// must not be the first thing to touch the path.
	root := t.TempDir()
	path := filepath.Join(root, "deep", "nested", "tmux-panes.json")
	panes := []Pane{{ID: "%0", Workspace: "ws"}}
	if err := saveWorkspaceState(path, "ws", panes, nil); err != nil {
		t.Fatalf("first save into fresh directory failed (#4373): %v", err)
	}
	// Round-trip: the save must actually be loadable, not just error-free.
	if _, _, err := loadWorkspaceState(path, "ws"); err != nil {
		t.Fatalf("load after fresh save: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("store file missing after save: %v", err)
	}
}
