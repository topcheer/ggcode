package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
)

// Regression guard for #4669: EnsureMeta's initial on-disk meta record must
// persist Preview and TasksEnvJSON exactly like appendMetaLocked does.
// Pre-fix, a fresh session whose only meta record came from EnsureMeta
// reloaded with an empty VCS fingerprint, so the workspace-change check
// misfired and rebuilt the task environment on every load.
func TestEnsureMeta_PersistsTasksEnvAndPreview(t *testing.T) {
	dir := t.TempDir()
	store, err := NewJSONLStore(dir)
	if err != nil {
		t.Fatalf("NewJSONLStore: %v", err)
	}

	ses := NewSession("test", "ep", "model")
	ses.Title = "t"
	ses.Preview = "first preview"
	ses.TasksEnvJSON = []byte(`{"branch":"main","head":"abc123","dirty":2}`)
	// EnsureMeta skips file creation for sessions with no user interaction
	// (see #4356 comment at the guard) - mirror a real first-turn session.
	ses.Messages = []provider.Message{{Role: "user", Content: []provider.ContentBlock{{Type: "text", Text: "hello"}}}}
	if err := store.EnsureMeta(ses); err != nil {
		t.Fatalf("EnsureMeta: %v", err)
	}

	raw, err := os.ReadFile(filepath.Join(dir, ses.ID+".jsonl"))
	if err != nil {
		t.Fatalf("read session file: %v", err)
	}
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	var meta jsonlRecord
	found := false
	for dec.More() {
		var rec jsonlRecord
		if err := dec.Decode(&rec); err != nil {
			t.Fatalf("decode record: %v", err)
		}
		if rec.Type == "meta" {
			meta = rec
			found = true
		}
	}
	if !found {
		t.Fatal("no meta record written by EnsureMeta")
	}
	if string(meta.TasksEnvJSON) != string(ses.TasksEnvJSON) {
		t.Errorf("meta.TasksEnvJSON = %q, want %q (appendMetaLocked persists it; EnsureMeta must match)",
			string(meta.TasksEnvJSON), string(ses.TasksEnvJSON))
	}
	if meta.Preview != ses.Preview {
		t.Errorf("meta.Preview = %q, want %q", meta.Preview, ses.Preview)
	}
}
