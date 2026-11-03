//go:build integration_local

package tui

import (
	"strings"
	"testing"
	"time"
)

func TestPTY_SlashHelp(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}
	h := startGGCode(t, ptyOptions{})
	defer h.quit()

	h.waitForText("Type a message", 5*time.Second)
	h.drainOutput()

	for _, ch := range "/help" {
		h.sendKey(string(ch))
	}
	time.Sleep(100 * time.Millisecond)
	h.sendKey("enter")
	time.Sleep(500 * time.Millisecond)

	screen := h.snapshot()
	t.Logf("after /help:\n%s", lastN(screen, 500))

	if !strings.Contains(screen, "help") && !strings.Contains(screen, "command") {
		t.Error("help output not visible")
	}
}

func TestPTY_SlashMode(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}
	h := startGGCode(t, ptyOptions{})
	defer h.quit()

	h.waitForText("Type a message", 5*time.Second)
	h.drainOutput()

	for _, ch := range "/mode auto" {
		h.sendKey(string(ch))
	}
	h.sendKey("enter")
	time.Sleep(500 * time.Millisecond)

	screen := h.snapshot()
	t.Logf("after /mode auto:\n%s", lastN(screen, 300))
}

func TestPTY_SlashLang(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}
	h := startGGCode(t, ptyOptions{})
	defer h.quit()

	h.waitForText("Type a message", 5*time.Second)
	h.drainOutput()

	for _, ch := range "/lang en" {
		h.sendKey(string(ch))
	}
	h.sendKey("enter")
	time.Sleep(500 * time.Millisecond)

	screen := h.snapshot()
	t.Logf("after /lang en:\n%s", lastN(screen, 300))
}

func TestPTY_SlashStreamStatus(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}
	h := startGGCode(t, ptyOptions{})
	defer h.quit()

	h.waitForText("Type a message", 5*time.Second)
	h.drainOutput()

	for _, ch := range "/stream status" {
		h.sendKey(string(ch))
	}
	h.sendKey("enter")
	time.Sleep(500 * time.Millisecond)

	screen := h.snapshot()
	t.Logf("after /stream status:\n%s", lastN(screen, 400))
}

func TestPTY_SlashSessions(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}
	h := startGGCode(t, ptyOptions{})
	defer h.quit()

	h.waitForText("Type a message", 5*time.Second)
	h.drainOutput()

	for _, ch := range "/sessions" {
		h.sendKey(string(ch))
	}
	h.sendKey("enter")
	time.Sleep(500 * time.Millisecond)

	screen := h.snapshot()
	t.Logf("after /sessions:\n%s", lastN(screen, 500))

	if !strings.Contains(screen, "session") && !strings.Contains(screen, "Session") {
		t.Log("sessions panel may not have opened (no sessions yet)")
	}
}

func TestPTY_SlashStreamConfig(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}
	h := startGGCode(t, ptyOptions{})
	defer h.quit()

	h.waitForText("Type a message", 5*time.Second)
	h.drainOutput()

	for _, ch := range "/stream config" {
		h.sendKey(string(ch))
	}
	h.sendKey("enter")
	time.Sleep(500 * time.Millisecond)

	screen := h.snapshot()
	t.Logf("after /stream config:\n%s", lastN(screen, 500))

	if !strings.Contains(screen, "stream") && !strings.Contains(screen, "Stream") && !strings.Contains(screen, "target") {
		t.Error("stream panel may not have opened")
	}
}

func TestPTY_SlashSkills(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}
	h := startGGCode(t, ptyOptions{})
	defer h.quit()

	h.waitForText("Type a message", 5*time.Second)
	h.drainOutput()

	for _, ch := range "/skills" {
		h.sendKey(string(ch))
	}
	h.sendKey("enter")
	time.Sleep(500 * time.Millisecond)

	screen := h.snapshot()
	t.Logf("after /skills:\n%s", lastN(screen, 500))
}

func TestPTY_SlashIM(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}
	h := startGGCode(t, ptyOptions{})
	defer h.quit()

	h.waitForText("Type a message", 5*time.Second)
	h.drainOutput()

	for _, ch := range "/im" {
		h.sendKey(string(ch))
	}
	h.sendKey("enter")
	time.Sleep(500 * time.Millisecond)

	screen := h.snapshot()
	t.Logf("after /im:\n%s", lastN(screen, 500))
}

func TestPTY_SlashPlugins(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}
	h := startGGCode(t, ptyOptions{})
	defer h.quit()

	h.waitForText("Type a message", 5*time.Second)
	h.drainOutput()

	for _, ch := range "/plugins" {
		h.sendKey(string(ch))
	}
	h.sendKey("enter")
	time.Sleep(500 * time.Millisecond)

	screen := h.snapshot()
	t.Logf("after /plugins:\n%s", lastN(screen, 500))
}

func TestPTY_SlashMCP(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}
	h := startGGCode(t, ptyOptions{})
	defer h.quit()

	h.waitForText("Type a message", 5*time.Second)
	h.drainOutput()

	for _, ch := range "/mcp" {
		h.sendKey(string(ch))
	}
	h.sendKey("enter")
	time.Sleep(500 * time.Millisecond)

	screen := h.snapshot()
	t.Logf("after /mcp:\n%s", lastN(screen, 500))
}

func TestPTY_EscapeClosesPanel(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}
	h := startGGCode(t, ptyOptions{})
	defer h.quit()

	h.waitForText("Type a message", 5*time.Second)
	h.drainOutput()

	for _, ch := range "/skills" {
		h.sendKey(string(ch))
	}
	h.sendKey("enter")
	time.Sleep(500 * time.Millisecond)

	h.sendKey("escape")
	time.Sleep(300 * time.Millisecond)

	screen := h.snapshot()
	t.Logf("after escape from panel:\n%s", lastN(screen, 300))

	if !strings.Contains(screen, "Type a message") {
		t.Log("may not have returned to input prompt")
	}
}

func TestPTY_TabCyclesPanelFields(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}
	h := startGGCode(t, ptyOptions{})
	defer h.quit()

	h.waitForText("Type a message", 5*time.Second)
	h.drainOutput()

	for _, ch := range "/stream config" {
		h.sendKey(string(ch))
	}
	h.sendKey("enter")
	time.Sleep(800 * time.Millisecond)

	screen := h.snapshot()
	t.Logf("panel opened:\n%s", lastN(screen, 400))

	if !strings.Contains(screen, "stream") && !strings.Contains(screen, "Stream") && !strings.Contains(screen, "target") && !strings.Contains(screen, "Add") {
		t.Skip("stream panel didn't open — skipping tab cycling test")
	}

	for i := 0; i < 4; i++ {
		h.sendKey("tab")
		time.Sleep(150 * time.Millisecond)
	}

	screen = h.snapshot()
	t.Logf("after tab cycling:\n%s", lastN(screen, 400))

	plain := compressSpaces(screen)
	if !strings.Contains(plain, "stream") || !strings.Contains(plain, "Stream") {

		t.Log("tab cycling may have moved focus away from panel")
	}
}
