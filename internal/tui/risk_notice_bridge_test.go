package tui

import (
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/agent"
)

func TestFormatRiskNoticeGate(t *testing.T) {
	n := agent.RiskNotice{
		Source: "irreversibility-gate",
		Tool:   "git_push",
		Tier:   3,
		Detail: "under-grounded high-impact action",
	}
	got := formatRiskNotice(n)
	for _, want := range []string{"[risk]", "irreversibility-gate", "git_push", "under-grounded"} {
		if !strings.Contains(got, want) {
			t.Fatalf("formatRiskNotice() = %q, want substring %q", got, want)
		}
	}
}

func TestFormatRiskNoticePolicy(t *testing.T) {
	n := agent.RiskNotice{
		Source: "permission-policy",
		Tool:   "edit_file",
		Mode:   "plan",
		Detail: "denied by permission policy",
	}
	got := formatRiskNotice(n)
	for _, want := range []string{"permission-policy", "edit_file", "mode plan", "denied by permission policy"} {
		if !strings.Contains(got, want) {
			t.Fatalf("formatRiskNotice() = %q, want substring %q", got, want)
		}
	}
}

func TestFormatRiskNoticeEmptySource(t *testing.T) {
	if got := formatRiskNotice(agent.RiskNotice{Detail: "orphan"}); got != "" {
		t.Fatalf("expected empty line for empty source, got %q", got)
	}
}
