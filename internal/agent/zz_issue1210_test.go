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

func TestIssue1210_NonPlanModeGuidance(t *testing.T) {
	s := newPermDenyStreakState()
	s.record(permission.AutoMode, denyResult())
	s.record(permission.AutoMode, denyResult())
	g := s.record(permission.AutoMode, denyResult())
	if g == "" {
		t.Fatal("expected guidance at threshold in auto mode")
	}
	if !strings.Contains(g, `mode: "supervised"`) {
		t.Errorf("guidance missing mode (auto alias normalizes to supervised): %s", g)
	}
	if strings.Contains(g, "Plan mode is read-only") {
		t.Errorf("auto-mode guidance should not mention plan mode: %s", g)
	}
}

func TestIssue1210_SuccessResetsStreak(t *testing.T) {
	s := newPermDenyStreakState()
	s.record(permission.PlanMode, denyResult())
	s.record(permission.PlanMode, okResult())
	s.record(permission.PlanMode, denyResult())
	if g := s.record(permission.PlanMode, denyResult()); g != "" {
		t.Fatalf("streak interrupted by success should not fire, got: %s", g)
	}
}

func TestIssue1210_MaxFiresAndRefireGap(t *testing.T) {
	s := newPermDenyStreakState()
	var g string
	for i := 0; i < 3; i++ {
		g = s.record(permission.PlanMode, denyResult())
	}
	if g == "" {
		t.Fatal("expected first fire")
	}

	for i := 0; i < 4; i++ {
		if g = s.record(permission.PlanMode, denyResult()); g != "" {
			t.Fatalf("refire before gap should not happen: %s", g)
		}
	}

	if g = s.record(permission.PlanMode, denyResult()); g == "" {
		t.Fatal("expected refire at gap")
	}

	for i := 0; i < 10; i++ {
		if g = s.record(permission.PlanMode, denyResult()); g != "" {
			t.Fatalf("fired beyond max: %s", g)
		}
	}
}

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
		{IsError: false, Content: "Permission denied for tool \"x\""},
		{IsError: true, Content: "permission check error: boom"},
		{Content: "done"},
	}
	for _, r := range no {
		if isPermissionDeniedResult(r) {
			t.Errorf("isPermissionDeniedResult(%q) = true, want false", r.Content)
		}
	}
}
