package agent

// #3671 probes:
//  1) a successful warn-mode workflow/invariant execution must reach the
//     crash sidecar (the branch previously returned without recording, so a
//     post-crash restore replayed the write). Pinned at the appendCrashSidecar
//     seam with a session-bound minimal agent: mutating tool + success shape.
//  2) externally-created baseline must clear the dry-run gate before the
//     write (empty-loss on real external content).
//  3) checkpoint.ErrNothingToUndo sentinel: empty-stack Undo is errors.Is-
//    distinguishable from store failures.

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/topcheer/ggcode/internal/checkpoint"
)

func TestIssue3671_MutatingCallRecordedToSidecar(t *testing.T) {
	dir := withCrashSidecarDir(t)
	// Bind a ledger: appendCrashSidecar treats a nil ledger as the kill
	// switch being off and returns before writing.
	a := &Agent{sessionID: "issue3671-sidecar", toolDedup: newToolDedupLedger()}
	a.appendCrashSidecar("write_file", `{"path":"/tmp/x.go","content":"x"}`)
	data, err := os.ReadFile(filepath.Join(dir, "issue3671-sidecar_mutating.jsonl"))
	if err != nil {
		t.Fatalf("mutating call must be recorded to the sidecar: %v", err)
	}
	var call crashMutatingCall
	if err := json.Unmarshal(data[:len(data)-1], &call); err != nil {
		t.Fatalf("sidecar line must be valid JSONL: %v", err)
	}
	if call.Name != "write_file" {
		t.Fatalf("expected write_file, got %s", call.Name)
	}
}

func TestIssue3671_ExternalBaselineClearsGate(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "external.go")
	os.WriteFile(f, []byte("package x\nvar A = 1\n"), 0o644)
	// The externally-created branch now validates (external, new) before the
	// overwrite: emptying a file that externally gained real content blocks.
	if msg := dryRunValidate(f, "package x\nvar A = 1\n", ""); msg == "" {
		t.Fatal("empty-loss on external content must be blocked by the gate")
	}
}

func TestIssue3671_NothingToUndoSentinel(t *testing.T) {
	m := checkpoint.NewManager(10)
	_, err := m.Undo("agent")
	if !errors.Is(err, checkpoint.ErrNothingToUndo) {
		t.Fatalf("empty-stack Undo must carry the sentinel, got: %v", err)
	}
}
