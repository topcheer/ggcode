package tui

import (
	"strings"
	"testing"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/im"
)

// setupModelForPaste creates a minimal model ready to accept paste events.
func setupModelForPaste() Model {
	m := NewModel(nil, nil)
	m.inputReady = true
	m.loading = false
	return m
}

// --- QQ panel paste ---

func TestQQPanelCreateModePaste(t *testing.T) {
	m := setupModelForPaste()
	m.openQQPanel()
	m.qqPanel.createMode = true

	updated, _ := m.Update(tea.PasteMsg{Content: "bot-token-abc123"})
	m = updated.(Model)

	if !strings.Contains(m.qqPanel.createInput, "bot-token-abc123") {
		t.Fatalf("expected createInput to contain pasted text, got %q", m.qqPanel.createInput)
	}
}

func TestQQPanelCreateModePasteAppends(t *testing.T) {
	m := setupModelForPaste()
	m.openQQPanel()
	m.qqPanel.createMode = true
	m.qqPanel.createInput = "prefix-"

	updated, _ := m.Update(tea.PasteMsg{Content: "suffix"})
	m = updated.(Model)

	if m.qqPanel.createInput != "prefix-suffix" {
		t.Fatalf("expected 'prefix-suffix', got %q", m.qqPanel.createInput)
	}
}

func TestQQPanelNotCreateModePasteFallsToMainInput(t *testing.T) {
	m := setupModelForPaste()
	m.openQQPanel()
	// createMode is false (default)

	updated, _ := m.Update(tea.PasteMsg{Content: "should-go-to-main"})
	m = updated.(Model)

	if m.qqPanel.createInput != "" {
		t.Fatalf("expected empty createInput, got %q", m.qqPanel.createInput)
	}
	if !strings.Contains(m.input.Value(), "should-go-to-main") {
		t.Fatalf("expected paste to go to main input, got %q", m.input.Value())
	}
}

// --- TG panel paste ---

func TestTGPanelCreateModePaste(t *testing.T) {
	m := setupModelForPaste()
	m.openTGPanel()
	m.tgPanel.createMode = true

	updated, _ := m.Update(tea.PasteMsg{Content: "tg-bot-token"})
	m = updated.(Model)

	if !strings.Contains(m.tgPanel.createInput, "tg-bot-token") {
		t.Fatalf("expected createInput to contain pasted text, got %q", m.tgPanel.createInput)
	}
}

// --- Discord panel paste ---

func TestDiscordPanelCreateModePaste(t *testing.T) {
	m := setupModelForPaste()
	m.openDiscordPanel()
	m.discordPanel.createMode = true

	updated, _ := m.Update(tea.PasteMsg{Content: "discord-webhook-url"})
	m = updated.(Model)

	if !strings.Contains(m.discordPanel.createInput, "discord-webhook-url") {
		t.Fatalf("expected createInput to contain pasted text, got %q", m.discordPanel.createInput)
	}
}

// --- Slack panel paste ---

func TestSlackPanelCreateModePaste(t *testing.T) {
	m := setupModelForPaste()
	m.openSlackPanel()
	m.slackPanel.createMode = true

	updated, _ := m.Update(tea.PasteMsg{Content: "slack-bot-token"})
	m = updated.(Model)

	if !strings.Contains(m.slackPanel.createInput, "slack-bot-token") {
		t.Fatalf("expected createInput to contain pasted text, got %q", m.slackPanel.createInput)
	}
}

// --- Feishu panel paste ---

func TestFeishuPanelCreateModePaste(t *testing.T) {
	m := setupModelForPaste()
	m.openFeishuPanel()
	m.feishuPanel.createMode = true

	updated, _ := m.Update(tea.PasteMsg{Content: "feishu-app-secret"})
	m = updated.(Model)

	if !strings.Contains(m.feishuPanel.createInput, "feishu-app-secret") {
		t.Fatalf("expected createInput to contain pasted text, got %q", m.feishuPanel.createInput)
	}
}

// --- Dingtalk panel paste ---

func TestDingtalkPanelCreateModePaste(t *testing.T) {
	m := setupModelForPaste()
	m.openDingtalkPanel()
	m.dingtalkPanel.createMode = true

	updated, _ := m.Update(tea.PasteMsg{Content: "dingtalk-webhook"})
	m = updated.(Model)

	if !strings.Contains(m.dingtalkPanel.createInput, "dingtalk-webhook") {
		t.Fatalf("expected createInput to contain pasted text, got %q", m.dingtalkPanel.createInput)
	}
}

// --- Provider panel paste (existing path, regression test) ---

func TestProviderPanelEditInputPaste(t *testing.T) {
	m := setupModelForPaste()
	m.SetConfig(&config.Config{
		Vendor:   "test",
		Endpoint: "default",
		Vendors: map[string]config.VendorConfig{
			"test": {
				Endpoints: map[string]config.EndpointConfig{
					"default": {Protocol: "openai"},
				},
			},
		},
	})
	m.openProviderPanel()
	// Simulate entering edit mode (which creates a textinput)
	ti := textinput.New()
	ti.Focus()
	m.providerPanel.editInput = ti
	m.providerPanel.editingField = "api_key"

	updated, _ := m.Update(tea.PasteMsg{Content: "sk-test-key-123"})
	m = updated.(Model)

	if !strings.Contains(m.providerPanel.editInput.Value(), "sk-test-key-123") {
		t.Fatalf("expected editInput to contain pasted text, got %q", m.providerPanel.editInput.Value())
	}
}

// --- Harness panel paste (existing path, regression test) ---

func TestHarnessPanelActionInputPaste(t *testing.T) {
	m := setupModelForPaste()
	m.harnessPanel = &harnessPanelState{}
	ti := textinput.New()
	ti.Focus()
	m.harnessPanel.actionInput = ti

	updated, _ := m.Update(tea.PasteMsg{Content: "harness-task-description"})
	m = updated.(Model)

	if !strings.Contains(m.harnessPanel.actionInput.Value(), "harness-task-description") {
		t.Fatalf("expected actionInput to contain pasted text, got %q", m.harnessPanel.actionInput.Value())
	}
}

// --- Impersonate panel paste (existing path, regression test) ---

func TestImpersonatePanelVersionInputPaste(t *testing.T) {
	m := setupModelForPaste()
	m.openImpersonatePanel()
	ti := textinput.New()
	ti.Focus()
	m.impersonatePanel.versionInput = ti

	updated, _ := m.Update(tea.PasteMsg{Content: "2024-01-01"})
	m = updated.(Model)

	if !strings.Contains(m.impersonatePanel.versionInput.Value(), "2024-01-01") {
		t.Fatalf("expected versionInput to contain pasted text, got %q", m.impersonatePanel.versionInput.Value())
	}
}

// --- Main input paste (fallback) ---

func TestPasteFallsToMainInput(t *testing.T) {
	m := setupModelForPaste()
	// No panels open

	updated, _ := m.Update(tea.PasteMsg{Content: "hello world"})
	m = updated.(Model)

	if !strings.Contains(m.input.Value(), "hello world") {
		t.Fatalf("expected main input to contain pasted text, got %q", m.input.Value())
	}
}

// --- Loading state blocks paste ---

func TestPasteBlockedWhenLoading(t *testing.T) {
	m := setupModelForPaste()
	m.loading = true

	updated, _ := m.Update(tea.PasteMsg{Content: "should-be-ignored"})
	m = updated.(Model)

	if strings.Contains(m.input.Value(), "should-be-ignored") {
		t.Fatal("expected paste to be blocked during loading")
	}
}

// --- inputReady blocks paste ---

func TestPasteBlockedBeforeInputReady(t *testing.T) {
	m := NewModel(nil, nil)
	m.inputReady = false
	m.loading = false

	updated, _ := m.Update(tea.PasteMsg{Content: "should-be-ignored"})
	m = updated.(Model)

	if strings.Contains(m.input.Value(), "should-be-ignored") {
		t.Fatal("expected paste to be blocked before inputReady")
	}
}

// --- Priority: active panel takes paste over main input ---

func TestIMPanelPasteTakesPriorityOverMainInput(t *testing.T) {
	m := setupModelForPaste()
	m.openQQPanel()
	m.qqPanel.createMode = true

	updated, _ := m.Update(tea.PasteMsg{Content: "bot-token"})
	m = updated.(Model)

	// QQ panel should get the paste
	if !strings.Contains(m.qqPanel.createInput, "bot-token") {
		t.Fatal("expected QQ panel to receive paste")
	}
	// Main input should NOT get it
	if strings.Contains(m.input.Value(), "bot-token") {
		t.Fatal("expected main input to NOT receive paste when QQ panel is active")
	}
}

// ensure im import is used
var _ im.AdapterState
