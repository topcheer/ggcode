package agent

// #3370 probes: timeout/displaced approval outcomes are NOT user denials -
// the audit must say ask_timeout, the result must not claim user rejection,
// and the R24 ask throttle must stay disarmed (the user never said no).

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/audit"
	"github.com/topcheer/ggcode/internal/permission"
	"github.com/topcheer/ggcode/internal/provider"
)

func executeAskGateFor3370(t *testing.T, a *Agent, tc provider.ToolCallDelta) string {
	t.Helper()
	res := a.executeToolWithPermission(context.Background(), tc)
	return res.Content
}

func TestIssue3370_TimeoutNotAttributedToUser(t *testing.T) {
	path := t.TempDir() + "/audit.jsonl"
	lg, err := audit.Open(path, "sess-3370")
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
		return permission.DenyTimeout
	}
	tc := provider.ToolCallDelta{Name: "edit_file", Arguments: json.RawMessage(`{"file_path":"/tmp/a/x.go"}`)}

	// Two timeouts must not arm the R24 throttle (two USER denials would).
	for i := 0; i < 2; i++ {
		content := executeAskGateFor3370(t, a, tc)
		if strings.Contains(content, "User rejected") {
			t.Fatalf("timeout must not be reported as user rejection: %q", content)
		}
		if !strings.Contains(content, "not answered") {
			t.Fatalf("timeout result must say the request was not answered: %q", content)
		}
	}
	// Third call must still reach the approval handler (throttle disarmed).
	executeAskGateFor3370(t, a, tc)
	if calls != 3 {
		t.Fatalf("ask throttle must stay disarmed after timeouts: handler called %d times, want 3", calls)
	}

	// The audit chain records the honest attribution.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read audit: %v", err)
	}
	if !strings.Contains(string(data), `"ask_timeout"`) {
		t.Fatal("audit must contain ask_timeout entries for timed-out gates")
	}
	if strings.Contains(string(data), `"user_denied"`) {
		t.Fatal("audit must not record user_denied for timed-out gates")
	}
}

func TestIssue3370_DisplacedNotUserDenial(t *testing.T) {
	a := &Agent{
		policy:      &stubAskPolicy{mode: permission.AutoMode},
		askThrottle: permission.NewAskThrottle(),
	}
	a.onApproval = func(ctx context.Context, name, args string) permission.Decision {
		return permission.DenyDisplaced
	}
	tc := provider.ToolCallDelta{Name: "edit_file", Arguments: json.RawMessage(`{"file_path":"/tmp/a/y.go"}`)}
	content := executeAskGateFor3370(t, a, tc)
	if strings.Contains(content, "User rejected") || !strings.Contains(content, "displaced") {
		t.Fatalf("displaced outcome must be reported as displaced, not user rejection: %q", content)
	}
}

func TestIssue3370_DecisionStrings(t *testing.T) {
	if permission.DenyTimeout.String() != "deny_timeout" || permission.DenyDisplaced.String() != "deny_displaced" {
		t.Fatalf("new decisions must stringify distinctly: %q %q", permission.DenyTimeout, permission.DenyDisplaced)
	}
}
