package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/topcheer/ggcode/internal/config"
)

func TestBuildModelListWindowCapsVisibleRows(t *testing.T) {
	models := []string{
		"model-01", "model-02", "model-03", "model-04", "model-05", "model-06",
		"model-07", "model-08", "model-09", "model-10", "model-11", "model-12",
	}

	window := buildModelListWindow(models, 11, newModelFilterInput(LangEnglish))
	if len(window.items) != maxVisibleModelRows {
		t.Fatalf("expected %d visible rows, got %d", maxVisibleModelRows, len(window.items))
	}
	if window.hiddenBefore != 2 || window.hiddenAfter != 0 {
		t.Fatalf("expected hidden counts 2/0, got %d/%d", window.hiddenBefore, window.hiddenAfter)
	}
	if window.items[0] != "model-03" || window.items[len(window.items)-1] != "model-12" {
		t.Fatalf("unexpected window items: %#v", window.items)
	}
	if window.selected != len(window.items)-1 {
		t.Fatalf("expected selected row at bottom of window, got %d", window.selected)
	}
}

func TestBuildModelListWindowFiltersModels(t *testing.T) {
	models := []string{"gpt-4o", "gpt-4o-mini", "claude-sonnet", "gemini-flash"}
	filter := newModelFilterInput(LangEnglish)
	filter.SetValue("4o-mini")

	window := buildModelListWindow(models, 0, filter)
	if window.filteredCount != 1 {
		t.Fatalf("expected one filtered result, got %d", window.filteredCount)
	}
	if len(window.items) != 1 || window.items[0] != "gpt-4o-mini" {
		t.Fatalf("unexpected filtered items: %#v", window.items)
	}
}

func TestProviderPanelIgnoresPlaceholderAPIKeyForRefresh(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Vendors = map[string]config.VendorConfig{
		"openai": {
			DisplayName: "OpenAI",
			APIKey:      "${OPENAI_API_KEY}",
			Endpoints: map[string]config.EndpointConfig{
				"api": {
					DisplayName:   "API",
					Protocol:      "openai",
					BaseURL:       "https://api.openai.com/v1",
					DefaultModel:  "gpt-4o-mini",
					SelectedModel: "gpt-4o-mini",
					Models:        []string{"gpt-4o-mini"},
				},
			},
		},
	}
	cfg.Vendor = "openai"
	cfg.Endpoint = "api"
	cfg.Model = "gpt-4o-mini"

	m := newTestModel()
	m.SetConfig(cfg)
	m.openProviderPanel()

	if cmd := m.refreshProviderModelsForVendor("openai"); cmd != nil {
		t.Fatal("expected placeholder api key to skip remote refresh")
	}
	rendered := m.renderProviderPanel()
	if strings.Contains(rendered, "configured") {
		t.Fatalf("expected placeholder api key to render as missing, got %q", rendered)
	}
}

func TestProviderPanelUsesResolvedPlaceholderAPIKeyForRefresh(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "test-key")

	cfg := config.DefaultConfig()
	cfg.Vendors = map[string]config.VendorConfig{
		"openai": {
			DisplayName: "OpenAI",
			APIKey:      "${OPENAI_API_KEY}",
			Endpoints: map[string]config.EndpointConfig{
				"api": {
					DisplayName:   "API",
					Protocol:      "openai",
					BaseURL:       "https://api.openai.com/v1",
					DefaultModel:  "gpt-4o-mini",
					SelectedModel: "gpt-4o-mini",
					Models:        []string{"gpt-4o-mini"},
				},
			},
		},
	}
	cfg.Vendor = "openai"
	cfg.Endpoint = "api"
	cfg.Model = "gpt-4o-mini"

	m := newTestModel()
	m.SetConfig(cfg)
	m.openProviderPanel()

	if cmd := m.refreshProviderModelsForVendor("openai"); cmd == nil {
		t.Fatal("expected resolved placeholder api key to allow remote refresh")
	}
	rendered := m.renderProviderPanel()
	if !strings.Contains(rendered, "configured") {
		t.Fatalf("expected resolved placeholder api key to render as configured, got %q", rendered)
	}
}

func TestProviderPanelRendersChineseStrings(t *testing.T) {
	m := newTestModel()
	m.SetConfig(config.DefaultConfig())
	m.setLanguage(string(LangZhCN))
	m.openProviderPanel()

	rendered := m.renderProviderPanel()
	for _, want := range []string{"供应商", "端点", "模型", "当前草稿"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expected provider panel to contain %q, got %q", want, rendered)
		}
	}
}

func TestModelPanelRendersChineseStrings(t *testing.T) {
	m := newTestModel()
	m.SetConfig(config.DefaultConfig())
	m.setLanguage(string(LangZhCN))
	cmd := m.openModelPanel()
	if cmd == nil {
		t.Fatal("expected openModelPanel to return refresh command")
	}

	rendered := m.renderModelPanel()
	for _, want := range []string{"模型", "供应商", "端点", "来源"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expected model panel to contain %q, got %q", want, rendered)
		}
	}
}

func TestModelPanelFilterAcceptsShortcutRunes(t *testing.T) {
	m := newTestModel()
	m.SetConfig(config.DefaultConfig())
	m.modelPanel = &modelPanelState{
		models:   []string{"alpha", "beta", "gamma"},
		selected: 0,
		filter:   newModelFilterInput(LangEnglish),
	}
	m.modelPanel.filter.Focus()

	next, cmd := m.handleModelPanelKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	m = next
	next, cmd = m.handleModelPanelKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	m = next
	_ = cmd

	if got := m.modelPanel.filter.Value(); got != "rs" {
		t.Fatalf("expected model filter to accept shortcut runes, got %q", got)
	}
}

func TestProviderPanelFilterAcceptsShortcutRunes(t *testing.T) {
	m := newTestModel()
	m.SetConfig(config.DefaultConfig())
	m.openProviderPanel()
	m.providerPanel.focus = providerPanelFocusModel
	m.providerPanel.models = []string{"alpha", "beta", "gamma"}
	m.providerPanel.modelIndex = 0
	m.providerPanel.modelFilter.Focus()

	next, cmd := m.handleProviderPanelKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	m = next
	next, cmd = m.handleProviderPanelKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	m = next
	_ = cmd

	if got := m.providerPanel.modelFilter.Value(); got != "sr" {
		t.Fatalf("expected provider filter to accept shortcut runes, got %q", got)
	}
}
