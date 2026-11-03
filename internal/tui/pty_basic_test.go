//go:build integration_local

package tui

import (
	"strings"
	"testing"
	"time"
)

func TestPTY_StartAndQuit(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}

	h := startGGCode(t, ptyOptions{})
	defer h.quit()

	h.waitForText("Type a message", 5*time.Second)
	screen := h.snapshot()
	if len(screen) == 0 {
		t.Fatal("no output received from ggcode")
	}
	t.Logf("screen length: %d chars", len(screen))
	t.Logf("screen preview:\n%s", lastN(screen, 300))
}

func TestPTY_InputTyping(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}

	h := startGGCode(t, ptyOptions{})
	defer h.quit()

	h.waitForText("Type a message", 5*time.Second)
	h.drainOutput()

	h.sendKey("h")
	h.sendKey("e")
	h.sendKey("l")
	h.sendKey("l")
	h.sendKey("o")
	time.Sleep(200 * time.Millisecond)

	screen := h.snapshot()
	t.Logf("screen after typing 'hello':\n%s", lastN(screen, 500))

	plain := compressSpaces(screen)
	if !strings.Contains(plain, "hello") {

		for _, ch := range "hello" {
			if !strings.ContainsRune(screen, ch) {
				t.Errorf("character %q not found on screen", ch)
			}
		}
	}
}

func TestPTY_ModeSwitchTab(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}

	h := startGGCode(t, ptyOptions{})
	defer h.quit()

	h.waitForText("Type a message", 5*time.Second)
	h.drainOutput()

	h.sendKey("tab")
	time.Sleep(100 * time.Millisecond)
	screen := h.snapshot()

	t.Logf("after tab:\n%s", lastN(screen, 300))

	for i := 0; i < 3; i++ {
		h.sendKey("tab")
		time.Sleep(50 * time.Millisecond)
	}
	screen = h.snapshot()
	t.Logf("after 4 tabs:\n%s", lastN(screen, 300))
}

func TestPTY_ResizeNarrowWindow(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}

	h := startGGCode(t, ptyOptions{Cols: 60, Rows: 20})
	defer h.quit()

	h.waitForText("Type a message", 5*time.Second)

	screen := h.snapshot()
	t.Logf("60x20 screen:\n%s", lastN(screen, 400))
	if len(screen) == 0 {
		t.Error("no output in narrow mode")
	}

	h.resize(40, 15)
	time.Sleep(200 * time.Millisecond)

	screen = h.snapshot()
	t.Logf("40x15 screen:\n%s", lastN(screen, 400))
}

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

func TestPTY_ResizeExtreme(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}

	h := startGGCode(t, ptyOptions{Cols: 20, Rows: 5})
	defer h.quit()

	time.Sleep(3 * time.Second)
	screen := h.snapshot()
	t.Logf("20x5 screen:\n%s", lastN(screen, 200))

	h.resize(120, 40)
	h.waitForText("Type a message", 5*time.Second)

	screen = h.snapshot()
	t.Logf("after resize to 120x40:\n%s", lastN(screen, 300))
}

func TestPTY_ResizeDynamic(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}

	h := startGGCode(t, ptyOptions{Cols: 120, Rows: 40})
	defer h.quit()

	h.waitForText("Type a message", 5*time.Second)

	for _, ch := range "hello" {
		h.sendKey(string(ch))
	}
	time.Sleep(200 * time.Millisecond)

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

	h.sendKey("f")
	h.sendKey("o")
	h.sendKey("o")
	time.Sleep(200 * time.Millisecond)

	screen := h.snapshot()
	t.Logf("after resize sequence:\n%s", lastN(screen, 300))

	if !strings.Contains(screen, "Type a message") && !strings.Contains(screen, "foo") {
		t.Error("process may have crashed after resize sequence")
	}
}

func TestPTY_EscapeKey(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}

	h := startGGCode(t, ptyOptions{})
	defer h.quit()

	h.waitForText("Type a message", 5*time.Second)
	h.drainOutput()

	h.sendKey("escape")
	time.Sleep(100 * time.Millisecond)

	screen := h.snapshot()
	t.Logf("after escape:\n%s", lastN(screen, 300))
}

func TestPTY_HelpToggle(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}

	h := startGGCode(t, ptyOptions{})
	defer h.quit()

	h.waitForText("Type a message", 5*time.Second)
	h.drainOutput()

	h.sendKeys("?")
	time.Sleep(200 * time.Millisecond)

	screen := h.snapshot()
	t.Logf("after ? key:\n%s", lastN(screen, 500))

	h.sendKeys("?")
	time.Sleep(100 * time.Millisecond)
}

func TestPTY_SlashCommandStart(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}

	h := startGGCode(t, ptyOptions{})
	defer h.quit()

	h.waitForText("Type a message", 5*time.Second)
	h.drainOutput()

	for _, ch := range "test 123" {
		h.sendKey(string(ch))
	}
	time.Sleep(300 * time.Millisecond)

	screen := h.snapshot()
	t.Logf("after typing 'test 123':\n%s", lastN(screen, 400))

	for _, ch := range "test123" {
		if !strings.ContainsRune(screen, ch) {
			t.Errorf("character %q not found on screen", ch)
		}
	}
}

func TestPTY_Backspace(t *testing.T) {
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
	time.Sleep(100 * time.Millisecond)

	h.sendKey("backspace")
	h.sendKey("backspace")
	time.Sleep(200 * time.Millisecond)

	screen := h.snapshot()
	t.Logf("after backspace:\n%s", lastN(screen, 300))

	plain := compressSpaces(screen)
	if !strings.Contains(plain, "hel") {
		t.Errorf("expected 'hel' after 2 backspaces\nscreen:\n%s", lastN(screen, 300))
	}
}
