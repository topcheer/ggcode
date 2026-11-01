package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

// #4861 (reframed): wizard renderer, five-state table classification,
// onboarding run-record lifecycle (resume + config-write invalidation),
// and the probe/first-use tail. The pairing flows themselves are
// UNTOUCHED (range guard) - the wizard only renders guidance and calls
// existing surfaces.

func TestIssue4861_WizardPlatformsAndSteps(t *testing.T) {
	wiz := imOnboardWizard()
	if len(wiz) < 10 {
		t.Fatalf("expected the full platform registry (>=10), got %d", len(wiz))
	}
	for _, platform := range []string{"qq", "telegram", "slack", "feishu", "whatsapp"} {
		entry, ok := wiz[platform]
		if !ok {
			t.Fatalf("%s missing from the wizard", platform)
		}
		if len(entry.Steps) < 2 {
			t.Fatalf("%s: need numbered steps, got %d", platform, len(entry.Steps))
		}
		if entry.Panel == "" {
			t.Fatalf("%s: must point at the existing config panel seam", platform)
		}
	}
	// Anti-confusion notes on the token-trap platforms.
	for _, platform := range []string{"qq", "telegram", "discord", "feishu"} {
		hasNote := false
		for _, s := range wiz[platform].Steps {
			if s.Note != "" {
				hasNote = true
			}
		}
		if !hasNote {
			t.Fatalf("%s: at least one anti-confusion NOTE required", platform)
		}
	}
}

func TestIssue4861_RenderOnboardStepsAndProbeTail(t *testing.T) {
	out, ok := renderIMOnboardSteps("telegram")
	if !ok {
		t.Fatal("telegram steps must render")
	}
	for _, want := range []string{"1.", "2.", "BotFather", "NOTE:", "masked input"} {
		if !strings.Contains(out, want) {
			t.Fatalf("steps render missing %q:\n%s", want, out)
		}
	}
	if _, ok := renderIMOnboardSteps("no-such-platform"); ok {
		t.Fatal("unknown platform must not render")
	}
	// Checklist 5: URL deep-links render as copyable lines; QR platforms
	// carry the large-QR + URL-fallback guidance; step URLs collectable.
	tgURLs := imOnboardStepURLs("telegram")
	if len(tgURLs) == 0 || !strings.Contains(tgURLs[0], "t.me/BotFather") {
		t.Fatalf("telegram step URLs = %v", tgURLs)
	}
	if !strings.Contains(out, "Open (copyable): https://t.me/BotFather") {
		t.Fatalf("telegram render missing copyable URL line:\n%s", out)
	}
	qqOut, _ := renderIMOnboardSteps("qq")
	if !strings.Contains(qqOut, "[QR pairing]") || !strings.Contains(qqOut, "URL fallback") {
		t.Fatalf("qq (QR platform) missing large-QR + fallback guidance:\n%s", qqOut)
	}
	// Post range-correction: the guidance is served to the vendor panels via
	// the seam, and the /im cockpit is a ROUTER (no parallel wizard).
	if got := imVendorOnboardGuidance("dingtalk"); !strings.Contains(got, "1.") {
		t.Fatalf("vendor seam must serve the numbered steps, got:\n%s", got)
	}
	if imVendorOnboardGuidance("no-such") != "" {
		t.Fatal("vendor seam: unknown platform renders empty")
	}
	cockpit := renderIMOnboardCockpit(t.TempDir()) // no record: plain routing table
	if !strings.Contains(cockpit, "IM Onboarding Cockpit") || !strings.Contains(cockpit, "run /qq") {
		t.Fatalf("cockpit must route to the vendor panels:\n%s", cockpit)
	}
	// Checklist 3+4: setup-time probe + first-use bridge in the tail.
	for _, want := range []string{"verification + first use", "/im status", "welcome self-test"} {
		if !strings.Contains(imOnboardProbeTail, want) {
			t.Fatalf("probe tail missing %q", want)
		}
	}
}

func TestIssue4861_RecordLifecycleResumeAndInvalidation(t *testing.T) {
	dir := t.TempDir()
	if rec := loadIMOnboardRecord(dir); rec != nil {
		t.Fatal("no record initially")
	}
	if err := saveIMOnboardRecord(dir, imOnboardRecord{Platform: "qq", Step: 2, UpdatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	rec := loadIMOnboardRecord(dir)
	if rec == nil || rec.Platform != "qq" || rec.Step != 2 {
		t.Fatalf("record roundtrip = %+v", rec)
	}
	// Record newer than config -> mid-flow, NOT due.
	cfgPath := filepath.Join(dir, "ggcode.yaml")
	if err := os.WriteFile(cfgPath, []byte("im:\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	os.Chtimes(cfgPath, time.Now().Add(-time.Hour), time.Now().Add(-time.Hour))
	if imOnboardingDue(dir, cfgPath) {
		t.Fatal("record newer than config must not be due")
	}
	// Config edited after the record -> stale, due anew.
	os.Chtimes(cfgPath, time.Now(), time.Now())
	if !imOnboardingDue(dir, cfgPath) {
		t.Fatal("config write after record must invalidate (due)")
	}
	clearIMOnboardRecord(dir)
	if loadIMOnboardRecord(dir) != nil || imOnboardingDue(dir, cfgPath) {
		t.Fatal("cleared record: nothing due")
	}
}

func TestIssue4861_FullStateTableFiveStates(t *testing.T) {
	states := []IMAdapterStateInfo{
		{Adapter: "a1", Platform: "qq", Healthy: true},
		{Adapter: "a2", Platform: "telegram", Healthy: false, LastError: "connection refused"},
		{Adapter: "a3", Platform: "slack", Healthy: false, LastError: "invalid auth token 401"},
		{Adapter: "a4", Platform: "feishu", Status: "muted"},
	}
	configured := map[string]string{"qq": "a1", "telegram": "a2", "slack": "a3", "feishu": "a4"}
	out := renderIMFullStatusTable(states, configured)
	for _, want := range []string{
		"connected", "failed", "expired", "muted", "never-configured",
		"invalid auth token", // last error surfaced
		"discord",            // unconfigured platform listed
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("full-state table missing %q:\n%s", want, out)
		}
	}
	// Classification specifics.
	if got := classifyIMAdapterState(states[0], true); got != "connected" {
		t.Fatalf("healthy = %q, want connected", got)
	}
	if got := classifyIMAdapterState(states[1], true); got != "failed" {
		t.Fatalf("conn refused = %q, want failed", got)
	}
	if got := classifyIMAdapterState(states[2], true); got != "expired" {
		t.Fatalf("auth 401 = %q, want expired", got)
	}
	if got := classifyIMAdapterState(states[3], true); got != "muted" {
		t.Fatalf("explicit muted = %q, want muted", got)
	}
}

func TestIssue4861_StatusRegistryNeverConfigured(t *testing.T) {
	out := renderIMFullStatusTable(nil, map[string]string{})
	if !strings.Contains(out, "never-configured") {
		t.Fatalf("empty runtime still lists platforms as never-configured:\n%s", out)
	}
}

// Post final-range-correction: cockpit lives IN the /im panel - 'o' routes
// the selected channel to its vendor panel, 'x' clears the record, and no
// /im onboard|status subcommands exist (commands.go case removed).
func TestIssue4861_PanelKeysORoutesAndXClears(t *testing.T) {
	m := Model{}
	m.openIMPanel()
	// No entries: 'o' must not crash or close the panel.
	updated, _ := m.handleIMPanelKey(tea.KeyPressMsg{Text: "o"})
	if updated.imPanel == nil {
		t.Fatal("'o' with no entries keeps the panel open")
	}
	// 'x' is a TWO-STEP clear (#4862 review follow-up): first press arms
	// (cross-session progress must survive a stray keypress), second
	// consecutive press clears.
	updated, _ = m.handleIMPanelKey(tea.KeyPressMsg{Text: "x"})
	if updated.imPanel == nil {
		t.Fatal("'x' keeps the panel open")
	}
	if !strings.Contains(updated.imPanel.message, "press 'x' again") {
		t.Fatalf("first 'x' must arm, got %q", updated.imPanel.message)
	}
	updated, _ = updated.handleIMPanelKey(tea.KeyPressMsg{Text: "x"})
	if !strings.Contains(updated.imPanel.message, "cleared") {
		t.Fatalf("second 'x' must clear, got %q", updated.imPanel.message)
	}
}
