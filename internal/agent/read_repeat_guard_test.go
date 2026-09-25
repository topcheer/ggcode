package agent

import (
	"strings"
	"testing"
)

var grepArgs = []byte(`{"pattern": "TODO", "path": "/workspace"}`)

func TestReadRepeatGuardAdvisesOnIdenticalRepeat(t *testing.T) {
	g := newReadRepeatGuard()
	if hint := g.observe("grep", grepArgs); hint != "" {
		t.Fatalf("first occurrence must not advise, got %q", hint)
	}
	hint := g.observe("grep", grepArgs)
	if !strings.Contains(hint, "[read-repeat]") {
		t.Fatalf("second identical occurrence must advise, got %q", hint)
	}
	if !strings.Contains(hint, "#2") {
		t.Fatalf("advisory must carry occurrence count, got %q", hint)
	}
	if hint := g.observe("grep", grepArgs); !strings.Contains(hint, "#3") {
		t.Fatalf("third identical occurrence must advise with count #3, got %q", hint)
	}
}

func TestReadRepeatGuardMutationResetsRedundancy(t *testing.T) {
	g := newReadRepeatGuard()
	g.observe("read_file", []byte(`{"path": "a.go"}`))
	// A mutating call in between makes the re-read a legitimate verify loop.
	if hint := g.observe("run_command", []byte(`{"command": "go build ./..."}`)); hint != "" {
		t.Fatalf("mutating call must not advise, got %q", hint)
	}
	if g.mutations != 1 {
		t.Fatalf("run_command must bump mutation counter, got %d", g.mutations)
	}
	if hint := g.observe("read_file", []byte(`{"path": "a.go"}`)); hint != "" {
		t.Fatalf("re-read after mutation is a legitimate verify loop, got %q", hint)
	}
	// But a third identical read with STILL no mutation in between advises.
	if hint := g.observe("read_file", []byte(`{"path": "a.go"}`)); !strings.Contains(hint, "[read-repeat]") {
		t.Fatalf("identical read with no intervening mutation must advise, got %q", hint)
	}
}

func TestReadRepeatGuardShellMutationResetsToo(t *testing.T) {
	g := newReadRepeatGuard()
	g.observe("grep", grepArgs)
	// go fmt is not in mutatingToolNames but commandMayRewriteWorkspace covers it.
	g.observe("run_command", []byte(`{"command": "go fmt ./..."}`))
	if g.mutations != 1 {
		t.Fatalf("workspace-rewriting shell command must bump mutations, got %d", g.mutations)
	}
	if hint := g.observe("grep", grepArgs); hint != "" {
		t.Fatalf("re-grep after shell mutation must not advise, got %q", hint)
	}
}

func TestReadRepeatGuardDifferentArgsNoAdvisory(t *testing.T) {
	g := newReadRepeatGuard()
	g.observe("grep", grepArgs)
	if hint := g.observe("grep", []byte(`{"pattern": "FIXME", "path": "/workspace"}`)); hint != "" {
		t.Fatalf("different arguments are a different signature, got %q", hint)
	}
}

func TestReadRepeatGuardArgOrderInsensitive(t *testing.T) {
	g := newReadRepeatGuard()
	g.observe("grep", []byte(`{"path": "/workspace", "pattern": "TODO"}`))
	hint := g.observe("grep", []byte(`{"pattern": "TODO", "path": "/workspace"}`))
	if !strings.Contains(hint, "[read-repeat]") {
		t.Fatalf("key-order-only difference must share one signature and advise, got %q", hint)
	}
}

func TestReadRepeatGuardPollingToolsExempt(t *testing.T) {
	g := newReadRepeatGuard()
	for i := 0; i < 5; i++ {
		if hint := g.observe("wait_command", []byte(`{"job_id": "j1"}`)); hint != "" {
			t.Fatalf("polling tools are exempt (identical repeats are normal), got %q", hint)
		}
	}
}

func TestReadRepeatGuardMutatingRepeatNeverAdvises(t *testing.T) {
	g := newReadRepeatGuard()
	for i := 0; i < 4; i++ {
		if hint := g.observe("run_command", []byte(`{"command": "make test"}`)); hint != "" {
			t.Fatalf("mutating tools never advise, got %q", hint)
		}
	}
}

func TestReadRepeatGuardAdvisoryCap(t *testing.T) {
	g := newReadRepeatGuard()
	advised := 0
	for i := 0; i < 20; i++ {
		if hint := g.observe("grep", grepArgs); hint != "" {
			advised++
		}
	}
	if advised != readRepeatMaxAdvisories {
		t.Fatalf("advisories must cap at %d, got %d", readRepeatMaxAdvisories, advised)
	}
}

func TestReadRepeatGuardNilReceiverSafe(t *testing.T) {
	var g *readRepeatGuard
	if hint := g.observe("grep", grepArgs); hint != "" {
		t.Fatalf("nil guard must be a no-op, got %q", hint)
	}
}

func TestReadRepeatSignatureStableAcrossInvocations(t *testing.T) {
	a := readRepeatSignature("grep", grepArgs)
	b := readRepeatSignature("grep", grepArgs)
	if a != b {
		t.Fatalf("signature must be deterministic")
	}
	if readRepeatSignature("grep", []byte(`not json`)) == "" {
		t.Fatalf("non-JSON args must still produce a signature")
	}
	if readRepeatSignature("grep", grepArgs) == readRepeatSignature("glob", grepArgs) {
		t.Fatalf("different tools must not share a signature")
	}
}
