package wailskit

import (
	"strings"
	"testing"
)

// #4864: GetIMOnboardGuide derives desktop guide content from the merged
// #4862 TUI wizard registry (imOnboardWizard, single source of truth) and
// aggregates per-step Notes/URLs into the desktop entry shape.
func TestIssue4864_OnboardGuideSingleSource(t *testing.T) {
	guide := GetIMOnboardGuide()
	if len(guide) < 12 {
		t.Fatalf("guide must cover the full #4862 wizard registry (>=12 platforms), got %d", len(guide))
	}
	var tg, qq *IMOnboardGuideEntry
	for i := range guide {
		switch guide[i].Key {
		case "telegram":
			tg = &guide[i]
		case "qq":
			qq = &guide[i]
		}
	}
	if tg == nil || qq == nil {
		t.Fatalf("telegram/qq entries missing in guide of %d", len(guide))
	}
	// Parity guard: the Telegram token anti-confusion note from the merged
	// #4862 registry must flow through to the desktop entry.
	found := false
	for _, n := range tg.Notes {
		if strings.Contains(n, "NOT your Telegram @username") {
			found = true
		}
	}
	if !found {
		t.Errorf("telegram guide must carry the #4862 token anti-confusion note, notes: %v", tg.Notes)
	}
	if len(tg.Steps) == 0 {
		t.Errorf("telegram guide must have steps")
	}
	// QR flag flows from the #4862 registry's QR marking (qq is QR-pairing).
	if !qq.QRAuth {
		t.Errorf("qq must carry QRAuth from the #4862 registry QR flag")
	}
}
