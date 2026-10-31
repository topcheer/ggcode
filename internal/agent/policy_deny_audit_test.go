package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/permission"
	"github.com/topcheer/ggcode/internal/provider"
)

// Policy-rule denials must be auditable (sa-17 / policy-engine governance):
// the user's OWN deny decisions were recorded, but the user's CONFIGURED
// rules firing left zero trace - the single most compliance-relevant
// permission event was the only silent one.
func TestPolicyDeniedIsAudited(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	t.Setenv(auditLedgerEnv, path)
	st := newAuditLedgerState()
	if st == nil || st.ledger == nil {
		t.Fatal("ledger must open from env")
	}
	defer st.ledger.Close()

	pol := permission.NewConfigPolicy(map[string]permission.Decision{
		"write_file": permission.Deny,
	}, nil)
	a := &Agent{
		auditLedger: st,
		policy:      pol,
		sessionID:   "sess-pd",
	}

	res := a.executeToolWithPermission(context.Background(), provider.ToolCallDelta{
		Name:      "write_file",
		Arguments: json.RawMessage(`{"path":"/tmp/x","content":"x"}`),
	})
	if !res.IsError {
		t.Fatal("policy-deny must produce an error result")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read ledger: %v", err)
	}
	if !strings.Contains(string(data), `"policy_denied"`) {
		t.Fatalf("policy denial must be recorded in the audit ledger, got:\n%s", data)
	}
	if !strings.Contains(string(data), "write_file") {
		t.Fatal("audited entry must name the denied tool")
	}
}

// Default-off contract preserved: without the ledger env var the added
// calls stay inert no-ops.
func TestPolicyDeniedAuditInertWhenLedgerOff(t *testing.T) {
	t.Setenv(auditLedgerEnv, "")
	st := newAuditLedgerState()
	pol := permission.NewConfigPolicy(map[string]permission.Decision{
		"write_file": permission.Deny,
	}, nil)
	a := &Agent{auditLedger: st, policy: pol}
	res := a.executeToolWithPermission(context.Background(), provider.ToolCallDelta{
		Name:      "write_file",
		Arguments: json.RawMessage(`{}`),
	})
	if !res.IsError {
		t.Fatal("deny must still error with the ledger off")
	}
	// No panic and nothing written (state inert) is the contract.
}
