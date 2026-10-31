package tool

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// #4710: list_memory's as_of temporal read was gated by the CURRENT
// existence of the key (LoadKey empty -> scope skipped), so a deleted
// key's archived versions - which the memory layer faithfully keeps via
// archiveAndTombstone - were unreachable and misreported as a bare
// "key not found". The temporal path must consult ReadMemoryAsOf
// directly; only the current view keeps the existence gate.

func TestIssue4710AsOfReadsDeletedKeyArchive(t *testing.T) {
	global, project := mkMemStores(t)
	_ = global
	tol := NewListMemoryTool(global, project)

	if err := project.SaveMemory("gone-key", "v1 content that must survive deletion"); err != nil {
		t.Fatal(err)
	}
	// A timestamp INSIDE [save, delete): captured after the save stamped
	// it, with the 10ms margin the #3880 companion uses so the tombstone
	// instant is strictly AFTER it (same-nanosecond boundary would read
	// as the deleted gap).
	held := time.Now().UTC()
	time.Sleep(10 * time.Millisecond)
	if err := project.DeleteMemory("gone-key"); err != nil {
		t.Fatal(err)
	}

	// Temporal read AFTER deletion must return the archived version, not
	// an error and not "key not found".
	out, err := tol.Execute(context.Background(), json.RawMessage(
		`{"scope":"project","key":"gone-key","as_of":"`+held.Format(time.RFC3339Nano)+`"}`))
	if err != nil || out.IsError {
		t.Fatalf("as_of read of deleted key failed: %v / %s", err, out.Content)
	}
	for _, want := range []string{"as_of", "v1 content that must survive deletion"} {
		if !strings.Contains(out.Content, want) {
			t.Fatalf("temporal output missing %q:\n%s", want, out.Content)
		}
	}

	// Current view (no as_of) of the deleted key keeps the plain
	// not-found semantics - the gate is intact for the live path.
	out2, err2 := tol.Execute(context.Background(), json.RawMessage(`{"scope":"project","key":"gone-key"}`))
	if err2 != nil {
		t.Fatal(err2)
	}
	if !out2.IsError || !strings.Contains(out2.Content, "not found") {
		t.Fatalf("current view of a deleted key must stay a not-found error, got: %s", out2.Content)
	}

	// A key that never existed at the requested instant reports the
	// did-not-exist-at verdict (#4455 wording), still not an error.
	never := time.Now().UTC().Add(-time.Hour)
	out3, err3 := tol.Execute(context.Background(), json.RawMessage(
		`{"scope":"project","key":"never-saved","as_of":"`+never.Format(time.RFC3339Nano)+`"}`))
	if err3 != nil {
		t.Fatal(err3)
	}
	if out3.IsError || !strings.Contains(out3.Content, "did not exist at") {
		t.Fatalf("never-held key must report did-not-exist-at, got: %s", out3.Content)
	}
}
