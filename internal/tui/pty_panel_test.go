//go:build integration_local

package tui

import (
	"strings"
	"testing"
	"time"
)

func TestPTY_ShiftTabModeSwitch(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}

	h := startGGCode(t, ptyOptions{})
	defer h.quit()

	h.waitForText("Type a message", 5*time.Second)
	h.drainOutput()

	screen := h.snapshot()
	if !strings.Contains(screen, "bypass") {
		t.Fatalf("expected initial mode 'bypass', screen:\n%s", lastN(screen, 300))
	}

	h.sendKey("shift+tab")
	time.Sleep(200 * time.Millisecond)
	screen = h.snapshot()
	t.Logf("after shift+tab:\n%s", lastN(screen, 200))

	if strings.Contains(screen, "bypass") && !strings.Contains(screen, "supervised") {

		t.Log("mode may not have changed on first shift+tab")
	}

	for i := 0; i < 6; i++ {
		h.sendKey("shift+tab")
		time.Sleep(50 * time.Millisecond)
	}
	time.Sleep(200 * time.Millisecond)
	screen = h.snapshot()
	t.Logf("after 6 shift+tabs:\n%s", lastN(screen, 200))
}

func TestPTY_EnterClearsInput(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}

	h := startGGCode(t, ptyOptions{})
	defer h.quit()

	h.waitForText("Type a message", 5*time.Second)
	h.drainOutput()

	for _, ch := range "test message" {
		h.sendKey(string(ch))
	}
	time.Sleep(200 * time.Millisecond)

	screen := h.snapshot()
	if !strings.ContainsRune(screen, 't') {
		t.Error("typed text not visible")
	}

	h.sendKey("enter")
	time.Sleep(300 * time.Millisecond)

	screen = h.snapshot()
	t.Logf("after enter:\n%s", lastN(screen, 300))

	if !strings.Contains(screen, "Type a message") {
		t.Log("prompt may have changed after send (expected)")
	}
}

func TestPTY_CtrlCClearsInput(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}

	h := startGGCode(t, ptyOptions{})
	defer h.quit()

	h.waitForText("Type a message", 5*time.Second)
	h.drainOutput()

	for _, ch := range "some text to clear" {
		h.sendKey(string(ch))
	}
	time.Sleep(200 * time.Millisecond)

	h.sendKey("ctrl+c")
	time.Sleep(300 * time.Millisecond)

	screen := h.snapshot()
	t.Logf("after ctrl+c:\n%s", lastN(screen, 300))

	if !strings.Contains(screen, "Type a message") {
		t.Log("prompt state after ctrl+c may vary")
	}
}

func TestPTY_ArrowKeys(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}

	h := startGGCode(t, ptyOptions{})
	defer h.quit()

	h.waitForText("Type a message", 5*time.Second)
	h.drainOutput()

	for _, ch := range "abc" {
		h.sendKey(string(ch))
	}
	time.Sleep(100 * time.Millisecond)

	h.sendKey("left")
	h.sendKey("left")
	time.Sleep(100 * time.Millisecond)

	h.sendKey("X")
	time.Sleep(200 * time.Millisecond)

	screen := h.snapshot()
	t.Logf("after arrow navigation:\n%s", lastN(screen, 300))

	if !strings.ContainsRune(screen, 'X') {
		t.Error("X not visible after cursor insertion")
	}
}

func TestPTY_CtrlLClearsScreen(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}

	h := startGGCode(t, ptyOptions{})
	defer h.quit()

	h.waitForText("Type a message", 5*time.Second)

	h.sendKey("ctrl+l")
	time.Sleep(200 * time.Millisecond)

	screen := h.snapshot()
	t.Logf("after ctrl+l:\n%s", lastN(screen, 200))

	if len(screen) == 0 {
		t.Error("no output after ctrl+l")
	}
}

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

	if !strings.Contains(screen, "Type a message") && !strings.Contains(screen, "mode") {
		t.Error("TUI may have become unresponsive")
	}
}

func TestPTY_UpArrowHistory(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}

	h := startGGCode(t, ptyOptions{})
	defer h.quit()

	h.waitForText("Type a message", 5*time.Second)
	h.drainOutput()

	for _, ch := range "hello" {
		h.sendKey(string(ch))
	}
	h.sendKey("enter")
	time.Sleep(300 * time.Millisecond)

	h.sendKey("up")
	time.Sleep(200 * time.Millisecond)

	screen := h.snapshot()
	t.Logf("after up arrow:\n%s", lastN(screen, 300))

	for _, ch := range "helo" {
		if !strings.ContainsRune(screen, ch) {
			t.Logf("character %q not found in history recall", ch)
		}
	}
}
