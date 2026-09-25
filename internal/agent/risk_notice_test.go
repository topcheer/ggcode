package agent

import "testing"

func TestEmitRiskNoticeNilHandler(t *testing.T) {
	a := &Agent{}
	var got []RiskNotice
	a.SetRiskNoticeHandler(func(n RiskNotice) { got = append(got, n) })
	a.emitRiskNotice(RiskNotice{Source: "irreversibility-gate", Tool: "git_push", Tier: 3, Detail: "d"})
	if len(got) != 1 {
		t.Fatalf("expected 1 notice, got %d", len(got))
	}
	// Clearing the handler must be honored: no further delivery, no panic.
	a.SetRiskNoticeHandler(nil)
	a.emitRiskNotice(RiskNotice{Source: "permission-policy", Detail: "denied"})
	if len(got) != 1 {
		t.Fatalf("nil handler must not deliver, got %d notices", len(got))
	}
}

func TestSetRiskNoticeHandler(t *testing.T) {
	a := &Agent{}
	var got []RiskNotice
	a.SetRiskNoticeHandler(func(n RiskNotice) { got = append(got, n) })
	a.emitRiskNotice(RiskNotice{Source: "permission-policy", Tool: "edit_file", Mode: "auto", Detail: "denied"})
	if len(got) != 1 {
		t.Fatalf("expected 1 notice, got %d", len(got))
	}
	if got[0].Source != "permission-policy" || got[0].Tool != "edit_file" || got[0].Mode != "auto" {
		t.Fatalf("unexpected notice: %+v", got[0])
	}
}

func TestRiskNoticeTierName(t *testing.T) {
	cases := map[int]string{
		irrevTierNone:   "none",
		irrevTierLow:    "low",
		irrevTierMedium: "medium",
		irrevTierHigh:   "high",
	}
	for tier, want := range cases {
		n := RiskNotice{Tier: tier}
		if got := n.TierName(); got != want {
			t.Fatalf("tier %d: got %q, want %q", tier, got, want)
		}
	}
}
