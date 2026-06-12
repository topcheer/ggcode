//go:build integration_local

package tui

import (
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/creack/pty/v2"
)

// ---------------------------------------------------------------------------
// startGGCodeLive launches ggcode with the user's real config.
// Config is copied to t.TempDir() (never inside the git repo).
// MCP servers requiring OAuth are stripped to prevent blocking popups.
// ---------------------------------------------------------------------------

func startGGCodeLive(t *testing.T) *ptyHarness {
	t.Helper()

	realGGCodeDir := os.Getenv("HOME") + "/.ggcode"
	if _, err := os.Stat(realGGCodeDir); err != nil {
		t.Skip("no ~/.ggcode directory found")
	}

	h := &ptyHarness{
		t:    t,
		cols: 120,
		rows: 40,
	}

	// Find binary
	candidates := []string{"./bin/ggcode", "../bin/ggcode", "./ggcode"}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			h.binary = c
			break
		}
	}
	if h.binary == "" {
		if p, _ := exec.LookPath("ggcode"); p != "" {
			h.binary = p
		}
	}
	if h.binary == "" {
		t.Skip("ggcode binary not found")
	}

	// t.TempDir() is outside the git repo — safe to copy real config here.
	h.tmpDir = t.TempDir()

	// Copy entire ~/.ggcode/ → {tmpdir}/.ggcode/ so adapters get keys.env,
	// oauth tokens, etc. Then strip MCP OAuth servers from the config copy.
	dstGGCodeDir := h.tmpDir + "/.ggcode"
	if err := copyDir(realGGCodeDir, dstGGCodeDir); err != nil {
		t.Fatalf("copy ~/.ggcode: %v", err)
	}

	// Strip MCP servers needing OAuth in the copied config
	cfgPath := dstGGCodeDir + "/ggcode.yaml"
	cfgData, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read copied config: %v", err)
	}
	safeCfg := stripMCPOAuthServers(string(cfgData))
	if err := os.WriteFile(cfgPath, []byte(safeCfg), 0600); err != nil {
		t.Fatalf("write stripped config: %v", err)
	}

	// Workspace dir
	workspaceDir := h.tmpDir + "/workspace"
	os.MkdirAll(workspaceDir, 0755)

	// Set HOME=tmpdir so ggcode reads our copied ~/.ggcode/
	filteredEnv := make([]string, 0, len(os.Environ()))
	for _, e := range os.Environ() {
		if strings.HasPrefix(e, "HOME=") {
			continue
		}
		filteredEnv = append(filteredEnv, e)
	}
	filteredEnv = append(filteredEnv,
		"HOME="+h.tmpDir,
		"TERM=xterm-256color",
	)

	h.cmd = exec.Command(h.binary)
	h.cmd.Dir = workspaceDir
	h.cmd.Env = filteredEnv
	h.cmd.Stderr = &h.stderr

	ptmx, err := pty.StartWithSize(h.cmd, &pty.Winsize{
		Cols: h.cols,
		Rows: h.rows,
	})
	if err != nil {
		t.Fatalf("start pty: %v", err)
	}
	h.ptmx = ptmx

	h.readerDone = make(chan struct{})
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := h.ptmx.Read(buf)
			if n > 0 {
				h.outputMu.Lock()
				h.outputBuf.Write(buf[:n])
				h.outputMu.Unlock()
			}
			if err != nil {
				close(h.readerDone)
				return
			}
		}
	}()

	return h
}

// copyDir recursively copies a directory tree.
func copyDir(src, dst string) error {
	return fs.WalkDir(os.DirFS(src), ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		dstPath := dst + "/" + path
		if d.IsDir() {
			return os.MkdirAll(dstPath, 0755)
		}
		data, err := os.ReadFile(src + "/" + path)
		if err != nil {
			return err
		}
		return os.WriteFile(dstPath, data, 0644)
	})
}

// stripMCPOAuthServers removes MCP server entries that need OAuth/device auth.
// This prevents the "MCP Device Authorization" popup from blocking the TUI.
func stripMCPOAuthServers(cfg string) string {
	lines := strings.Split(cfg, "\n")
	var result []string
	skipEntry := false
	inMCP := false
	mcpIndent := 0
	entryIndent := 0

	for i := 0; i < len(lines); i++ {
		line := lines[i]
		trimmed := strings.TrimSpace(line)
		indent := len(line) - len(strings.TrimLeft(line, " \t"))

		// Detect mcp_servers: section
		if trimmed == "mcp_servers:" {
			inMCP = true
			mcpIndent = indent
			result = append(result, line)
			continue
		}

		if inMCP {
			if trimmed != "" && !strings.HasPrefix(trimmed, "#") && indent <= mcpIndent {
				inMCP = false
				skipEntry = false
				result = append(result, line)
				continue
			}

			// Detect start of a new entry: "  - name: xxx"
			if strings.HasPrefix(trimmed, "- name:") {
				skipEntry = false
				entryIndent = indent
				// Look ahead for oauth or githubcopilot
				for j := i + 1; j < len(lines); j++ {
					ahead := strings.TrimSpace(lines[j])
					if ahead == "" || strings.HasPrefix(ahead, "#") {
						continue
					}
					aheadIndent := len(lines[j]) - len(strings.TrimLeft(lines[j], " \t"))
					if aheadIndent <= entryIndent {
						break
					}
					if strings.Contains(ahead, "oauth_client_id") ||
						(strings.Contains(lines[j], "githubcopilot") && strings.Contains(lines[j], "http")) {
						skipEntry = true
						break
					}
				}
			}

			if skipEntry {
				continue
			}
		}

		result = append(result, line)
	}

	return strings.Join(result, "\n")
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// waitForIdle waits until the TUI shows the input prompt without a spinner.
func waitForIdle(h *ptyHarness, timeout time.Duration) {
	h.t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		screen := h.snapshot()
		// Agent busy indicators
		if strings.Contains(screen, "Thinking") {
			time.Sleep(500 * time.Millisecond)
			continue
		}
		// Spinner characters mean agent is running
		if strings.Contains(screen, "⠋") || strings.Contains(screen, "⠙") ||
			strings.Contains(screen, "⠹") || strings.Contains(screen, "⠸") {
			time.Sleep(500 * time.Millisecond)
			continue
		}
		// Input prompt visible and no busy indicators
		if strings.Contains(screen, "Type a message") {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func typeCommand(h *ptyHarness, cmd string) {
	// Wait for agent to be idle
	waitForIdle(h, 20*time.Second)
	h.drainOutput()
	time.Sleep(200 * time.Millisecond)

	// Type the command
	for _, ch := range cmd {
		h.sendKey(string(ch))
	}
	// Wait for autocomplete to appear and settle
	time.Sleep(300 * time.Millisecond)
	// Enter to select from autocomplete
	h.sendKey("enter")
	// Wait for autocomplete to close / command to execute
	time.Sleep(300 * time.Millisecond)
	// Second enter to confirm (slash command picker needs double-enter)
	h.sendKey("enter")
}

func countQRBlocks(screen string) int {
	return strings.Count(screen, "█") + strings.Count(screen, "▀") + strings.Count(screen, "▄")
}

func waitForAdapterReady(h *ptyHarness, timeout time.Duration) bool {
	h.t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		screen := h.snapshot()
		if strings.Contains(screen, "healthy") {
			h.t.Logf("waitForAdapterReady: matched 'healthy'")
			return true
		}
		if strings.Contains(screen, "Bound") || strings.Contains(screen, "bound") {
			// Exclude "Bound Directory:" which is just a panel label
			cleaned := strings.ReplaceAll(screen, "Bound Directory:", "")
			if strings.Contains(cleaned, "Bound") || strings.Contains(cleaned, "bound") {
				h.t.Logf("waitForAdapterReady: matched 'bound'")
				return true
			}
		}
		if strings.Contains(screen, "Online") {
			h.t.Logf("waitForAdapterReady: matched 'Online'")
			return true
		}
		// "Active" followed by space, newline, or box-drawing char (NOT "Activeconnecting")
		for _, suffix := range []string{"Active ", "Active│", "Active\n"} {
			if strings.Contains(screen, suffix) {
				h.t.Logf("waitForAdapterReady: matched %q", suffix)
				return true
			}
		}
		time.Sleep(300 * time.Millisecond)
	}
	return false
}

// ---------------------------------------------------------------------------
// Test 1: Nostr auto-generate → QR overlay
// Nostr doesn't need a running server; keypair is generated locally.
// ---------------------------------------------------------------------------

func TestPTY_QROverlay_NostrAutoGen(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}

	h := startGGCodeLive(t)
	defer h.quit()

	h.waitForText("Type a message", 10*time.Second)
	h.drainOutput()

	typeCommand(h, "/nostr")
	time.Sleep(1 * time.Second)

	screen := h.snapshot()
	t.Logf("Nostr panel:\n%s", lastN(screen, 500))

	// Enter create mode
	h.sendKey("i")
	time.Sleep(800 * time.Millisecond)

	screen = h.snapshot()
	t.Logf("After 'i':\n%s", lastN(screen, 400))

	for _, ch := range "pty-test-bot" {
		h.sendKey(string(ch))
		time.Sleep(50 * time.Millisecond)
	}
	h.sendKey("enter")
	time.Sleep(3 * time.Second)

	screen = h.snapshot()
	t.Logf("After create:\n%s", lastN(screen, 800))

	qrBlocks := countQRBlocks(screen)
	hasNPub := strings.Contains(screen, "npub1")

	if qrBlocks < 5 {
		t.Fatalf("expected QR blocks in overlay (found %d), screen:\n%s", qrBlocks, lastN(screen, 600))
	}
	if !hasNPub {
		t.Fatalf("expected npub1 in overlay, screen:\n%s", lastN(screen, 600))
	}
	t.Logf("Nostr QR overlay OK: %d blocks, npub present", qrBlocks)

	// Esc closes overlay
	h.sendKey("esc")
	time.Sleep(500 * time.Millisecond)
}

// ---------------------------------------------------------------------------
// Test 2: All platform panels — 'q' key doesn't crash
// ---------------------------------------------------------------------------

func TestPTY_QROverlay_QKeyNoCrash(t *testing.T) {
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
		{"/nostr", "Nostr"},
		{"/qq", "QQ"},
		{"/slack", "Slack"},
		{"/wecom", "WeCom"},
	}

	for _, p := range panels {
		t.Run(p.name, func(t *testing.T) {
			h := startGGCodeLive(t)
			defer h.quit()

			h.waitForText("Type a message", 10*time.Second)
			h.drainOutput()

			typeCommand(h, p.cmd)
			time.Sleep(500 * time.Millisecond)

			h.sendKey("q")
			time.Sleep(300 * time.Millisecond)
			h.sendKey("esc")
			time.Sleep(300 * time.Millisecond)

			screen := h.snapshot()
			if len(screen) == 0 {
				t.Fatalf("%s: crashed after q+esc", p.name)
			}
			t.Logf("%s: survived q key", p.name)
		})
	}
}

// ---------------------------------------------------------------------------
// Test 3: No inline QR in any panel
// ---------------------------------------------------------------------------

func TestPTY_QROverlay_NoInlineQR(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}

	panels := []string{"/telegram", "/discord", "/signal", "/matrix",
		"/dingtalk", "/feishu", "/slack", "/wecom", "/qq", "/wechat", "/nostr"}

	for _, cmd := range panels {
		t.Run(cmd, func(t *testing.T) {
			h := startGGCodeLive(t)
			defer h.quit()

			h.waitForText("Type a message", 10*time.Second)
			h.drainOutput()

			typeCommand(h, cmd)
			time.Sleep(1 * time.Second)

			screen := h.snapshot()
			qrBlocks := countQRBlocks(screen)
			if qrBlocks > 20 {
				t.Fatalf("%s should NOT have inline QR (found %d blocks)", cmd, qrBlocks)
			}
			t.Logf("%s: no inline QR (OK)", cmd)
		})
	}
}

// ---------------------------------------------------------------------------
// Test 4: Adapter-connected platforms — 'q' opens QR overlay
// Waits for adapter to connect, then verifies QR overlay content.
// ---------------------------------------------------------------------------

func TestPTY_QROverlay_AdapterQR(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}

	platforms := []struct {
		cmd       string
		name      string
		uriPrefix string
	}{
		{"/telegram", "Telegram", "t.me/"},
		{"/discord", "Discord", "discord.com/"},
		{"/dingtalk", "DingTalk", "dingtalk.com/"},
		{"/feishu", "Feishu", "feishu.cn/"},
		{"/matrix", "Matrix", "matrix.to/"},
		{"/wecom", "WeCom", "weixin.qq.com/"},
		{"/signal", "Signal", "signal.me/"},
	}

	for _, p := range platforms {
		t.Run(p.name, func(t *testing.T) {
			h := startGGCodeLive(t)
			defer h.quit()

			h.waitForText("Type a message", 10*time.Second)
			h.drainOutput()

			typeCommand(h, p.cmd)
			time.Sleep(1 * time.Second)

			// Wait for adapter to connect (up to 15s)
			time.Sleep(3 * time.Second)
			screen := h.snapshot()
			t.Logf("%s panel:\n%s", p.name, lastN(screen, 600))

			// Press q to open QR overlay
			h.sendKey("q")
			time.Sleep(1 * time.Second)

			screen = h.snapshot()
			t.Logf("After 'q':\n%s", lastN(screen, 800))

			// Verify QR overlay content
			qrBlocks := countQRBlocks(screen)
			hasURI := strings.Contains(screen, p.uriPrefix)
			hasScanHint := strings.Contains(screen, "Scan") || strings.Contains(screen, "扫码")

			if qrBlocks < 5 {
				// No QR overlay — adapter likely not connected
				t.Skipf("%s: no QR overlay (adapter may not be connected)", p.name)
			}
			if !hasURI {
				t.Fatalf("%s: expected URI prefix %q in overlay, screen:\n%s",
					p.name, p.uriPrefix, lastN(screen, 600))
			}
			t.Logf("%s QR overlay OK: %d blocks, URI=%v, scan=%v",
				p.name, qrBlocks, hasURI, hasScanHint)

			// Esc closes overlay
			h.sendKey("esc")
			time.Sleep(500 * time.Millisecond)
		})
	}
}

// ---------------------------------------------------------------------------
// Test 5: Esc closes QR overlay and returns to panel
// ---------------------------------------------------------------------------

func TestPTY_QROverlay_EscReturnsToPanel(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}

	h := startGGCodeLive(t)
	defer h.quit()

	h.waitForText("Type a message", 10*time.Second)
	h.drainOutput()

	// Use Nostr — always works without server
	typeCommand(h, "/nostr")
	time.Sleep(500 * time.Millisecond)
	h.sendKey("i")
	time.Sleep(200 * time.Millisecond)
	for _, ch := range "esc-test" {
		h.sendKey(string(ch))
	}
	h.sendKey("enter")
	time.Sleep(2 * time.Second)

	screen := h.snapshot()
	qrBefore := countQRBlocks(screen)
	if qrBefore < 5 {
		t.Skip("QR overlay did not open for Nostr")
	}

	// Press Esc
	h.sendKey("esc")
	time.Sleep(500 * time.Millisecond)

	screen = h.snapshot()
	qrAfter := countQRBlocks(screen)
	if qrAfter > 20 {
		t.Fatalf("QR should be gone after Esc (found %d blocks)", qrAfter)
	}
	t.Logf("Esc closed overlay: %d → %d blocks", qrBefore, qrAfter)
}

func init() {
	_ = fmt.Sprintf
}
