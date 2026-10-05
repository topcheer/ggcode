package agent

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/audit"
	"github.com/topcheer/ggcode/internal/permission"
	"github.com/topcheer/ggcode/internal/provider"
)

// R24 (#3368) wiring-level coverage: the Ask gate must (a) audit
// approve/deny decisions, and (b) suppress re-asks after repeated denials
// (ATR-2026-00118 pattern 1). The throttle unit tests live in
// internal/permission/ask_throttle_test.go; these prove the agent-side
// wiring in executeToolWithPermission.

type stubAskPolicy struct{ mode permission.PermissionMode }

func (p *stubAskPolicy) Check(toolName string, input json.RawMessage) (permission.Decision, error) {
	return permission.Ask, nil
}
func (p *stubAskPolicy) Mode() permission.PermissionMode        { return p.mode }
func (p *stubAskPolicy) IsDangerous(command string) bool        { return false }
func (p *stubAskPolicy) AllowedPath(path string) bool           { return true }
func (p *stubAskPolicy) AllowedPathForTool(n, path string) bool { return true }
func (p *stubAskPolicy) AllowCommandPattern(pattern string)     {}
func (p *stubAskPolicy) BlocksAutoApprove(toolName string, input json.RawMessage) bool {
	return false
}
func (p *stubAskPolicy) SetOverride(toolName string, decision permission.Decision) {}

// Deny and approve decisions must land in the audit chain as first-class
// approval events, not only as downstream tool errors.
func TestAskGateAuditsApproveAndDeny(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	lg, err := audit.Open(path, "sess-r24")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer lg.Close()

	a := &Agent{
		policy:      &stubAskPolicy{mode: permission.AutoMode},
		auditLedger: &auditLedgerState{ledger: lg},
		askThrottle: permission.NewAskThrottle(),
	}
	calls := 0
	a.onApproval = func(ctx context.Context, name, args string) permission.Decision {
		calls++
		if calls == 1 {
			return permission.Allow
		}
		return permission.Deny
	}
	tc := provider.ToolCallDelta{Name: "edit_file", Arguments: json.RawMessage(`{"file_path":"/tmp/a/x.go"}`)}

	// 1st call: user approves. The tool itself is not registered on this
	// bare Agent, so execution fails - but the APPROVAL must be audited
	// before execution is attempted.
	a.executeToolWithPermission(context.Background(), tc)
	// 2nd call: user denies.
	a.executeToolWithPermission(context.Background(), tc)

	rep, err := audit.Verify(path)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if rep.Entries < 2 {
		t.Fatalf("expected >=2 audited entries, got %d", rep.Entries)
	}
}

// After the same key is denied twice, a third attempt must NOT prompt the
// user again: the ask throttle answers deny directly.
func TestAskGateThrottleSuppressesThirdAsk(t *testing.T) {
	a := &Agent{
		policy:      &stubAskPolicy{mode: permission.AutoMode},
		askThrottle: permission.NewAskThrottle(),
	}
	prompts := 0
	a.onApproval = func(ctx context.Context, name, args string) permission.Decision {
		prompts++
		return permission.Deny
	}
	tc := provider.ToolCallDelta{Name: "edit_file", Arguments: json.RawMessage(`{"file_path":"/tmp/b/y.go"}`)}

	for i := 0; i < 3; i++ {
		res := a.executeToolWithPermission(context.Background(), tc)
		if !res.IsError {
			t.Fatalf("call %d: expected denial", i+1)
		}
	}
	if prompts != 2 {
		t.Fatalf("expected exactly 2 user prompts (3rd suppressed), got %d", prompts)
	}
	// The suppressed third denial must be labeled as throttled.
	// (Second and third results are denials; the third must carry the
	// throttle guidance.)
	last := a.executeToolWithPermission(context.Background(), tc)
	if !strings.Contains(last.Content, "denied twice") {
		t.Errorf("suppressed re-ask missing throttle guidance:\n%s", last.Content)
	}
	if prompts != 2 {
		t.Fatalf("suppressed re-ask still prompted the user: %d prompts", prompts)
	}
}

// A nil throttle must not panic the Ask path (zero-value Agent safety).
func TestAskGateNilThrottleSafe(t *testing.T) {
	a := &Agent{policy: &stubAskPolicy{mode: permission.AutoMode}}
	a.onApproval = func(ctx context.Context, name, args string) permission.Decision {
		return permission.Allow
	}
	res := a.executeToolWithPermission(context.Background(),
		provider.ToolCallDelta{Name: "read_file", Arguments: json.RawMessage(`{}`)})
	_ = res // no panic is the assertion
}
