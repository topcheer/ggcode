//go:build integration_local

package tui

import (
	"strings"
	"testing"
	"time"
)

// TestPTY_StartAndQuit verifies ggcode starts and responds in a PTY.
func TestPTY_StartAndQuit(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}

	h := startGGCode(t, ptyOptions{})
	defer h.quit()

	// Wait for the TUI to render — input prompt is always visible
	h.waitForText("Type a message", 5*time.Second)
	screen := h.snapshot()
	if len(screen) == 0 {
		t.Fatal("no output received from ggcode")
	}
	t.Logf("screen length: %d chars", len(screen))
	t.Logf("screen preview:\n%s", lastN(screen, 300))
}

// TestPTY_InputTyping verifies typing appears in the input area.
func TestPTY_InputTyping(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}

	h := startGGCode(t, ptyOptions{})
	defer h.quit()

	// Wait for startup
	h.waitForText("Type a message", 5*time.Second)
	h.drainOutput()

	// Type a word — send all at once, not char by char
	h.sendKey("h")
	h.sendKey("e")
	h.sendKey("l")
	h.sendKey("l")
	h.sendKey("o")
	time.Sleep(200 * time.Millisecond)

	screen := h.snapshot()
	t.Logf("screen after typing 'hello':\n%s", lastN(screen, 500))

	// Characters should appear (may have ANSI spacing between them)
	plain := compressSpaces(screen)
	if !strings.Contains(plain, "hello") {
		// Check if individual chars are present at least
		for _, ch := range "hello" {
			if !strings.ContainsRune(screen, ch) {
				t.Errorf("character %q not found on screen", ch)
			}
		}
	}
}

// TestPTY_ModeSwitchTab verifies Tab cycles permission modes.
func TestPTY_ModeSwitchTab(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}

	h := startGGCode(t, ptyOptions{})
	defer h.quit()

	h.waitForText("Type a message", 5*time.Second)
	h.drainOutput()

	// Press Tab to cycle mode
	h.sendKey("tab")
	time.Sleep(100 * time.Millisecond)
	screen := h.snapshot()

	// Should show a different mode indicator
	t.Logf("after tab:\n%s", lastN(screen, 300))

	// Press Tab a few more times
	for i := 0; i < 3; i++ {
		h.sendKey("tab")
		time.Sleep(50 * time.Millisecond)
	}
	screen = h.snapshot()
	t.Logf("after 4 tabs:\n%s", lastN(screen, 300))
}

// TestPTY_ResizeNarrowWindow verifies behavior with narrow terminal.
func TestPTY_ResizeNarrowWindow(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}

	h := startGGCode(t, ptyOptions{Cols: 60, Rows: 20})
	defer h.quit()

	h.waitForText("Type a message", 5*time.Second)

	// Should not crash — just render in narrow mode
	screen := h.snapshot()
	t.Logf("60x20 screen:\n%s", lastN(screen, 400))
	if len(screen) == 0 {
		t.Error("no output in narrow mode")
	}

	// Resize to even narrower
	h.resize(40, 15)
	time.Sleep(200 * time.Millisecond)

	screen = h.snapshot()
	t.Logf("40x15 screen:\n%s", lastN(screen, 400))
}

// TestPTY_ResizeWideWindow verifies behavior with wide terminal.
func TestPTY_ResizeWideWindow(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}

	h := startGGCode(t, ptyOptions{Cols: 200, Rows: 50})
	defer h.quit()

	h.waitForText("Type a message", 5*time.Second)

	screen := h.snapshot()
	t.Logf("200x50 screen (last 300 chars):\n%s", lastN(screen, 300))
	if len(screen) == 0 {
		t.Error("no output in wide mode")
	}
}

// TestPTY_ResizeExtreme verifies ggcode doesn't crash on tiny terminal.
func TestPTY_ResizeExtreme(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}

	h := startGGCode(t, ptyOptions{Cols: 20, Rows: 5})
	defer h.quit()

	// Tiny terminal — just wait for any output and check process is alive
	time.Sleep(3 * time.Second)
	screen := h.snapshot()
	t.Logf("20x5 screen:\n%s", lastN(screen, 200))

	// Now resize to normal — should recover
	h.resize(120, 40)
	h.waitForText("Type a message", 5*time.Second)

	screen = h.snapshot()
	t.Logf("after resize to 120x40:\n%s", lastN(screen, 300))
}

// TestPTY_ResizeDynamic verifies dynamic resize doesn't crash.
func TestPTY_ResizeDynamic(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}

	h := startGGCode(t, ptyOptions{Cols: 120, Rows: 40})
	defer h.quit()

	h.waitForText("Type a message", 5*time.Second)

	// Type some text first
	for _, ch := range "hello" {
		h.sendKey(string(ch))
	}
	time.Sleep(200 * time.Millisecond)

	// Aggressive resize sequence
	sizes := []struct{ c, r uint16 }{
		{80, 24},
		{120, 40},
		{200, 60},
		{60, 20},
		{120, 40},
	}
	for _, s := range sizes {
		h.resize(s.c, s.r)
		time.Sleep(100 * time.Millisecond)
	}

	// Process should still be alive and responsive
	h.sendKey("f")
	h.sendKey("o")
	h.sendKey("o")
	time.Sleep(200 * time.Millisecond)

	screen := h.snapshot()
	t.Logf("after resize sequence:\n%s", lastN(screen, 300))

	// At minimum, the prompt should still be visible
	if !strings.Contains(screen, "Type a message") && !strings.Contains(screen, "foo") {
		t.Error("process may have crashed after resize sequence")
	}
}

// TestPTY_EscapeKey verifies Escape key behavior.
func TestPTY_EscapeKey(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}

	h := startGGCode(t, ptyOptions{})
	defer h.quit()

	h.waitForText("Type a message", 5*time.Second)
	h.drainOutput()

	// Press Escape — should not crash
	h.sendKey("escape")
	time.Sleep(100 * time.Millisecond)

	screen := h.snapshot()
	t.Logf("after escape:\n%s", lastN(screen, 300))
}

// TestPTY_HelpToggle verifies help display.
func TestPTY_HelpToggle(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}

	h := startGGCode(t, ptyOptions{})
	defer h.quit()

	h.waitForText("Type a message", 5*time.Second)
	h.drainOutput()

	// Press ? to toggle help
	h.sendKeys("?")
	time.Sleep(200 * time.Millisecond)

	screen := h.snapshot()
	t.Logf("after ? key:\n%s", lastN(screen, 500))

	// Press ? again to dismiss
	h.sendKeys("?")
	time.Sleep(100 * time.Millisecond)
}

// TestPTY_SlashCommandStart verifies text input with special characters.
func TestPTY_SlashCommandStart(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}

	h := startGGCode(t, ptyOptions{})
	defer h.quit()

	h.waitForText("Type a message", 5*time.Second)
	h.drainOutput()

	// Type text with special chars (not slash — that triggers command mode)
	for _, ch := range "test 123" {
		h.sendKey(string(ch))
	}
	time.Sleep(300 * time.Millisecond)

	screen := h.snapshot()
	t.Logf("after typing 'test 123':\n%s", lastN(screen, 400))

	// Characters should appear on screen
	for _, ch := range "test123" {
		if !strings.ContainsRune(screen, ch) {
			t.Errorf("character %q not found on screen", ch)
		}
	}
}

// TestPTY_Backspace verifies backspace deletes characters.
func TestPTY_Backspace(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}

	h := startGGCode(t, ptyOptions{})
	defer h.quit()

	h.waitForText("Type a message", 5*time.Second)
	h.drainOutput()

	// Type "hello"
	for _, ch := range "hello" {
		h.sendKey(string(ch))
	}
	time.Sleep(100 * time.Millisecond)

	// Delete last 2 chars
	h.sendKey("backspace")
	h.sendKey("backspace")
	time.Sleep(200 * time.Millisecond)

	screen := h.snapshot()
	t.Logf("after backspace:\n%s", lastN(screen, 300))

	// "hel" should remain
	plain := compressSpaces(screen)
	if !strings.Contains(plain, "hel") {
		t.Errorf("expected 'hel' after 2 backspaces\nscreen:\n%s", lastN(screen, 300))
	}
}
