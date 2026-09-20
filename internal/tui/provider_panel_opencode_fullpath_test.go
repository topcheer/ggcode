package tui

// Full-path regression test for the "pressing l on the opencode vendor does
// nothing" report: the key event must survive the REAL top-level Update
// dispatch (global keys -> panel routing), not just the inner panel handler,
// and the login command it returns must be non-nil. Uses the same panel
// entry as the /provider command (openProviderPanel) and the zen-openai
// endpoint exactly as reported.

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/session"
)

func TestProviderPanelOpenCodeLoginKeyFullPath(t *testing.T) {
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

	// Same entry the /provider command uses.
	m.openProviderPanel()
	if m.providerPanel == nil {
		t.Fatal("provider panel not opened")
	}
	m.providerPanel.vendorIndex = indexOf(m.providerPanel.vendorIDs, "opencode")
	if m.providerPanel.vendorIndex < 0 {
		t.Fatalf("opencode vendor missing: %v", m.providerPanel.vendorIDs)
	}
	m.providerPanel.syncLists(m.configView())
	m.providerPanel.endpointIndex = indexOf(m.providerPanel.endpointIDs, "zen-openai")
	if got := m.providerPanel.selectedVendor(); got != "opencode" {
		t.Fatalf("selectedVendor = %q, want opencode", got)
	}

	// Drive the REAL top-level Update, exactly like a terminal keypress.
	nextModel, cmd := m.Update(tea.KeyPressMsg{Text: "l"})
	next, ok := nextModel.(Model)
	if !ok {
		t.Fatalf("Update returned %T, want Model", nextModel)
	}
	if next.providerPanel == nil {
		t.Fatal("panel closed by keypress")
	}
	if !next.providerPanel.authBusy {
		t.Fatalf("authBusy not set - key was swallowed by the dispatch chain; message=%q", next.providerPanel.message)
	}
	if msg := next.providerPanel.message; !strings.Contains(msg, "OpenCode") {
		t.Fatalf("message = %q, want the OpenCode login starting notice", msg)
	}
	if cmd == nil {
		t.Fatal("Update returned nil cmd - device flow would never start")
	}
}
