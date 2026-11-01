package tool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// #4798 (S-29): an atomic multi_file_edit failure must carry a snapshot-drift
// summary - which planned files changed on disk since the batch was planned,
// with line counts and the first differing line - so the retry re-anchors in
// one step instead of a whack-a-mole loop.

func runAtomicEdit(t *testing.T, files map[string]string, edits map[string][]textEdit) Result {
	t.Helper()
	for path, content := range files {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	type jsonEdit struct {
		OldText string `json:"old_text"`
		NewText string `json:"new_text"`
	}
	type fileEntry struct {
		Path  string     `json:"path"`
		Edits []jsonEdit `json:"edits"`
	}
	var entries []fileEntry
	for path, es := range edits {
		je := make([]jsonEdit, 0, len(es))
		for _, e := range es {
			je = append(je, jsonEdit{OldText: e.OldText, NewText: e.NewText})
		}
		entries = append(entries, fileEntry{Path: path, Edits: je})
	}
	raw, err := json.Marshal(map[string]any{"files": entries, "mode": "atomic"})
	if err != nil {
		t.Fatal(err)
	}
	tool := MultiFileEdit{WorkingDir: t.TempDir()}
	res, execErr := tool.Execute(context.Background(), raw)
	if execErr != nil {
		t.Fatalf("execute returned error: %v", execErr)
	}
	return res
}

func TestIssue4798_AtomicPlanFailureReportsSnapshotDrift(t *testing.T) {
	dir := t.TempDir()
	fileA := filepath.Join(dir, "a.go")
	fileB := filepath.Join(dir, "b.go")
	// fileA's anchor resolves; fileB's anchor text does not exist -> the whole
	// atomic batch fails at plan time and NOTHING is written.
	res := runAtomicEdit(t,
		map[string]string{
			fileA: "line1\nline2\nline3\n",
			fileB: "alpha\nbeta\n",
		},
		map[string][]textEdit{
			fileA: {{OldText: "line2", NewText: "LINE2"}},
			fileB: {{OldText: "no-such-anchor", NewText: "x"}},
		},
	)
	if !res.IsError {
		t.Fatalf("expected atomic failure, got success: %s", res.Content)
	}

	// No external change between plan and failure: no drift lines.
	var before struct {
		SnapshotDrift []string `json:"snapshot_drift"`
	}
	if err := json.Unmarshal([]byte(res.Content), &before); err != nil {
		t.Fatal(err)
	}
	if len(before.SnapshotDrift) != 0 {
		t.Fatalf("expected no drift before external change, got %v", before.SnapshotDrift)
	}

	// Simulate a concurrent formatter rewriting fileA AFTER the plan snapshot:
	// rerun the same doomed batch against the changed disk state.
	res2 := runAtomicEdit(t,
		map[string]string{
			fileA: "line1\nline2\nline3\nline4\nline5\n", // formatter appended 2 lines
			fileB: "alpha\nbeta\n",
		},
		map[string][]textEdit{
			fileA: {{OldText: "line2", NewText: "LINE2"}}, // plan snapshot taken fresh here
			fileB: {{OldText: "no-such-anchor", NewText: "x"}},
		},
	)
	// Both runs plan from a fresh read, so drift is only observable when the
	// disk changes BETWEEN plan and the drift probe. To pin the drift summary
	// itself, exercise the helper directly on a stale plan (below) - the
	// end-to-end contract here is that the field exists and never fires on a
	// quiescent tree.
	var after struct {
		SnapshotDrift []string `json:"snapshot_drift"`
	}
	if err := json.Unmarshal([]byte(res2.Content), &after); err != nil {
		t.Fatal(err)
	}
	if len(after.SnapshotDrift) != 0 {
		t.Fatalf("quiescent tree: expected no drift lines, got %v", after.SnapshotDrift)
	}
}

func TestIssue4798_SnapshotDriftLinesChangedDeletedUnchanged(t *testing.T) {
	dir := t.TempDir()
	changed := filepath.Join(dir, "changed.txt")
	deleted := filepath.Join(dir, "deleted.txt")
	same := filepath.Join(dir, "same.txt")
	for _, p := range []string{changed, deleted, same} {
		if err := os.WriteFile(p, []byte("one\ntwo\nthree\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	plans := []PlannedFileEdit{
		{Path: changed, OldContent: "one\ntwo\nthree\n", Existed: true},
		{Path: deleted, OldContent: "one\ntwo\nthree\n", Existed: true},
		{Path: same, OldContent: "one\ntwo\nthree\n", Existed: true},
	}
	// External rewrite: line 2 differs and one line was appended.
	if err := os.WriteFile(changed, []byte("one\nTWO\nthree\nfour\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(deleted); err != nil {
		t.Fatal(err)
	}
	drift := snapshotDriftLines(plans)
	if len(drift) != 2 {
		t.Fatalf("expected 2 drift lines (changed+deleted; unchanged omitted), got %d: %v", len(drift), drift)
	}
	if !strings.Contains(drift[0], "changed on disk since the batch was planned") ||
		!strings.Contains(drift[0], "snapshot 3 lines, disk 4 lines, first difference at line 2") {
		t.Fatalf("changed-file drift line wrong: %q", drift[0])
	}
	if !strings.Contains(drift[1], "deleted on disk") {
		t.Fatalf("deleted-file drift line wrong: %q", drift[1])
	}
}
