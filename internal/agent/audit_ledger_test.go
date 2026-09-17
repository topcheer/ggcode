package agent

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/audit"
	"github.com/topcheer/ggcode/internal/tool"
)

// Off by default: state is non-nil (issue #341 pointer-field guard) but the
// ledger inside is nil, so audit calls are inert no-ops.
func TestAuditLedgerOffByDefault(t *testing.T) {
	t.Setenv(auditLedgerEnv, "")
	st := newAuditLedgerState()
	if st == nil {
		t.Fatal("state must never be nil")
	}
	if st.ledger != nil {
		t.Error("ledger should be nil when the env var is unset/empty")
	}
	// Method calls on a disabled state must not panic and must not write.
	a := &Agent{auditLedger: st}
	a.auditToolResult("read_file", json.RawMessage(`{}`), audit.StatusOK, "", time.Millisecond)
	a.auditAnchor()
}

// Setting the env var opens the ledger file.
func TestAuditLedgerOpensFromEnv(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	t.Setenv(auditLedgerEnv, path)
	st := newAuditLedgerState()
	if st == nil || st.ledger == nil {
		t.Fatal("expected an open ledger")
	}
	if st.ledger.Path() != path {
		t.Errorf("Path = %s, want %s", st.ledger.Path(), path)
	}
	defer st.ledger.Close()
}

// auditToolExecution derives ok/error from the result and entries verify as
// a hash chain, carrying the session ID and bounded error text.
func TestAuditToolExecutionWritesChain(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	lg, err := audit.Open(path, "")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	a := &Agent{auditLedger: &auditLedgerState{ledger: lg}, sessionID: "sess-9"}

	a.auditToolExecution("read_file", json.RawMessage(`{"path":"a.go"}`),
		tool.Result{Content: "ok"}, nil, 3*time.Millisecond)
	a.auditToolExecution("run_command", json.RawMessage(`{"command":"boom"}`),
		tool.Result{Content: "failed: " + string(make([]byte, 500)), IsError: true}, nil, 4*time.Millisecond)
	a.auditToolExecution("panic_tool", json.RawMessage(`{}`), tool.Result{}, errPanicky, time.Millisecond)

	if err := lg.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	rep, err := audit.Verify(path)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if !rep.OK() || rep.Entries != 3 {
		t.Fatalf("report = %+v, want clean 3-entry chain", rep)
	}
}

// errPanicky is a non-nil error for the Go-error audit path.
var errPanicky = &auditTestError{"panic recovered"}

type auditTestError struct{ msg string }

func (e *auditTestError) Error() string { return e.msg }
