package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/audit"
	"github.com/topcheer/ggcode/internal/permission"
	"github.com/topcheer/ggcode/internal/provider"
)

// #3370 regression: an approval that ends in timeout/cancellation is
// fail-closed (tool does not run) but must never be attributed to the user -
// audit records ask_timeout, approval memory learns nothing, and two
// timeouts must NOT arm the ask throttle with a false "denied twice" story.

func new3370Agent(onApproval func(ctx context.Context, name, args string) permission.Decision) *Agent {
	a := &Agent{
		policy:         &stubAskPolicy{mode: permission.AutoMode},
		askThrottle:    permission.NewAskThrottle(),
		approvalMemory: permission.NewApprovalMemory(),
	}
	a.onApproval = onApproval
	return a
}

var tc3370 = provider.ToolCallDelta{Name: "edit_file", Arguments: json.RawMessage(`{"file_path":"/tmp/c/z.go"}`)}

// Timeout at the gate: fail-closed, message names the timeout and never
// claims the user denied/rejected anything.
func TestAskGateTimeoutFailClosedWithoutUserBlame(t *testing.T) {
	a := new3370Agent(func(ctx context.Context, name, args string) permission.Decision {
		return permission.Timeout
	})
	res := a.executeToolWithPermission(context.Background(), tc3370)
	if !res.IsError {
		t.Fatal("timeout must be fail-closed (IsError)")
	}
	for _, banned := range []string{"denied", "rejected"} {
		if strings.Contains(strings.ToLower(res.Content), banned) {
			t.Errorf("timeout message must not blame the user (%q present):\n%s", banned, res.Content)
		}
	}
	if !strings.Contains(res.Content, "without a user decision") {
		t.Errorf("timeout message should name the non-decision:\n%s", res.Content)
	}
}

// The issue's headline scenario: TWO consecutive timeouts must leave the ask
// throttle unarmed - the third ask still prompts (no suppression, no false
// "denied twice" message).
func TestAskGateDoubleTimeoutDoesNotThrottle(t *testing.T) {
	prompts := 0
	a := new3370Agent(func(ctx context.Context, name, args string) permission.Decision {
		prompts++
		return permission.Timeout
	})
	for i := 0; i < 3; i++ {
		res := a.executeToolWithPermission(context.Background(), tc3370)
		if !res.IsError {
			t.Fatalf("call %d: expected fail-closed", i+1)
		}
	}
	if prompts != 3 {
		t.Fatalf("two timeouts must not arm the throttle: expected 3 prompts, got %d", prompts)
	}
	if a.askThrottle.ShouldSuppress(tc3370.Name, tc3370.Arguments) {
		t.Fatal("askThrottle armed by timeouts - non-decisions must not count as denials")
	}
}

// A displaced/interrupted approval (Cancelled) mirrors the timeout handling.
func TestAskGateCancelledMirrorsTimeout(t *testing.T) {
	a := new3370Agent(func(ctx context.Context, name, args string) permission.Decision {
		return permission.Cancelled
	})
	res := a.executeToolWithPermission(context.Background(), tc3370)
	if !res.IsError {
		t.Fatal("cancelled must be fail-closed")
	}
	if strings.Contains(strings.ToLower(res.Content), "denied") {
		t.Errorf("cancelled message must not blame the user:\n%s", res.Content)
	}
	// And it must not count toward the throttle either.
	if a.askThrottle.ShouldSuppress(tc3370.Name, tc3370.Arguments) {
		t.Fatal("cancelled non-decision armed the ask throttle")
	}
}

// Timeout/cancel non-decisions must not enter the approval-memory learning
// sample: after a timeout, RecordApproval-style auto-approve must still need
// the full 3 real approvals (i.e. the timeout recorded nothing).
func TestAskGateTimeoutNotInApprovalMemory(t *testing.T) {
	a := new3370Agent(func(ctx context.Context, name, args string) permission.Decision {
		return permission.Timeout
	})
	a.executeToolWithPermission(context.Background(), tc3370)
	if a.approvalMemory.ShouldAutoApprove(tc3370.Name, tc3370.Arguments) {
		t.Fatal("timeout leaked into approval memory as a learned pattern")
	}
}

// The audit ledger must record the timeout as ask_timeout (wiring the
// previously dead StatusAskTimeout constant), not as a user denial.
func TestAskGateTimeoutAuditedAsAskTimeout(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	lg, err := audit.Open(path, "sess-3370")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer lg.Close()
	a := new3370Agent(func(ctx context.Context, name, args string) permission.Decision {
		return permission.Timeout
	})
	a.auditLedger = &auditLedgerState{ledger: lg}

	a.executeToolWithPermission(context.Background(), tc3370)
	lg.Close()

	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open ledger: %v", err)
	}
	defer f.Close()
	foundTimeout, foundUserDenied := false, false
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if strings.Contains(line, audit.StatusAskTimeout) {
			foundTimeout = true
		}
		if strings.Contains(line, audit.StatusUserDenied) {
			foundUserDenied = true
		}
	}
	if !foundTimeout {
		t.Error("ledger has no ask_timeout entry - StatusAskTimeout still unwired")
	}
	if foundUserDenied {
		t.Error("timeout was audited as a user denial - false attribution")
	}
}

// Timeouts must not extend the throttle window for a DIFFERENT key either
// (isolation guard for the exemption itself).
func TestAskGateTimeoutThenRealDenialStillCountsOnce(t *testing.T) {
	calls := 0
	a := new3370Agent(func(ctx context.Context, name, args string) permission.Decision {
		calls++
		if calls == 1 {
			return permission.Timeout // not counted
		}
		return permission.Deny
	})
	a.executeToolWithPermission(context.Background(), tc3370) // timeout
	a.executeToolWithPermission(context.Background(), tc3370) // real deny #1
	// One real denial alone must not suppress.
	if a.askThrottle.ShouldSuppress(tc3370.Name, tc3370.Arguments) {
		t.Fatal("single real denial suppressed asks - timeout inflated the count")
	}
}
