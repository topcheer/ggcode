package agent

import (
	"encoding/json"
	"testing"
)

// Issue #3254: the r454 invariant engine classified a batch file_ops call
// by its FIRST mutation operation only. Three defects, one root cause:
//
//  1. Miss (check side): a block rule on op X was bypassed by placing a
//     benign mutation first in the batch.
//  2. Field mismatch: mkdir's target lives in the SOURCE field (the tool
//     runs os.MkdirAll(source)); the old classifier read Destination, so
//     mkdir targets never matched any glob and never entered runProducts.
//  3. False positive (record side): products created inside a batch whose
//     classified op was "delete" were never registered, so the documented
//     "only delete what this run created" scenario blocked legitimate
//     deletes of run-created paths.
//
// The fix expands every file_ops call to one (op, target) pair per
// mutation operation, for both check() and the recordProduct wiring.

// Defect 1: a blocked mkdir placed after a benign delete in one batch must
// still be checked and blocked (the old code classified the whole call as
// "delete" and skipped the mkdir rule entirely).
func TestIssue3254_BatchBypassBlocked(t *testing.T) {
	e := newTestEngine(t, `{"invariants":[
		{"id":"m1","on_tools":["file_ops"],"op":"mkdir","path_glob":"/tmp/**","mode":"block","message":"no mkdir in tmp"}
	]}`)
	batch := argsJSON(t, map[string]any{"operations": []any{
		map[string]string{"action": "delete", "source": "/tmp/old"},
		map[string]string{"action": "mkdir", "source": "/tmp/new"},
	}})
	v := e.check("file_ops", batch)
	if v == nil || v.Inv.ID != "m1" {
		t.Fatalf("expected m1 block violation on second op, got %+v", v)
	}
	if v.Op != "mkdir" || v.Target != "/tmp/new" {
		t.Errorf("violation op/target = %q/%q, want mkdir//tmp/new", v.Op, v.Target)
	}
	// Reversed order must block too (and reports the mkdir target).
	batchRev := argsJSON(t, map[string]any{"operations": []any{
		map[string]string{"action": "mkdir", "source": "/tmp/new"},
		map[string]string{"action": "delete", "source": "/tmp/old"},
	}})
	if v := e.check("file_ops", batchRev); v == nil || v.Inv.ID != "m1" {
		t.Fatalf("expected m1 violation on first op, got %+v", v)
	}
	// Benign batch (no mkdir) stays clean.
	benign := argsJSON(t, map[string]any{"operations": []any{
		map[string]string{"action": "delete", "source": "/tmp/old"},
	}})
	if v := e.check("file_ops", benign); v != nil {
		t.Errorf("benign batch tripped rule: %+v", v)
	}
}

// Defect 2: mkdir's target is the SOURCE field (tool: os.MkdirAll(source)).
// A pure-mkdir call whose glob matches the source path must violate; the
// old Destination read produced an empty target that matched nothing.
func TestIssue3254_MkdirTargetIsSourceField(t *testing.T) {
	e := newTestEngine(t, `{"invariants":[
		{"id":"mk","on_tools":["file_ops"],"op":"mkdir","path_glob":"**/newdir","mode":"block","message":"controlled dirs"}
	]}`)
	// source-only payload: destination absent, exactly how the tool schema
	// requires it (source is the required field for mkdir).
	call := argsJSON(t, map[string]any{"operations": []any{
		map[string]string{"action": "mkdir", "source": "/w/newdir"},
	}})
	v := e.check("file_ops", call)
	if v == nil || v.Inv.ID != "mk" {
		t.Fatalf("expected mk violation with source-field target, got %+v", v)
	}
	if v.Target != "/w/newdir" {
		t.Errorf("target = %q, want /w/newdir (mkdir target is source field)", v.Target)
	}
}

// move keeps destination as its target (moving INTO a path is the mutation).
func TestIssue3254_MoveTargetIsDestination(t *testing.T) {
	e := newTestEngine(t, `{"invariants":[
		{"id":"mv","on_tools":["file_ops"],"op":"move","path_glob":"/vault/**","mode":"block","message":"vault is cold storage"}
	]}`)
	call := argsJSON(t, map[string]any{"operations": []any{
		map[string]string{"action": "move", "source": "/tmp/a", "destination": "/vault/a"},
	}})
	v := e.check("file_ops", call)
	if v == nil || v.Inv.ID != "mv" {
		t.Fatalf("expected mv violation, got %+v", v)
	}
	if v.Target != "/vault/a" {
		t.Errorf("target = %q, want /vault/a", v.Target)
	}
}

// Defect 3: the recordProduct wiring must register EVERY write-class
// target a successful batch produced - including a mkdir hidden behind a
// delete - or the created_by_run predicate false-positives later.
// This mirrors the agent_tool.go wiring loop verbatim.
func TestIssue3254_CreatedByRunRegistersBatchProducts(t *testing.T) {
	e := newTestEngine(t, `{"invariants":[
		{"id":"own","on_tools":["file_ops"],"op":"delete","created_by_run":true,"mode":"block","message":"only delete what this run created"}
	]}`)
	// A successful mixed batch: [delete /a, mkdir /b]. The wiring (now
	// operation-granular) registers /b; the old first-op classifier called
	// the whole batch "delete" and registered nothing.
	batch := argsJSON(t, map[string]any{"operations": []any{
		map[string]string{"action": "delete", "source": "/a"},
		map[string]string{"action": "mkdir", "source": "/b"},
	}})
	for _, ot := range invariantOpTargets("file_ops", batch) {
		if ot.Op == "write" || ot.Op == "mkdir" || ot.Op == "move" {
			e.recordProduct(ot.Target)
		}
	}
	// Deleting the run-created /b must NOT violate.
	delB := argsJSON(t, map[string]any{"operations": []any{map[string]string{"action": "delete", "source": "/b"}}})
	if v := e.check("file_ops", delB); v != nil {
		t.Errorf("delete of run-created /b falsely blocked: %+v", v)
	}
	// Deleting an unregistered path still violates (predicate still live).
	delC := argsJSON(t, map[string]any{"operations": []any{map[string]string{"action": "delete", "source": "/c"}}})
	if v := e.check("file_ops", delC); v == nil || v.Inv.ID != "own" {
		t.Errorf("expected own violation for /c, got %+v", v)
	}
}

// Block wins over warn across the expanded pairs: a warn hit on an earlier
// operation must not mask a block hit on a later one.
func TestIssue3254_BlockWinsOverWarn(t *testing.T) {
	e := newTestEngine(t, `{"invariants":[
		{"id":"w","on_tools":["file_ops"],"op":"delete","mode":"warn","message":"watch deletes"},
		{"id":"b","on_tools":["file_ops"],"op":"mkdir","path_glob":"/tmp/**","mode":"block","message":"no tmp mkdir"}
	]}`)
	batch := argsJSON(t, map[string]any{"operations": []any{
		map[string]string{"action": "delete", "source": "/x"},
		map[string]string{"action": "mkdir", "source": "/tmp/n"},
	}})
	v := e.check("file_ops", batch)
	if v == nil || v.Inv.ID != "b" {
		t.Fatalf("expected block rule b to win over warn rule w, got %+v", v)
	}
}

// Expansion unit tests: the classifier's contract per tool shape.
func TestIssue3254_OpTargetsExpansion(t *testing.T) {
	batch := argsJSON(t, map[string]any{"operations": []any{
		map[string]string{"action": "delete", "source": "/d"},
		map[string]string{"action": "mkdir", "source": "/m"},
		map[string]string{"action": "move", "source": "/s", "destination": "/dst"},
	}})
	got := invariantOpTargets("file_ops", batch)
	want := []invariantOpTarget{
		{Op: "delete", Target: "/d"},
		{Op: "mkdir", Target: "/m"},
		{Op: "move", Target: "/dst"},
	}
	if len(got) != len(want) {
		t.Fatalf("expanded %d pairs, want %d: %+v", len(got), len(want), got)
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("pair[%d] = %+v, want %+v", i, got[i], w)
		}
	}
	// Garbage JSON degrades to the legacy single empty pair (check stays a
	// no-op for glob/created rules, byte-for-byte like the old classifier).
	if got := invariantOpTargets("file_ops", json.RawMessage(`{bad`)); len(got) != 1 || got[0].Op != "" || got[0].Target != "" {
		t.Errorf("garbage JSON expansion = %+v, want single empty pair", got)
	}
	// Non-batch tool: single pair via the legacy classifiers (write_file).
	wf := invariantOpTargets("write_file", argsJSON(t, map[string]string{"file_path": "/f.go", "content": "x"}))
	if len(wf) != 1 || wf[0].Op != "write" || wf[0].Target != "/f.go" {
		t.Errorf("write_file expansion = %+v, want single write pair", wf)
	}
	// file_ops with no mutation ops expands to zero pairs (nothing to check).
	empty := invariantOpTargets("file_ops", argsJSON(t, map[string]any{"operations": []any{}}))
	if len(empty) != 0 {
		t.Errorf("empty operations expansion = %+v, want zero pairs", empty)
	}
}
