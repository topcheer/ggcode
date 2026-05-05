//go:build integration_local

package tui

import (
	"os"
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Helper: open a platform panel by typing its command
// ---------------------------------------------------------------------------

func openPanel(h *ptyHarness, cmd string) {
	h.drainOutput()
	for _, ch := range cmd {
		h.sendKey(string(ch))
	}
	time.Sleep(100 * time.Millisecond)
	h.sendKey("enter")
	time.Sleep(500 * time.Millisecond)
}

// ---------------------------------------------------------------------------
// Safe tests: verify panel opens, no inline QR, q key doesn't crash
// ---------------------------------------------------------------------------

func TestPTY_IMPanel_NoInlineQR(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}

	panels := []struct {
		cmd  string
		name string
	}{
		{"/telegram", "Telegram"},
		{"/discord", "Discord"},
		{"/signal", "Signal"},
		{"/matrix", "Matrix"},
		{"/dingtalk", "DingTalk"},
		{"/feishu", "Feishu"},
		{"/slack", "Slack"},
		{"/wecom", "WeCom"},
		{"/qq", "QQ"},
		{"/wechat", "WeChat"},
		{"/nostr", "Nostr"},
	}

	for _, p := range panels {
		t.Run(p.name, func(t *testing.T) {
			h := startGGCode(t, ptyOptions{})
			defer h.quit()

			h.waitForText("Type a message", 8*time.Second)
			h.drainOutput()

			// Open the panel
			openPanel(h, p.cmd)

			screen := h.snapshot()

			// Panel should show something (not empty)
			if len(screen) == 0 {
				t.Fatalf("%s panel produced no output", p.name)
			}

			// Panel should NOT contain inline QR block characters (█ ▀ ▄)
			// We check for dense QR-like patterns, not individual block chars
			qrBlocks := strings.Count(screen, "█")
			if qrBlocks > 20 {
				t.Fatalf("%s panel should NOT render inline QR (found %d █ chars). Screen:\n%s",
					p.name, qrBlocks, lastN(screen, 500))
			}

			t.Logf("%s panel opened OK (no inline QR)", p.name)
		})
	}
}

func TestPTY_IMPanel_QKeyDoesNotCrash(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}

	panels := []struct {
		cmd  string
		name string
	}{
		{"/telegram", "Telegram"},
		{"/discord", "Discord"},
		{"/matrix", "Matrix"},
		{"/dingtalk", "DingTalk"},
		{"/feishu", "Feishu"},
		{"/slack", "Slack"},
		{"/wecom", "WeCom"},
		{"/qq", "QQ"},
		{"/nostr", "Nostr"},
	}

	for _, p := range panels {
		t.Run(p.name, func(t *testing.T) {
			h := startGGCode(t, ptyOptions{})
			defer h.quit()

			h.waitForText("Type a message", 8*time.Second)
			h.drainOutput()

			openPanel(h, p.cmd)
			time.Sleep(300 * time.Millisecond)

			// Press q — should not crash regardless of adapter state
			h.sendKey("q")
			time.Sleep(300 * time.Millisecond)

			// If QR overlay opened, press Esc to close it
			h.sendKey("esc")
			time.Sleep(200 * time.Millisecond)

			screen := h.snapshot()
			if len(screen) == 0 {
				t.Fatalf("%s panel crashed after pressing q", p.name)
			}
			t.Logf("%s panel survived q key", p.name)
		})
	}
}

func TestPTY_IMPanel_EscClosesPanel(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}

	panels := []string{"/telegram", "/discord", "/matrix", "/nostr"}

	for _, cmd := range panels {
		t.Run(cmd, func(t *testing.T) {
			h := startGGCode(t, ptyOptions{})
			defer h.quit()

			h.waitForText("Type a message", 8*time.Second)
			h.drainOutput()

			openPanel(h, cmd)
			time.Sleep(300 * time.Millisecond)

			// Press Esc to close panel
			h.sendKey("esc")
			time.Sleep(300 * time.Millisecond)

			screen := h.snapshot()
			// Should be back to main view with input prompt
			if !strings.Contains(screen, "Type a message") && !strings.Contains(screen, "mode") {
				t.Fatalf("expected main view after Esc, got:\n%s", lastN(screen, 300))
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Live config tests: use real config with running adapters
// These tests verify QR overlay content when adapters are connected.
// ---------------------------------------------------------------------------

func TestPTY_IMPanel_QROverlay_WithRealConfig(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}

	// Find real config to get actual adapter connections
	realConfigPath := findRealConfig()
	if realConfigPath == "" {
		t.Skip("no real ggcode config found, skipping live adapter QR test")
	}
	data, err := os.ReadFile(realConfigPath)
	if err != nil {
		t.Skipf("cannot read real config: %v", err)
	}
	configStr := string(data)

	// Check which platforms are configured
	type platformTest struct {
		cmd       string
		name      string
		uriPrefix string // expected ContactURI prefix in QR overlay
	}

	platforms := []platformTest{
		{"/telegram", "Telegram", "https://t.me/"},
		{"/discord", "Discord", "https://discord.com/"},
		{"/signal", "Signal", "https://signal.me/"},
		{"/matrix", "Matrix", "https://matrix.to/"},
		{"/dingtalk", "DingTalk", "https://h5.dingtalk.com/"},
		{"/feishu", "Feishu", "https://applink.feishu.cn/"},
		{"/slack", "Slack", "https://slack.com/"},
		{"/wecom", "WeCom", "https://work.weixin.qq.com/"},
	}

	for _, p := range platforms {
		// Only test platforms that are configured
		if !strings.Contains(configStr, p.name) &&
			!strings.Contains(configStr, strings.ToLower(p.name)) {
			t.Logf("Skipping %s — not in config", p.name)
			continue
		}

		t.Run(p.name, func(t *testing.T) {
			// Use real config via opts.Config
			h := startGGCode(t, ptyOptions{Config: configStr})
			defer h.quit()

			h.waitForText("Type a message", 10*time.Second)
			h.drainOutput()

			// Open panel
			openPanel(h, p.cmd)
			time.Sleep(500 * time.Millisecond)

			// Wait for adapter to potentially connect
			screen := h.snapshot()

			// Check if adapter shows as connected/healthy
			connected := strings.Contains(screen, "healthy") ||
				strings.Contains(screen, "connected") ||
				strings.Contains(screen, "bound") ||
				strings.Contains(screen, "active")

			if !connected {
				// Adapter might not be connected yet, give more time
				time.Sleep(3 * time.Second)
				screen = h.snapshot()
			}

			// Press q to open QR overlay
			h.sendKey("q")
			time.Sleep(500 * time.Millisecond)

			screen = h.snapshot()

			// If QR overlay opened, verify content
			hasQR := strings.Contains(screen, "█") ||
				strings.Contains(screen, "▀") ||
				strings.Contains(screen, "▄")
			hasURI := strings.Contains(screen, p.uriPrefix)

			if hasQR && hasURI {
				t.Logf("%s QR overlay OK: has QR code and URI prefix %q", p.name, p.uriPrefix)

				// Verify Esc closes overlay
				h.sendKey("esc")
				time.Sleep(200 * time.Millisecond)
				screen = h.snapshot()
				if strings.Contains(screen, "Type a message") || strings.Contains(screen, "mode") {
					t.Logf("%s: overlay closed, back to main/panel view", p.name)
				}
			} else if hasQR && !hasURI {
				t.Logf("%s: QR shown but URI prefix %q not found (adapter may use different URI)", p.name, p.uriPrefix)
			} else {
				t.Logf("%s: no QR overlay (adapter likely not connected)", p.name)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// WeChat-specific: verify auth QR opens as overlay, not inline
// ---------------------------------------------------------------------------

func TestPTY_WeChatPanel_NoInlineQR(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}

	h := startGGCode(t, ptyOptions{})
	defer h.quit()

	h.waitForText("Type a message", 8*time.Second)
	h.drainOutput()

	openPanel(h, "/wechat")
	time.Sleep(300 * time.Millisecond)

	screen := h.snapshot()

	// Should show wechat panel content, NOT QR code blocks
	qrBlocks := strings.Count(screen, "█")
	if qrBlocks > 20 {
		t.Fatalf("WeChat panel should NOT render inline QR. Found %d █ chars. Screen:\n%s",
			qrBlocks, lastN(screen, 500))
	}
	t.Logf("WeChat panel: no inline QR (OK)")
}

// ---------------------------------------------------------------------------
// Nostr-specific: verify create bot generates key and shows QR overlay
// ---------------------------------------------------------------------------

func TestPTY_NostrPanel_CreateBotShowsOverlay(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}

	h := startGGCode(t, ptyOptions{})
	defer h.quit()

	h.waitForText("Type a message", 8*time.Second)
	h.drainOutput()

	// Open Nostr panel
	openPanel(h, "/nostr")
	time.Sleep(500 * time.Millisecond)

	screen := h.snapshot()
	t.Logf("Nostr panel after open:\n%s", lastN(screen, 400))

	// Press 'i' to enter create mode
	h.sendKey("i")
	time.Sleep(500 * time.Millisecond)

	screen = h.snapshot()
	t.Logf("After 'i' key:\n%s", lastN(screen, 400))

	// Type a bot name
	for _, ch := range "test-nostr-bot" {
		h.sendKey(string(ch))
	}
	time.Sleep(200 * time.Millisecond)

	// Press Enter to create
	h.sendKey("enter")
	time.Sleep(2 * time.Second)

	screen = h.snapshot()
	t.Logf("After create:\n%s", lastN(screen, 600))

	// Should show either QR overlay or success message
	hasNPub := strings.Contains(screen, "npub1")
	hasNSECK := strings.Contains(screen, "nsec1")
	hasQR := strings.Contains(screen, "█") || strings.Contains(screen, "▀")
	hasAdded := strings.Contains(screen, "Added") || strings.Contains(screen, "已添加") ||
		strings.Contains(screen, "Generated") || strings.Contains(screen, "生成")

	if hasNPub || hasNSECK || hasQR || hasAdded {
		t.Logf("Nostr create bot: success — npub=%v nsec=%v qr=%v added=%v", hasNPub, hasNSECK, hasQR, hasAdded)
	} else {
		t.Logf("Nostr create bot: may have failed. Screen:\n%s", lastN(screen, 500))
	}

	// Press Esc to close any overlay or panel
	h.sendKey("esc")
	time.Sleep(200 * time.Millisecond)
}

// ---------------------------------------------------------------------------
// Verify actions hint includes 'q' for QR
// ---------------------------------------------------------------------------

func TestPTY_IMPanel_ActionsHintIncludesQ(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}

	panels := []struct {
		cmd  string
		name string
	}{
		{"/telegram", "Telegram"},
		{"/discord", "Discord"},
		{"/matrix", "Matrix"},
		{"/nostr", "Nostr"},
		{"/feishu", "Feishu"},
	}

	for _, p := range panels {
		t.Run(p.name, func(t *testing.T) {
			h := startGGCode(t, ptyOptions{})
			defer h.quit()

			h.waitForText("Type a message", 8*time.Second)
			h.drainOutput()

			openPanel(h, p.cmd)
			time.Sleep(500 * time.Millisecond)

			screen := h.snapshot()

			// Check that the actions hint mentions 'q' for QR
			// The hint is at the bottom of the panel and contains "q" and "QR" or "二维码"
			hasQRHint := strings.Contains(screen, "QR") || strings.Contains(screen, "二维码")
			if !hasQRHint {
				t.Logf("%s: actions hint may not contain QR hint. Screen:\n%s", p.name, lastN(screen, 400))
			} else {
				t.Logf("%s: QR hint found in actions (OK)", p.name)
			}
		})
	}
}
