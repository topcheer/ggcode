package agent

// Regression probes for #3214: the r444 comment claimed "file_ops moves
// are tracked too" but the implementation had three gaps - userWriteTools
// lacked file_ops, extractUserEditPath could not parse file_ops's
// operations[] envelope, and multi_file_edit only surfaced its FIRST
// file. A user hand-editing a file the agent moved in was invisible to
// the turn-gap observer, so the ratchet never learned from it.

import (
	"encoding/json"
	"testing"
)

func TestIssue3214_FileOpsMoveDestinationsTracked(t *testing.T) {
	args, _ := json.Marshal(map[string]any{
		"operations": []map[string]any{
			{"action": "move", "source": "/tmp/a.go", "destination": "/tmp/b.go"},
			{"action": "move", "source": "/tmp/c.go", "destination": "/tmp/d.go"},
			{"action": "mkdir", "source": "/tmp/newdir"},
			{"action": "delete", "source": "/tmp/gone.go"},
		},
	})
	got := extractUserEditPaths("file_ops", args)
	if len(got) != 2 || got[0] != "/tmp/b.go" || got[1] != "/tmp/d.go" {
		t.Fatalf("file_ops move destinations: got %v, want [/tmp/b.go /tmp/d.go] (mkdir/delete excluded)", got)
	}
}

func TestIssue3214_FileOpsInUserWriteTools(t *testing.T) {
	if !userWriteTools["file_ops"] {
		t.Fatal("file_ops missing from userWriteTools - move destinations stay agent-authored-invisible (#3214)")
	}
}

func TestIssue3214_MultiFileEditAllFilesTracked(t *testing.T) {
	args, _ := json.Marshal(map[string]any{
		"files": []map[string]any{
			{"path": "/tmp/one.go", "content": "x"},
			{"path": "/tmp/two.go", "content": "y"},
		},
	})
	got := extractUserEditPaths("multi_file_edit", args)
	if len(got) != 2 || got[0] != "/tmp/one.go" || got[1] != "/tmp/two.go" {
		t.Fatalf("multi_file_edit must surface ALL files (was first-only): got %v", got)
	}
}

func TestIssue3214_SingleFileToolsUnchanged(t *testing.T) {
	args, _ := json.Marshal(map[string]any{"file_path": "/tmp/solo.go", "content": "z"})
	if got := extractUserEditPaths("write_file", args); len(got) != 1 || got[0] != "/tmp/solo.go" {
		t.Fatalf("write_file single-path semantics changed: got %v", got)
	}
	// Malformed JSON stays a no-op.
	if got := extractUserEditPaths("file_ops", []byte("{torn")); got != nil {
		t.Fatalf("torn json must yield nil, got %v", got)
	}
}
