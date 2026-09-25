package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/audit"
)

// newTraceGuardTestAgent builds an Agent with an enabled audit ledger at a
// temp path, mirroring how newAuditLedgerState wires GGCODE_AUDIT_LEDGER.
func newTraceGuardTestAgent(t *testing.T) (*Agent, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	lg, err := audit.Open(path, "sess-guard")
	if err != nil {
		t.Fatalf("audit.Open: %v", err)
	}
	a := &Agent{}
	a.auditLedger = &auditLedgerState{ledger: lg, path: path}
	return a, path
}

func TestTraceGuardBlocksLedgerDeletion(t *testing.T) {
	a, path := newTraceGuardTestAgent(t)

	cases := []struct {
		toolName string
		args     string
	}{
		{"file_ops", `{"operations":[{"action":"delete","source":"` + path + `"}]}`},
		{"file_ops", `{"operations":[{"action":"move","source":"` + path + `","destination":"/tmp/x"}]}`},
		{"write_file", `{"path":"` + path + `","content":"x"}`},
		{"edit_file", `{"file_path":"` + path + `","old_text":"a","new_text":"b"}`},
		{"run_command", `{"command":"rm -f ` + path + `"}`},
		{"run_command", `{"command":"rm -f ` + path + `.head"}`},
		{"run_command", `{"command":"shred -u ` + path + `"}`},
	}
	for _, tc := range cases {
		res, denied := a.enforceTraceGuard(tc.toolName, json.RawMessage(tc.args), time.Millisecond)
		if !denied {
			t.Fatalf("%s args=%s: expected denial", tc.toolName, tc.args)
		}
		if !res.IsError {
			t.Fatalf("%s: denial result must be an error result", tc.toolName)
		}
		if !strings.Contains(res.Content, "trace-integrity guard") {
			t.Fatalf("%s: denial message should name the guard, got %q", tc.toolName, res.Content)
		}
	}

	// The blocked attempts must be sealed into the ledger as StatusInvalid
	// entries with a verifiable chain.
	rep, err := audit.Verify(path)
	if err != nil {
		t.Fatalf("ledger verify: %v", err)
	}
	if !rep.OK() {
		t.Fatalf("ledger chain should verify after guard denials: %+v", rep)
	}
	if rep.Entries != len(cases) {
		t.Fatalf("expected %d audited invalid attempts, got %d", len(cases), rep.Entries)
	}
}

func TestTraceGuardTildeAndSuffixForms(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("no home dir: %v", err)
	}
	// Create the ledger under a real home-backed directory so the
	// ~-contracted form actually applies (t.TempDir() may live elsewhere).
	dir, err := os.MkdirTemp(home, ".ggcode-traceguard-test-")
	if err != nil {
		t.Skipf("cannot create temp dir under home: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	path := filepath.Join(dir, "audit.jsonl")
	lg, err := audit.Open(path, "sess-guard")
	if err != nil {
		t.Fatalf("audit.Open: %v", err)
	}
	a := &Agent{}
	a.auditLedger = &auditLedgerState{ledger: lg, path: path}

	tilde := "~" + strings.TrimPrefix(path, home)
	if _, denied := a.traceGuardCheck("file_ops", json.RawMessage(`{"source":"`+tilde+`"}`)); !denied {
		t.Fatalf("tilde form %q should be caught", tilde)
	}
	base := filepath.Base(path)
	if _, denied := a.traceGuardCheck("write_file", json.RawMessage(`{"path":"./`+base+`"}`)); !denied {
		t.Fatalf("suffix form ./%s should be caught", base)
	}
}

func TestTraceGuardAllowsUnrelatedPaths(t *testing.T) {
	a, path := newTraceGuardTestAgent(t)

	allow := []string{
		`{"path":"/tmp/normal.go","content":"ok"}`,
		`{"command":"rm -f /tmp/unrelated.jsonl"}`,
		`{"path":"` + filepath.Join(t.TempDir(), "myaudit.jsonl") + `"}`,
		`{"pattern":"audit","path":"` + filepath.Dir(path) + `"}`,
	}
	for _, args := range allow {
		if _, denied := a.traceGuardCheck("write_file", json.RawMessage(args)); denied {
			t.Fatalf("unrelated args should not be denied: %s", args)
		}
	}
}

func TestTraceGuardInertWithoutLedger(t *testing.T) {
	a := &Agent{}
	a.auditLedger = &auditLedgerState{} // enabled field present, ledger nil (env unset)
	if _, denied := a.traceGuardCheck("run_command", json.RawMessage(`{"command":"rm -rf /anything"}`)); denied {
		t.Fatal("guard must be inert when no ledger is configured")
	}
	res, denied := a.enforceTraceGuard("file_ops", json.RawMessage(`{"action":"delete","source":"/tmp/audit.jsonl"}`), 0)
	if denied || res.IsError || res.Content != "" {
		t.Fatal("enforceTraceGuard must be a no-op without an active ledger")
	}
}
