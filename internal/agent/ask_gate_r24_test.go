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

	a.executeToolWithPermission(context.Background(), tc)

	a.executeToolWithPermission(context.Background(), tc)

	rep, err := audit.Verify(path)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if rep.Entries < 2 {
		t.Fatalf("expected >=2 audited entries, got %d", rep.Entries)
	}
}

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

	last := a.executeToolWithPermission(context.Background(), tc)
	if !strings.Contains(last.Content, "denied twice") {
		t.Errorf("suppressed re-ask missing throttle guidance:\n%s", last.Content)
	}
	if prompts != 2 {
		t.Fatalf("suppressed re-ask still prompted the user: %d prompts", prompts)
	}
}

func TestAskGateNilThrottleSafe(t *testing.T) {
	a := &Agent{policy: &stubAskPolicy{mode: permission.AutoMode}}
	a.onApproval = func(ctx context.Context, name, args string) permission.Decision {
		return permission.Allow
	}
	res := a.executeToolWithPermission(context.Background(),
		provider.ToolCallDelta{Name: "read_file", Arguments: json.RawMessage(`{}`)})
	_ = res
}
