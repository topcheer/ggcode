package tui

import (
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/im"
)

// sidebarStatLines returns the non-blank lines of a sidebar section.
func sidebarStatLines(t *testing.T, out string) []string {
	t.Helper()
	var lines []string
	for _, l := range strings.Split(out, "\n") {
		if strings.TrimSpace(l) != "" {
			lines = append(lines, l)
		}
	}
	return lines
}

// The sidebar IM section is stats-only: a title and the adapter/healthy
// counts. Per-adapter detail lives in /im.
func TestSidebarIMSection_StatsOnly(t *testing.T) {
	cfg := &config.Config{IM: config.IMConfig{
		Enabled: true,
		Adapters: map[string]config.IMAdapterConfig{
			"qq-bot-1": {Platform: "qq", Enabled: true},
			"tg-bot-1": {Platform: "telegram", Enabled: true},
		},
	}}
	mgr := im.NewManager()
	mgr.BindSession(im.SessionBinding{SessionID: "s1", Workspace: "/ws"})
	for _, b := range []im.ChannelBinding{
		{Workspace: "/ws", Platform: im.PlatformQQ, Adapter: "qq-bot-1", ChannelID: "c1"},
		{Workspace: "/ws", Platform: im.PlatformTelegram, Adapter: "tg-bot-1", ChannelID: "c2"},
	} {
		if _, err := mgr.BindChannel(b); err != nil {
			t.Fatalf("BindChannel(%s): %v", b.Adapter, err)
		}
	}

	m := newTestModel()
	m.config, m.imManager = cfg, mgr

	out := m.renderSidebarIMSection()
	lines := sidebarStatLines(t, out)
	if len(lines) != 2 {
		t.Fatalf("stats-only IM section must be title + summary (2 lines), got %d:\n%s", len(lines), out)
	}
	if !strings.HasPrefix(lines[1], "2 adapters") {
		t.Fatalf("summary must count 2 adapters, got %q", lines[1])
	}
	for _, name := range []string{"qq-bot-1", "tg-bot-1"} {
		if strings.Contains(out, name) {
			t.Fatalf("adapter %q must not be listed in the stats-only IM section:\n%s", name, out)
		}
	}
}

// The sidebar MCP section is stats-only: a title and the up/pending/failed
// counts; active tools appear as a count row, never as a list. Per-server
// detail lives in /mcp.
func TestSidebarMCPSection_StatsOnly(t *testing.T) {
	m := newTestModel()
	m.mcpServers = []MCPInfo{
		{Name: "alpha", Connected: true, Transport: "stdio"},
		{Name: "beta", Connected: true, Transport: "sse"},
		{Name: "gamma", Pending: true},
		{Name: "delta"},
		{Name: "eps"},
		{Name: "zeta", Connected: true},
		{Name: "disabled-one", Disabled: true, Connected: true},
	}

	out := m.renderSidebarMCPSection()
	lines := sidebarStatLines(t, out)
	if len(lines) != 2 {
		t.Fatalf("stats-only MCP section must be title + summary (2 lines), got %d:\n%s", len(lines), out)
	}
	for _, want := range []string{"3 up", "1 pending", "2 failed"} {
		if !strings.Contains(lines[1], want) {
			t.Fatalf("summary %q must contain %q", lines[1], want)
		}
	}
	for _, name := range []string{"alpha", "beta", "gamma", "delta", "eps", "zeta", "disabled-one"} {
		if strings.Contains(out, name) {
			t.Fatalf("server %q must not be listed in the stats-only MCP section:\n%s", name, out)
		}
	}

	m.activeMCPTools = map[string]ToolStatusMsg{
		"t1": {ToolName: "search_everything"},
		"t2": {ToolName: "run_query"},
	}
	out = m.renderSidebarMCPSection()
	lines = sidebarStatLines(t, out)
	if len(lines) != 3 || !strings.HasPrefix(lines[2], "2 active tools") {
		t.Fatalf("active tools must be a single count row, got %d lines:\n%s", len(lines), out)
	}
	if strings.Contains(out, "search_everything") || strings.Contains(out, "run_query") {
		t.Fatalf("tool names must not be listed in the stats-only MCP section:\n%s", out)
	}
}

// Disabled servers never count toward the summary.
func TestSidebarMCPSection_StatsOnlyEmpty(t *testing.T) {
	m := newTestModel()
	m.mcpServers = []MCPInfo{{Name: "off", Disabled: true}}
	out := m.renderSidebarMCPSection()
	if strings.Contains(out, "0 up") {
		t.Fatalf("no active servers must render the none-placeholder, got:\n%s", out)
	}
}
