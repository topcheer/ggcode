package tui

import (
	"strings"
	"testing"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/im"
	toolpkg "github.com/topcheer/ggcode/internal/tool"
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

func TestPasteAllowedWhenLoading(t *testing.T) {
	m := setupModelForPaste()
	m.loading = true

	updated, _ := m.Update(tea.PasteMsg{Content: "pasted-while-loading"})
	m = updated.(Model)

	if !strings.Contains(m.input.Value(), "pasted-while-loading") {
		t.Fatal("expected paste to be allowed during loading for interleaved messages")
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

// ============================================================
// Tests for paths originally fixed by Claude's paste commit
// ============================================================

// --- Provider panel modelFilter paste ---

func TestProviderPanelModelFilterPaste(t *testing.T) {
	m := setupModelForPaste()
	m.SetConfig(&config.Config{
		Vendor:   "test",
		Endpoint: "default",
		Vendors: map[string]config.VendorConfig{
			"test": {
				Endpoints: map[string]config.EndpointConfig{
					"default": {
						Protocol: "openai",
						Models:   []string{"gpt-4", "gpt-4o", "gpt-4o-mini"},
					},
				},
			},
		},
	})
	m.openProviderPanel()
	m.providerPanel.modelFilter.Focus()

	updated, _ := m.Update(tea.PasteMsg{Content: "gpt-4o"})
	m = updated.(Model)

	if !strings.Contains(m.providerPanel.modelFilter.Value(), "gpt-4o") {
		t.Fatalf("expected modelFilter to contain pasted text, got %q", m.providerPanel.modelFilter.Value())
	}
}

// --- Model panel filter paste ---

func TestModelPanelFilterPaste(t *testing.T) {
	m := setupModelForPaste()
	m.modelPanel = &modelPanelState{
		models:   []string{"gpt-4", "gpt-4o", "gpt-4o-mini"},
		selected: 0,
	}
	ti := textinput.New()
	ti.Focus()
	m.modelPanel.filter = ti

	updated, _ := m.Update(tea.PasteMsg{Content: "gpt-4o"})
	m = updated.(Model)

	if !strings.Contains(m.modelPanel.filter.Value(), "gpt-4o") {
		t.Fatalf("expected modelPanel filter to contain pasted text, got %q", m.modelPanel.filter.Value())
	}
}

// --- Impersonate panel header key input paste ---

func TestImpersonatePanelHeaderKeyInputPaste(t *testing.T) {
	m := setupModelForPaste()
	m.openImpersonatePanel()
	ti := textinput.New()
	ti.Focus()
	m.impersonatePanel.headerKeyInput = ti
	m.impersonatePanel.editingHeader = 0 // >= 0 means editing a header

	updated, _ := m.Update(tea.PasteMsg{Content: "Authorization"})
	m = updated.(Model)

	if !strings.Contains(m.impersonatePanel.headerKeyInput.Value(), "Authorization") {
		t.Fatalf("expected headerKeyInput to contain pasted text, got %q", m.impersonatePanel.headerKeyInput.Value())
	}
}

// --- Impersonate panel header value input paste ---

func TestImpersonatePanelHeaderValueInputPaste(t *testing.T) {
	m := setupModelForPaste()
	m.openImpersonatePanel()
	// headerKeyInput NOT focused, headerValueInput focused
	m.impersonatePanel.headerKeyInput = textinput.New()
	vi := textinput.New()
	vi.Focus()
	m.impersonatePanel.headerValueInput = vi
	m.impersonatePanel.editingHeader = 0

	updated, _ := m.Update(tea.PasteMsg{Content: "Bearer sk-xxx"})
	m = updated.(Model)

	if !strings.Contains(m.impersonatePanel.headerValueInput.Value(), "Bearer sk-xxx") {
		t.Fatalf("expected headerValueInput to contain pasted text, got %q", m.impersonatePanel.headerValueInput.Value())
	}
}

// --- Harness context prompt input paste ---

func TestHarnessContextPromptInputPaste(t *testing.T) {
	m := setupModelForPaste()
	ti := textinput.New()
	ti.Focus()
	m.harnessContextPrompt = &harnessContextPromptState{
		inputFocus: true,
		input:      ti,
	}

	updated, _ := m.Update(tea.PasteMsg{Content: "user context data"})
	m = updated.(Model)

	if !strings.Contains(m.harnessContextPrompt.input.Value(), "user context data") {
		t.Fatalf("expected harness context prompt input to contain pasted text, got %q", m.harnessContextPrompt.input.Value())
	}
}

// --- Questionnaire input paste ---

func TestQuestionnaireInputPaste(t *testing.T) {
	m := setupModelForPaste()
	ti := textinput.New()
	ti.Focus()
	m.pendingQuestionnaire = &questionnaireState{
		request: toolpkg.AskUserRequest{
			Questions: []toolpkg.AskUserQuestion{
				{ID: "q1", Title: "Test", Prompt: "Enter value", Kind: "text", AllowFreeform: true},
			},
		},
		input: ti,
	}
	// Must initialize answers slice to match questions length
	m.pendingQuestionnaire.answers = make([]questionnaireAnswerState, 1)
	m.pendingQuestionnaire.answers[0].selected = make(map[string]struct{})

	updated, _ := m.Update(tea.PasteMsg{Content: "answer-from-clipboard"})
	m = updated.(Model)

	if !strings.Contains(m.pendingQuestionnaire.input.Value(), "answer-from-clipboard") {
		t.Fatalf("expected questionnaire input to contain pasted text, got %q", m.pendingQuestionnaire.input.Value())
	}
}

// --- IM adapter edit mode paste ---

func TestQQPanelEditModePaste(t *testing.T) {
	m := setupModelForPaste()
	m.openQQPanel()
	m.qqPanel.editState = imAdapterEditState{
		mode:        imEditInput,
		adapterName: "test-qq",
		editField:   "token",
		editInput:   "old_",
	}

	updated, _ := m.Update(tea.PasteMsg{Content: "pasted-secret-value"})
	m = updated.(Model)

	if !strings.Contains(m.qqPanel.editState.editInput, "pasted-secret-value") {
		t.Fatalf("expected QQ edit input to contain pasted text, got %q", m.qqPanel.editState.editInput)
	}
	if strings.Contains(m.input.Value(), "pasted-secret-value") {
		t.Fatal("expected main input to NOT receive paste when QQ edit mode is active")
	}
}

func TestTGPanelEditModePaste(t *testing.T) {
	m := setupModelForPaste()
	m.openTGPanel()
	m.tgPanel.editState = imAdapterEditState{
		mode:        imEditInput,
		adapterName: "test-tg",
		editField:   "token",
		editInput:   "",
	}

	updated, _ := m.Update(tea.PasteMsg{Content: "123456:ABC"})
	m = updated.(Model)

	if !strings.Contains(m.tgPanel.editState.editInput, "123456:ABC") {
		t.Fatalf("expected TG edit input to contain pasted text, got %q", m.tgPanel.editState.editInput)
	}
	if strings.Contains(m.input.Value(), "123456:ABC") {
		t.Fatal("expected main input to NOT receive paste when TG edit mode is active")
	}
}

// --- PC panel create mode paste ---

func TestPCPanelCreateModePaste(t *testing.T) {
	m := setupModelForPaste()
	m.openPCPanel()
	m.pcPanel.createMode = true

	updated, _ := m.Update(tea.PasteMsg{Content: "session-label"})
	m = updated.(Model)

	if !strings.Contains(m.pcPanel.createInput, "session-label") {
		t.Fatalf("expected PC panel create input to contain pasted text, got %q", m.pcPanel.createInput)
	}
	if strings.Contains(m.input.Value(), "session-label") {
		t.Fatal("expected main input to NOT receive paste when PC panel create mode is active")
	}
}
