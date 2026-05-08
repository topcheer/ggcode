package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/im"
)

func TestIMPanelOpenClose(t *testing.T) {
	m := Model{}
	m.openIMPanel()
	if m.imPanel == nil {
		t.Fatal("imPanel should be set after openIMPanel")
	}
	m.closeIMPanel()
	if m.imPanel != nil {
		t.Fatal("imPanel should be nil after closeIMPanel")
	}
}

func TestIMPanelEscape(t *testing.T) {
	m := Model{}
	m.openIMPanel()
	updated, _ := m.handleIMPanelKey(tea.KeyPressMsg{Code: tea.KeyEsc})
	if updated.imPanel != nil {
		t.Fatal("imPanel should be nil after Esc")
	}
}

func TestIMPanelNavigateEmpty(t *testing.T) {
	m := Model{}
	m.openIMPanel()

	// Navigate with no entries should not crash
	updated, _ := m.handleIMPanelKey(tea.KeyPressMsg{Text: "j"})
	if updated.imPanel == nil {
		t.Fatal("imPanel should still be open")
	}
	updated, _ = m.handleIMPanelKey(tea.KeyPressMsg{Text: "k"})
	if updated.imPanel == nil {
		t.Fatal("imPanel should still be open")
	}
}

func TestIMPanelDisableNoRuntime(t *testing.T) {
	m := Model{}
	m.openIMPanel()
	// Without imManager, disable should show error message
	updated, _ := m.handleIMPanelKey(tea.KeyPressMsg{Text: "d"})
	if updated.imPanel == nil {
		t.Fatal("imPanel should still be open")
	}
	// message should be set (no channels available)
	if updated.imPanel.message == "" {
		t.Fatal("message should be set when no channels")
	}
}

func TestIMPanelEnableNoRuntime(t *testing.T) {
	m := Model{}
	m.openIMPanel()
	updated, _ := m.handleIMPanelKey(tea.KeyPressMsg{Text: "e"})
	if updated.imPanel == nil {
		t.Fatal("imPanel should still be open")
	}
	if updated.imPanel.message == "" {
		t.Fatal("message should be set when no channels")
	}
}

func TestIMPanelMuteNoRuntime(t *testing.T) {
	m := Model{}
	m.openIMPanel()
	updated, _ := m.handleIMPanelKey(tea.KeyPressMsg{Text: "m"})
	if updated.imPanel == nil {
		t.Fatal("imPanel should still be open")
	}
	if updated.imPanel.message == "" {
		t.Fatal("message should be set when no channels")
	}
}

func TestIMPanelUnmuteNoRuntime(t *testing.T) {
	m := Model{}
	m.openIMPanel()
	updated, _ := m.handleIMPanelKey(tea.KeyPressMsg{Text: "u"})
	if updated.imPanel == nil {
		t.Fatal("imPanel should still be open")
	}
	if updated.imPanel.message == "" {
		t.Fatal("message should be set when no channels")
	}
}

func TestIMPanelMuteAllNoRuntime(t *testing.T) {
	m := Model{}
	m.openIMPanel()
	updated, cmd := m.handleIMPanelKey(tea.KeyPressMsg{Text: "M"})
	if updated.imPanel == nil {
		t.Fatal("imPanel should still be open")
	}
	// MuteAll returns a command even without runtime
	if cmd == nil {
		t.Fatal("expected a command for MuteAll")
	}
}

func TestIMPanelUnmuteAllNoRuntime(t *testing.T) {
	m := Model{}
	m.openIMPanel()
	updated, cmd := m.handleIMPanelKey(tea.KeyPressMsg{Text: "U"})
	if updated.imPanel == nil {
		t.Fatal("imPanel should still be open")
	}
	if cmd == nil {
		t.Fatal("expected a command for UnmuteAll")
	}
}

func TestClampIMSelection(t *testing.T) {
	tests := []struct {
		selected, total, want int
	}{
		{0, 0, 0},
		{-1, 5, 0},
		{3, 3, 2},
		{5, 3, 2},
		{1, 3, 1},
	}
	for _, tt := range tests {
		got := clampIMSelection(tt.selected, tt.total)
		if got != tt.want {
			t.Errorf("clampIMSelection(%d, %d) = %d, want %d", tt.selected, tt.total, got, tt.want)
		}
	}
}

func TestFirstNonEmptyIM(t *testing.T) {
	if got := firstNonEmptyIM("", "  ", "hello"); got != "hello" {
		t.Errorf("firstNonEmptyIM = %q, want %q", got, "hello")
	}
	if got := firstNonEmptyIM(""); got != "" {
		t.Errorf("firstNonEmptyIM = %q, want empty", got)
	}
	if got := firstNonEmptyIM("first", "second"); got != "first" {
		t.Errorf("firstNonEmptyIM = %q, want %q", got, "first")
	}
}

func TestIMPanelOutputModeKeysReturnMessages(t *testing.T) {
	m := Model{}
	m.openIMPanel()
	// imEmitter is nil — setIMOutputMode should still return a result msg

	for _, key := range []string{"v", "q", "s"} {
		_, cmd := m.handleIMPanelKey(tea.KeyPressMsg{Text: key})
		if cmd == nil {
			t.Errorf("key %q: expected a cmd, got nil", key)
			continue
		}
		result := cmd()
		r, ok := result.(imPanelResultMsg)
		if !ok {
			t.Errorf("key %q: expected imPanelResultMsg, got %T", key, result)
		}
		if r.err != nil {
			t.Errorf("key %q: unexpected error: %v", key, r.err)
		}
	}
}

func TestIMPanelOutputModeDisplayNilEmitter(t *testing.T) {
	m := Model{}
	m.openIMPanel()

	panel := m.renderIMPanel()
	// Should show "verbose" as default when emitter is nil
	if !strings.Contains(panel, "verbose") {
		t.Errorf("expected 'verbose' in panel output, got:\n%s", panel)
	}
}

func TestIMPanelOutputModeDisplayContainsHint(t *testing.T) {
	m := Model{}
	m.openIMPanel()

	panel := m.renderIMPanel()
	// Should contain output mode hint (en or zh-CN depending on default lang)
	if !strings.Contains(panel, "v verbose") && !strings.Contains(panel, "v 详细") {
		t.Errorf("expected output mode hint in panel, got:\n%s", panel)
	}
}

func TestIMPanelEntries_ConfigDisabled(t *testing.T) {
	// Verify that imChannelEntries reads disabled state from config,
	// not just from runtime disabledBindings.
	cfg := &config.Config{
		IM: config.IMConfig{
			Enabled: true,
			Adapters: map[string]config.IMAdapterConfig{
				"qq-bot-1": {Platform: "qq", Enabled: false},
				"tg-bot-1": {Platform: "telegram", Enabled: true},
			},
		},
	}

	mgr := im.NewManager()
	mgr.BindSession(im.SessionBinding{
		SessionID: "test-session",
		Workspace: "/workspace/test",
	})
	_, _ = mgr.BindChannel(im.ChannelBinding{
		Workspace: "/workspace/test",
		Platform:  im.PlatformQQ,
		Adapter:   "qq-bot-1",
		ChannelID: "channel-1",
	})
	_, _ = mgr.BindChannel(im.ChannelBinding{
		Workspace: "/workspace/test",
		Platform:  im.PlatformTelegram,
		Adapter:   "tg-bot-1",
		ChannelID: "channel-2",
	})

	m := Model{config: cfg, imManager: mgr}

	entries := m.imChannelEntries()
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}

	for _, e := range entries {
		if e.Adapter == "qq-bot-1" {
			if !e.Disabled {
				t.Error("qq-bot-1 should be disabled (config enabled=false)")
			}
		}
		if e.Adapter == "tg-bot-1" {
			if e.Disabled {
				t.Error("tg-bot-1 should NOT be disabled (config enabled=true)")
			}
		}
	}
}

func TestIMPanelEntries_ConfigDisabledOverridesRuntime(t *testing.T) {
	// Verify that config disabled=true overrides runtime disabled=false.
	// This simulates the scenario where a second instance reads config
	// but runtime hasn't run ApplyAdapterConfig yet.
	cfg := &config.Config{
		IM: config.IMConfig{
			Enabled: true,
			Adapters: map[string]config.IMAdapterConfig{
				"qq-bot-1": {Platform: "qq", Enabled: false},
			},
		},
	}

	mgr := im.NewManager()
	mgr.BindSession(im.SessionBinding{
		SessionID: "test-session",
		Workspace: "/workspace/test",
	})
	_, _ = mgr.BindChannel(im.ChannelBinding{
		Workspace: "/workspace/test",
		Platform:  im.PlatformQQ,
		Adapter:   "qq-bot-1",
		ChannelID: "channel-1",
	})
	// NOT calling ApplyAdapterConfig — runtime still has qq-bot-1 active

	m := Model{config: cfg, imManager: mgr}

	entries := m.imChannelEntries()
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if !entries[0].Disabled {
		t.Error("qq-bot-1 should show disabled from config even when runtime has it active")
	}
	if entries[0].Muted {
		t.Error("qq-bot-1 should NOT be muted when disabled")
	}
}

func TestIMPanelEntries_MutedNotShownWhenDisabled(t *testing.T) {
	// When an adapter is disabled, muted should be false —
	// disabled takes precedence.
	cfg := &config.Config{
		IM: config.IMConfig{
			Enabled: true,
			Adapters: map[string]config.IMAdapterConfig{
				"qq-bot-1": {Platform: "qq", Enabled: false},
			},
		},
	}

	mgr := im.NewManager()
	mgr.BindSession(im.SessionBinding{
		SessionID: "test-session",
		Workspace: "/workspace/test",
	})
	_, _ = mgr.BindChannel(im.ChannelBinding{
		Workspace: "/workspace/test",
		Platform:  im.PlatformQQ,
		Adapter:   "qq-bot-1",
		ChannelID: "channel-1",
	})
	_ = mgr.MuteBinding("qq-bot-1") // mute in runtime

	m := Model{config: cfg, imManager: mgr}

	entries := m.imChannelEntries()
	for _, e := range entries {
		if e.Adapter == "qq-bot-1" {
			if !e.Disabled {
				t.Error("should be disabled")
			}
			if e.Muted {
				t.Error("should NOT show muted when disabled takes precedence")
			}
		}
	}
}
