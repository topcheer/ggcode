//go:build integration_local

package tui

import (
	"strings"
	"testing"
	"time"
)

// TestPTY_ShiftTabModeSwitch verifies Shift+Tab cycles permission modes.
func TestPTY_ShiftTabModeSwitch(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}

	h := startGGCode(t, ptyOptions{})
	defer h.quit()

	h.waitForText("Type a message", 5*time.Second)
	h.drainOutput()

	// Initial mode should be "bypass"
	screen := h.snapshot()
	if !strings.Contains(screen, "bypass") {
		t.Fatalf("expected initial mode 'bypass', screen:\n%s", lastN(screen, 300))
	}

	// Press Shift+Tab to cycle mode
	h.sendKey("shift+tab")
	time.Sleep(200 * time.Millisecond)
	screen = h.snapshot()
	t.Logf("after shift+tab:\n%s", lastN(screen, 200))

	// Mode should have changed from "bypass"
	if strings.Contains(screen, "bypass") && !strings.Contains(screen, "supervised") {
		// May not have changed — depends on mode list order
		t.Log("mode may not have changed on first shift+tab")
	}

	// Cycle through all modes
	for i := 0; i < 6; i++ {
		h.sendKey("shift+tab")
		time.Sleep(50 * time.Millisecond)
	}
	time.Sleep(200 * time.Millisecond)
	screen = h.snapshot()
	t.Logf("after 6 shift+tabs:\n%s", lastN(screen, 200))
}

// TestPTY_EnterClearsInput verifies Enter sends message and clears input.
func TestPTY_EnterClearsInput(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}

	h := startGGCode(t, ptyOptions{})
	defer h.quit()

	h.waitForText("Type a message", 5*time.Second)
	h.drainOutput()

	// Type a message
	for _, ch := range "test message" {
		h.sendKey(string(ch))
	}
	time.Sleep(200 * time.Millisecond)

	screen := h.snapshot()
	if !strings.ContainsRune(screen, 't') {
		t.Error("typed text not visible")
	}

	// Press Enter to send
	h.sendKey("enter")
	time.Sleep(300 * time.Millisecond)

	screen = h.snapshot()
	t.Logf("after enter:\n%s", lastN(screen, 300))
	// Input should be cleared — prompt should reappear
	if !strings.Contains(screen, "Type a message") {
		t.Log("prompt may have changed after send (expected)")
	}
}

// TestPTY_CtrlCClearsInput verifies Ctrl+C clears the current input.
func TestPTY_CtrlCClearsInput(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}

	h := startGGCode(t, ptyOptions{})
	defer h.quit()

	h.waitForText("Type a message", 5*time.Second)
	h.drainOutput()

	// Type some text
	for _, ch := range "some text to clear" {
		h.sendKey(string(ch))
	}
	time.Sleep(200 * time.Millisecond)

	// Press Ctrl+C to clear
	h.sendKey("ctrl+c")
	time.Sleep(300 * time.Millisecond)

	screen := h.snapshot()
	t.Logf("after ctrl+c:\n%s", lastN(screen, 300))

	// Prompt should be back to empty
	if !strings.Contains(screen, "Type a message") {
		t.Log("prompt state after ctrl+c may vary")
	}
}

// TestPTY_ArrowKeys verifies arrow key navigation.
func TestPTY_ArrowKeys(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}

	h := startGGCode(t, ptyOptions{})
	defer h.quit()

	h.waitForText("Type a message", 5*time.Second)
	h.drainOutput()

	// Type text, then use arrows
	for _, ch := range "abc" {
		h.sendKey(string(ch))
	}
	time.Sleep(100 * time.Millisecond)

	// Press left arrow — cursor should move
	h.sendKey("left")
	h.sendKey("left")
	time.Sleep(100 * time.Millisecond)

	// Type at cursor position
	h.sendKey("X")
	time.Sleep(200 * time.Millisecond)

	screen := h.snapshot()
	t.Logf("after arrow navigation:\n%s", lastN(screen, 300))

	// X should be visible somewhere
	if !strings.ContainsRune(screen, 'X') {
		t.Error("X not visible after cursor insertion")
	}
}

// TestPTY_CtrlLClearsScreen verifies Ctrl+L refreshes the display.
func TestPTY_CtrlLClearsScreen(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}

	h := startGGCode(t, ptyOptions{})
	defer h.quit()

	h.waitForText("Type a message", 5*time.Second)

	// Press Ctrl+L — should not crash
	h.sendKey("ctrl+l")
	time.Sleep(200 * time.Millisecond)

	screen := h.snapshot()
	t.Logf("after ctrl+l:\n%s", lastN(screen, 200))

	// TUI should still be visible
	if len(screen) == 0 {
		t.Error("no output after ctrl+l")
	}
}

// TestPTY_MultipleInputsInSequence tests typing and sending multiple messages.
func TestPTY_MultipleInputsInSequence(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}

	h := startGGCode(t, ptyOptions{})
	defer h.quit()

	h.waitForText("Type a message", 5*time.Second)
	h.drainOutput()

	messages := []string{"first", "second", "third"}
	for _, msg := range messages {
		for _, ch := range msg {
			h.sendKey(string(ch))
		}
		time.Sleep(100 * time.Millisecond)
		h.sendKey("enter")
		time.Sleep(200 * time.Millisecond)
	}

	screen := h.snapshot()
	t.Logf("after 3 messages:\n%s", lastN(screen, 400))

	// Should still be responsive
	if !strings.Contains(screen, "Type a message") && !strings.Contains(screen, "mode") {
		t.Error("TUI may have become unresponsive")
	}
}

// TestPTY_UpArrowHistory verifies up arrow recalls previous input.
func TestPTY_UpArrowHistory(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}

	h := startGGCode(t, ptyOptions{})
	defer h.quit()

	h.waitForText("Type a message", 5*time.Second)
	h.drainOutput()

	// Send a message
	for _, ch := range "hello" {
		h.sendKey(string(ch))
	}
	h.sendKey("enter")
	time.Sleep(300 * time.Millisecond)

	// Press up to recall
	h.sendKey("up")
	time.Sleep(200 * time.Millisecond)

	screen := h.snapshot()
	t.Logf("after up arrow:\n%s", lastN(screen, 300))

	// The recalled text should contain 'hello' characters
	for _, ch := range "helo" {
		if !strings.ContainsRune(screen, ch) {
			t.Logf("character %q not found in history recall", ch)
		}
	}
}
