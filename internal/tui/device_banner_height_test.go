package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/topcheer/ggcode/internal/util"
)

func TestDeviceCodeBannerNaturalHeight(t *testing.T) {
	m := newTestModel()

	m.width = 160
	m.height = 50

	m.pendingDeviceCodes = []deviceCodeInfo{
		{serverName: "cloudflare", verifyURL: "https://mcp.cloudflare.com/authorize?client_id=x"},
	}

	banner := m.renderDeviceCodeBanner()
	if banner == "" {
		t.Fatal("banner is empty")
	}
	if !strings.Contains(banner, "MCP Device Authorization") || !strings.Contains(banner, "cloudflare") {
		t.Fatal("banner missing title/server line")
	}

	bannerH := lipgloss.Height(banner)
	fullH := m.fullPanelHeight()
	if bannerH >= fullH {
		t.Fatalf("banner height %d >= full panel height %d: banner is filling the main content area (composer pushed off-screen)", bannerH, fullH)
	}

	if bannerH > 8 {
		t.Fatalf("banner height %d > 8: expected a compact strip", bannerH)
	}
}

func TestDeviceCodeBannerUserCodeShape(t *testing.T) {
	m := newTestModel()
	m.width = 120
	m.height = 40
	m.pendingDeviceCodes = []deviceCodeInfo{
		{serverName: "linear", userCode: "ABCD-EFGH", verifyURL: "https://linear.app/dev"},
	}
	banner := m.renderDeviceCodeBanner()
	if !strings.Contains(banner, "A   B   C   D") || !strings.Contains(banner, "https://linear.app/dev") {
		t.Fatalf("banner missing spaced user code:\n%s", banner)
	}
	if h := lipgloss.Height(banner); h > 8 {
		t.Fatalf("banner height %d > 8: device-code variant must also be a compact strip", h)
	}
	_ = tea.KeyPressMsg{}
	_ = util.FirstNonEmpty
}
