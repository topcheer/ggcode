package agent

import (
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/permission"
	"github.com/topcheer/ggcode/internal/tool"
)

func denyResult() tool.Result {
	return tool.Result{IsError: true, Content: "Permission denied for tool \"edit_file\". The operation was blocked by the permission policy (current mode: plan)."}
}

func okResult() tool.Result {
	return tool.Result{Content: "done"}
}

// TestIssue1210_PermDenyStreakFiresAtThreshold verifies guidance fires on the
// 3rd consecutive policy denial, names the current mode, and points at the
// plan-mode self-rescue path.
func TestIssue1210_PermDenyStreakFiresAtThreshold(t *testing.T) {
	s := newPermDenyStreakState()

	if g := s.record(permission.PlanMode, denyResult()); g != "" {
		t.Fatalf("streak 1 should not fire, got: %s", g)
	}
	if g := s.record(permission.PlanMode, denyResult()); g != "" {
		t.Fatalf("streak 2 should not fire, got: %s", g)
	}
	g := s.record(permission.PlanMode, denyResult())
	if g == "" {
		t.Fatal("streak 3 should fire mode-guard guidance")
	}
	for _, want := range []string{"3 consecutive", `mode: "plan"`, "switch_mode"} {
		if !strings.Contains(g, want) {
			t.Errorf("guidance missing %q: %s", want, g)
		}
	}
}

// TestIssue1210_NonPlanModeGuidance verifies the non-plan branch still names
// the mode but omits plan-specific advice.
func TestIssue1210_NonPlanModeGuidance(t *testing.T) {
	s := newPermDenyStreakState()
	s.record(permission.AutoMode, denyResult())
	s.record(permission.AutoMode, denyResult())
	g := s.record(permission.AutoMode, denyResult())
	if g == "" {
		t.Fatal("expected guidance at threshold in auto mode")
	}
	if !strings.Contains(g, `mode: "auto"`) {
		t.Errorf("guidance missing mode: %s", g)
	}
	if strings.Contains(g, "Plan mode is read-only") {
		t.Errorf("auto-mode guidance should not mention plan mode: %s", g)
	}
}

// TestIssue1210_SuccessResetsStreak verifies a successful result resets the
// streak, so scattered denials never accumulate to the threshold.
func TestIssue1210_SuccessResetsStreak(t *testing.T) {
	s := newPermDenyStreakState()
	s.record(permission.PlanMode, denyResult())
	s.record(permission.PlanMode, okResult())
	s.record(permission.PlanMode, denyResult())
	if g := s.record(permission.PlanMode, denyResult()); g != "" {
		t.Fatalf("streak interrupted by success should not fire, got: %s", g)
	}
}

// TestIssue1210_MaxFiresAndRefireGap verifies the per-run cap and refire gap.
func TestIssue1210_MaxFiresAndRefireGap(t *testing.T) {
	s := newPermDenyStreakState()
	var g string
	for i := 0; i < 3; i++ { // first fire at 3
		g = s.record(permission.PlanMode, denyResult())
	}
	if g == "" {
		t.Fatal("expected first fire")
	}
	// Streak 4..7 (below 3+5): no refire.
	for i := 0; i < 4; i++ {
		if g = s.record(permission.PlanMode, denyResult()); g != "" {
			t.Fatalf("refire before gap should not happen: %s", g)
		}
	}
	// Streak 8: second fire.
	if g = s.record(permission.PlanMode, denyResult()); g == "" {
		t.Fatal("expected refire at gap")
	}
	// Beyond cap: never again.
	for i := 0; i < 10; i++ {
		if g = s.record(permission.PlanMode, denyResult()); g != "" {
			t.Fatalf("fired beyond max: %s", g)
		}
	}
}

// TestIssue1210_IsPermissionDeniedResult covers the classifier, including the
// user-rejection and no-handler variants and non-permission errors.
func TestIssue1210_IsPermissionDeniedResult(t *testing.T) {
	yes := []tool.Result{
		{IsError: true, Content: "Permission denied for tool \"run_command\". The operation was blocked by the permission policy (current mode: plan)."},
		{IsError: true, Content: "Permission denied for tool \"edit_file\". User rejected the request."},
		{IsError: true, Content: "  Permission denied for tool \"write_file\". No approval handler available (running in non-interactive mode)."},
	}
	for _, r := range yes {
		if !isPermissionDeniedResult(r) {
			t.Errorf("isPermissionDeniedResult(%q) = false, want true", r.Content)
		}
	}
	no := []tool.Result{
		{IsError: true, Content: "exit status 1"},
		{IsError: false, Content: "Permission denied for tool \"x\""}, // not an error result
		{IsError: true, Content: "permission check error: boom"},      // different prefix
		{Content: "done"},
	}
	for _, r := range no {
		if isPermissionDeniedResult(r) {
			t.Errorf("isPermissionDeniedResult(%q) = true, want false", r.Content)
		}
	}
}
