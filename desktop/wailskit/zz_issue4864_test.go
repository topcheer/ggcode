package wailskit

import (
	"strings"
	"testing"
)

// #4864: GetIMOnboardGuide derives desktop guide content from the TUI
// registry (single source of truth) and cross-fills QR-auth flags from the
// desktop platform registry.
func TestIssue4864_OnboardGuideSingleSource(t *testing.T) {
	guide := GetIMOnboardGuide()
	if len(guide) < 12 {
		t.Fatalf("guide must cover the full TUI registry (12 platforms), got %d", len(guide))
	}
	// Content parity with the TUI registry: spot-check Telegram's
	// anti-confusion note and QQ's QR flow.
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
		t.Fatalf("telegram/qq entries missing: %+v", guide)
	}
	found := false
	for _, n := range tg.Notes {
		if strings.Contains(n, "NUMERIC") || strings.Contains(n, "numeric") {
			found = true
		}
	}
	if !found {
		t.Errorf("telegram guide must carry the numeric-ID anti-confusion note, notes: %v", tg.Notes)
	}
	if len(tg.Steps) == 0 {
		t.Errorf("telegram guide must have steps")
	}
	// QRAuth cross-fill from the desktop registry (wechat is QR-pairing).
	var wechat *IMOnboardGuideEntry
	for i := range guide {
		if guide[i].Key == "wechat" {
			wechat = &guide[i]
		}
	}
	if wechat == nil || !wechat.QRAuth {
		t.Errorf("wechat must cross-fill QRAuth from the desktop platform registry")
	}
}
