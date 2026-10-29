package agent

// #3547 (reopen) probe: batch write tools must expand PER FILE in
// invariantOpTargets - a protected path in ANY position fires the rule.
// The first fix only extracted files[0], so moving the sensitive file to
// position 2 silently bypassed path_glob block rules.

import (
	"encoding/json"
	"testing"
)

func TestIssue3547B_MultiFileWriteExpandsPerFile(t *testing.T) {
	args := json.RawMessage(`{"files":[{"path":"/repo/a.py","content":"x"},{"path":"/repo/.env","content":"y"}]}`)
	got := invariantOpTargets("multi_file_write", args)
	if len(got) != 2 {
		t.Fatalf("mfw must expand to 2 targets, got %d: %+v", len(got), got)
	}
	if got[0].Op != "write" || got[0].Target != "/repo/a.py" {
		t.Fatalf("target[0] = %+v", got[0])
	}
	if got[1].Op != "write" || got[1].Target != "/repo/.env" {
		t.Fatalf("target[1] = %+v - the non-first .env entry must be present", got[1])
	}
}

func TestIssue3547B_NonFirstProtectedPathBlocks(t *testing.T) {
	// Engine-level: a path_glob block on .env must fire when .env is the
	// SECOND file in the batch (the exact reopen scenario).
	e := newTestEngine(t, `{"invariants":[`+
		`{"id":"env","op":"write","path_glob":"**/.env","mode":"block","message":"env is protected"}`+
		`]}`)
	v := e.check("multi_file_write", json.RawMessage(`{"files":[{"path":"/repo/a.py","content":"x"},{"path":"/repo/.env","content":"SECRET=1"}]}`))
	if v == nil {
		t.Fatal("block rule on .env must fire even when .env is not the first file")
	}
	if v.Target != "/repo/.env" {
		t.Fatalf("violation target = %q, want /repo/.env", v.Target)
	}
}

func TestIssue3547B_BatchReplaceAlsoExpands(t *testing.T) {
	args := json.RawMessage(`{"files":["/r/ok.go","/r/.env"],"pattern":"a","replacement":"b"}`)
	got := invariantOpTargets("batch_replace", args)
	if len(got) != 2 || got[1].Target != "/r/.env" {
		t.Fatalf("batch_replace must expand per file (same hide-behind risk): %+v", got)
	}
}

func TestIssue3547B_DegradedShapesKeepLegacyPair(t *testing.T) {
	for _, args := range []json.RawMessage{[]byte(`not json`), []byte(`{"files":[]}`)} {
		got := invariantOpTargets("multi_file_write", args)
		if len(got) != 1 {
			t.Fatalf("degraded input must fall back to one legacy pair, got %+v", got)
		}
	}
}
