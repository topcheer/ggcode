package tmux

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// #4681: a corrupt pane store must be quarantined (renamed aside, data
// preserved for manual recovery) and rebuilt from empty - not wedge every
// Load/Save on the machine forever.
func TestIssue4681_CorruptStoreQuarantinedAndRebuilt(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tmux-panes.json")
	if err := os.WriteFile(path, []byte("{{{ not json"), 0600); err != nil {
		t.Fatal(err)
	}

	store, err := readPaneStore(path)
	if err != nil {
		t.Fatalf("corrupt store must quarantine+rebuild, got error: %v (#4681)", err)
	}
	if store.Workspaces == nil || len(store.Workspaces) != 0 {
		t.Fatalf("rebuilt store must be empty, got %+v (#4681)", store.Workspaces)
	}

	// The corrupt original must survive as a timestamped quarantine copy.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	quarantined := false
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "tmux-panes.json.corrupt-") {
			data, rerr := os.ReadFile(filepath.Join(dir, e.Name()))
			if rerr == nil && strings.Contains(string(data), "not json") {
				quarantined = true
			}
		}
	}
	if !quarantined {
		t.Fatalf("corrupt store must be preserved as .corrupt-* for manual recovery, dir=%v (#4681)", entries)
	}

	// Round-trip: the rebuilt store is usable JSON.
	if _, err := readPaneStore(path); err != nil {
		t.Fatalf("post-quarantine reads must succeed: %v", err)
	}
	var probe paneStoreFile
	if err := json.Unmarshal([]byte(`{"workspaces":{}}`), &probe); err != nil {
		t.Fatal(err)
	}
}
