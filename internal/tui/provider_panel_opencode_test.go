package tui

// Regression test: pressing `l` on the opencode vendor in the provider panel
// must immediately enter the busy state, surface the starting message, and
// return a non-nil login command (the device-flow kickoff). Reported as
// "pressing l does nothing" (#2592-era); this pins the key path so a silent
// regression fails here instead of in front of a user.

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/topcheer/ggcode/internal/auth"
	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/session"
)

func newOpenCodePanelTestModel(t *testing.T) Model {
	t.Helper()
	cfg := &config.Config{
		Vendor:   "opencode",
		Endpoint: "zen-openai",
		Model:    "mimo-v2.5-free",
		Vendors: map[string]config.VendorConfig{
			"opencode": {
				DisplayName: "OpenCode Zen",
				APIKey:      "${OPENCODE_API_KEY}",
				Endpoints: map[string]config.EndpointConfig{
					"zen-openai": {
						DisplayName:  "Zen (OpenAI)",
						Protocol:     "openai",
						BaseURL:      "https://opencode.ai/zen/v1",
						DefaultModel: "mimo-v2.5-free",
					},
				},
			},
		},
	}
	m := newTestModel()
	m.SetConfig(cfg)
	m.session = &session.Session{Vendor: "opencode", Endpoint: "zen-openai", Model: "mimo-v2.5-free"}
	m.setActiveRuntimeSelection("OpenCode Zen", "Zen (OpenAI)", "mimo-v2.5-free")
	m.openProviderPanel()
	m.providerPanel.vendorIndex = indexOf(m.providerPanel.vendorIDs, "opencode")
	if m.providerPanel.vendorIndex < 0 {
		t.Fatalf("opencode vendor missing from panel vendor list: %v", m.providerPanel.vendorIDs)
	}
	m.providerPanel.syncLists(m.configView())
	return m
}

func TestProviderPanelOpenCodeLoginKey(t *testing.T) {
	m := newOpenCodePanelTestModel(t)
	if got := m.providerPanel.selectedVendor(); got != "opencode" {
		t.Fatalf("selectedVendor = %q, want opencode", got)
	}

	next, cmd := m.handleProviderPanelKey(tea.KeyPressMsg{Text: "l"})
	m = next
	if m.providerPanel == nil {
		t.Fatal("panel closed by keypress")
	}
	if !m.providerPanel.authBusy {
		t.Fatal("authBusy not set: login key was a no-op")
	}
	if msg := m.providerPanel.message; !strings.Contains(msg, "OpenCode") {
		t.Fatalf("message = %q, want an OpenCode login starting notice", msg)
	}
	if cmd == nil {
		t.Fatal("no login command returned: device flow never starts")
	}
}

func TestProviderLoginBannerLifecycle(t *testing.T) {
	m := newOpenCodePanelTestModel(t)

	// Start msg wires the banner (device code + URL), regardless of footer focus.
	m.providerPanel.pendingLogin = nil
	startMsg := providerAuthStartMsg{vendor: auth.ProviderOpenCode, openCodeFlow: &auth.OpenCodeDeviceAuth{
		UserCode: "ABCD-EFGH", VerificationURIComplete: "/console/device?user_code=ABCD-EFGH",
	}}
	next, _ := m.handleProviderAuthStartMsg(startMsg)
	if next.providerPanel.pendingLogin == nil {
		t.Fatal("banner not wired by start msg")
	}
	if next.providerPanel.pendingLogin.Code != "ABCD-EFGH" {
		t.Fatalf("banner code = %q", next.providerPanel.pendingLogin.Code)
	}

	// Banner persists until the result arrives...
	next2, _ := next.handleProviderAuthResultMsg(providerAuthResultMsg{vendor: auth.ProviderOpenCode})
	if next2.providerPanel.pendingLogin != nil {
		t.Fatal("banner must clear once authorization state is fetched")
	}
	if next2.providerPanel.authBusy {
		t.Fatal("authBusy must clear on result")
	}

	// Copilot path gets the same banner (regression: code must be copyable).
	next3, _ := next2.handleProviderAuthStartMsg(providerAuthStartMsg{
		vendor: auth.ProviderGitHubCopilot,
		flow:   &auth.CopilotDeviceFlow{VerificationURI: "https://github.com/login/device", UserCode: "XY9Z-1234"},
	})
	if next3.providerPanel.pendingLogin == nil || next3.providerPanel.pendingLogin.Code != "XY9Z-1234" {
		t.Fatalf("copilot banner missing: %+v", next3.providerPanel.pendingLogin)
	}
}
