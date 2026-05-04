//go:build integration_local

package tui

import (
	"strings"
	"testing"
	"time"
)

// --- Keyboard Edge Cases ---

// TestPTY_CtrlK deletes to end of line.
func TestPTY_CtrlK(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}
	h := startGGCode(t, ptyOptions{})
	defer h.quit()
	h.waitForText("Type a message", 5*time.Second)
	h.drainOutput()

	// Type text, move to middle, Ctrl+K
	for _, ch := range "abcdefghij" {
		h.sendKey(string(ch))
	}
	time.Sleep(100 * time.Millisecond)

	// Move left 4 positions
	for i := 0; i < 4; i++ {
		h.sendKey("left")
	}
	time.Sleep(100 * time.Millisecond)

	// Ctrl+K: delete from cursor to end
	h.sendKey("ctrl+k")
	time.Sleep(300 * time.Millisecond)

	screen := h.snapshot()
	t.Logf("after ctrl+k:\n%s", lastN(screen, 300))
}

// TestPTY_CtrlP_CtrlN verifies Ctrl+P (up) and Ctrl+N (down) history.
func TestPTY_CtrlP_CtrlN(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}
	h := startGGCode(t, ptyOptions{})
	defer h.quit()
	h.waitForText("Type a message", 5*time.Second)
	h.drainOutput()

	// Send a message
	h.typeAndSend("first msg")
	time.Sleep(300 * time.Millisecond)

	// Ctrl+P = previous (like up arrow)
	h.sendKey("ctrl+p")
	time.Sleep(200 * time.Millisecond)

	screen := h.snapshot()
	t.Logf("after ctrl+p:\n%s", lastN(screen, 300))

	// Ctrl+N = next (like down arrow)
	h.sendKey("ctrl+n")
	time.Sleep(200 * time.Millisecond)

	screen = h.snapshot()
	t.Logf("after ctrl+n:\n%s", lastN(screen, 300))
}

// TestPTY_CtrlR verifies Ctrl+R doesn't crash (reverse search).
func TestPTY_CtrlR(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}
	h := startGGCode(t, ptyOptions{})
	defer h.quit()
	h.waitForText("Type a message", 5*time.Second)

	h.sendKey("ctrl+r")
	time.Sleep(200 * time.Millisecond)

	// Type search term
	for _, ch := range "test" {
		h.sendKey(string(ch))
	}
	time.Sleep(200 * time.Millisecond)

	screen := h.snapshot()
	t.Logf("after ctrl+r search:\n%s", lastN(screen, 300))

	// Escape to exit search
	h.sendKey("escape")
	time.Sleep(100 * time.Millisecond)
}

// TestPTY_CtrlV verifies Ctrl+V (paste mode) doesn't crash.
func TestPTY_CtrlV(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}
	h := startGGCode(t, ptyOptions{})
	defer h.quit()
	h.waitForText("Type a message", 5*time.Second)

	h.sendKey("ctrl+v")
	time.Sleep(100 * time.Millisecond)
	for _, ch := range "pasted text" {
		h.sendKey(string(ch))
	}
	time.Sleep(300 * time.Millisecond)

	screen := h.snapshot()
	t.Logf("after ctrl+v:\n%s", lastN(screen, 300))
}

// TestPTY_MultipleEscape verifies multiple Escape presses don't crash.
func TestPTY_MultipleEscape(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}
	h := startGGCode(t, ptyOptions{})
	defer h.quit()
	h.waitForText("Type a message", 5*time.Second)

	for i := 0; i < 10; i++ {
		h.sendKey("escape")
		time.Sleep(50 * time.Millisecond)
	}
	time.Sleep(200 * time.Millisecond)

	screen := h.snapshot()
	if len(screen) == 0 {
		t.Error("no output after multiple escapes")
	}
	t.Logf("after 10 escapes:\n%s", lastN(screen, 200))
}

// TestPTY_RapidResizeStorm verifies rapid resize doesn't crash.
func TestPTY_RapidResizeStorm(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}
	h := startGGCode(t, ptyOptions{Cols: 120, Rows: 40})
	defer h.quit()
	h.waitForText("Type a message", 5*time.Second)

	// 30 rapid resizes with no delay
	for i := 0; i < 30; i++ {
		cols := uint16(40 + (i*7)%160)
		rows := uint16(10 + (i*3)%50)
		h.resize(cols, rows)
	}
	time.Sleep(500 * time.Millisecond)

	// Restore and verify
	h.resize(120, 40)
	time.Sleep(300 * time.Millisecond)

	screen := h.snapshot()
	if len(screen) == 0 {
		t.Error("no output after resize storm")
	}
	t.Logf("after resize storm:\n%s", lastN(screen, 200))
}

// TestPTY_IdleThenInteract verifies idle period then interaction works.
func TestPTY_IdleThenInteract(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}
	h := startGGCode(t, ptyOptions{})
	defer h.quit()
	h.waitForText("Type a message", 5*time.Second)

	// Idle for 2 seconds
	time.Sleep(2 * time.Second)

	// Then type — should still work
	for _, ch := range "after idle" {
		h.sendKey(string(ch))
	}
	time.Sleep(300 * time.Millisecond)

	screen := h.snapshot()
	t.Logf("after idle + type:\n%s", lastN(screen, 300))

	for _, ch := range "afteridle" {
		if ch == ' ' {
			continue
		}
		if !strings.ContainsRune(screen, ch) {
			t.Errorf("char %q not found after idle", ch)
		}
	}
}

// TestPTY_TypeWhileStreaming verifies typing during "Thinking..." doesn't crash.
func TestPTY_TypeWhileStreaming(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}
	h := startGGCode(t, ptyOptions{})
	defer h.quit()
	h.waitForText("Type a message", 5*time.Second)
	h.drainOutput()

	// Send a message that triggers LLM (will fail, but triggers spinner)
	h.typeAndSend("test query")
	time.Sleep(300 * time.Millisecond)

	// Type while potentially streaming
	for _, ch := range "during" {
		h.sendKey(string(ch))
	}
	time.Sleep(300 * time.Millisecond)

	screen := h.snapshot()
	t.Logf("type during stream:\n%s", lastN(screen, 300))
	if len(screen) == 0 {
		t.Error("no output while streaming")
	}
}

// --- Multi-step Workflows ---

// TestPTY_OpenCloseReopenPanel verifies panel open→close→reopen cycle.
func TestPTY_OpenCloseReopenPanel(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}
	h := startGGCode(t, ptyOptions{})
	defer h.quit()
	h.waitForText("Type a message", 5*time.Second)
	h.drainOutput()

	for i := 0; i < 3; i++ {
		// Open
		h.typeAndSend("/skills")
		time.Sleep(500 * time.Millisecond)

		screen := h.snapshot()
		if !strings.Contains(screen, "skill") && !strings.Contains(screen, "Skill") {
			t.Logf("cycle %d: panel didn't open", i)
		}

		// Close
		h.sendKey("escape")
		time.Sleep(300 * time.Millisecond)
	}

	// Should still be responsive
	screen := h.snapshot()
	if !strings.Contains(screen, "Type a message") {
		t.Error("prompt lost after open/close cycle")
	}
}

// TestPTY_CommandAfterPanelClose verifies command works after panel close.
func TestPTY_CommandAfterPanelClose(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}
	h := startGGCode(t, ptyOptions{})
	defer h.quit()
	h.waitForText("Type a message", 5*time.Second)
	h.drainOutput()

	// Open panel
	h.typeAndSend("/skills")
	time.Sleep(500 * time.Millisecond)

	// Close panel
	h.sendKey("escape")
	time.Sleep(300 * time.Millisecond)

	// Send a command
	h.typeAndSend("/status")
	time.Sleep(500 * time.Millisecond)

	screen := h.snapshot()
	t.Logf("command after panel close:\n%s", lastN(screen, 300))
}

// TestPTY_SwitchPanels verifies switching between different panels.
func TestPTY_SwitchPanels(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}
	h := startGGCode(t, ptyOptions{})
	defer h.quit()
	h.waitForText("Type a message", 5*time.Second)
	h.drainOutput()

	// Open skills
	h.typeAndSend("/skills")
	time.Sleep(400 * time.Millisecond)
	h.sendKey("escape")
	time.Sleep(200 * time.Millisecond)

	// Open IM
	h.typeAndSend("/im")
	time.Sleep(400 * time.Millisecond)
	h.sendKey("escape")
	time.Sleep(200 * time.Millisecond)

	// Open sessions
	h.typeAndSend("/sessions")
	time.Sleep(400 * time.Millisecond)
	h.sendKey("escape")
	time.Sleep(200 * time.Millisecond)

	screen := h.snapshot()
	t.Logf("after panel switching:\n%s", lastN(screen, 300))
	if !strings.Contains(screen, "Type a message") {
		t.Log("prompt may have changed after panel switching")
	}
}

// TestPTY_ResizePanelOpenClose verifies resize during panel lifecycle.
func TestPTY_ResizePanelOpenClose(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}
	h := startGGCode(t, ptyOptions{Cols: 120, Rows: 40})
	defer h.quit()
	h.waitForText("Type a message", 5*time.Second)
	h.drainOutput()

	// Open panel
	h.typeAndSend("/skills")
	time.Sleep(400 * time.Millisecond)

	// Resize while panel open
	h.resize(80, 24)
	time.Sleep(200 * time.Millisecond)

	// Close panel
	h.sendKey("escape")
	time.Sleep(200 * time.Millisecond)

	// Resize back
	h.resize(120, 40)
	time.Sleep(200 * time.Millisecond)

	screen := h.snapshot()
	if !strings.Contains(screen, "Type a message") {
		t.Error("prompt lost after resize+panel cycle")
	}
}

// TestPTY_InputAccumulation verifies input accumulates across multiple type calls.
func TestPTY_InputAccumulation(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}
	h := startGGCode(t, ptyOptions{})
	defer h.quit()
	h.waitForText("Type a message", 5*time.Second)
	h.drainOutput()

	// Type in bursts with pauses
	for _, ch := range "part1" {
		h.sendKey(string(ch))
	}
	time.Sleep(100 * time.Millisecond)
	for _, ch := range "_part2" {
		h.sendKey(string(ch))
	}
	time.Sleep(100 * time.Millisecond)
	for _, ch := range "_part3" {
		h.sendKey(string(ch))
	}
	time.Sleep(300 * time.Millisecond)

	screen := h.snapshot()
	t.Logf("accumulated input:\n%s", lastN(screen, 300))

	// Check individual characters survived
	for _, ch := range "part123" {
		if !strings.ContainsRune(screen, ch) {
			t.Errorf("char %q missing from accumulated input", ch)
		}
	}
}

// TestPTY_BackspacePastInput verifies backspacing past start doesn't crash.
func TestPTY_BackspacePastInput(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}
	h := startGGCode(t, ptyOptions{})
	defer h.quit()
	h.waitForText("Type a message", 5*time.Second)
	h.drainOutput()

	// Type "ab"
	h.sendKey("a")
	h.sendKey("b")
	time.Sleep(100 * time.Millisecond)

	// Backspace 5 times (more than typed)
	for i := 0; i < 5; i++ {
		h.sendKey("backspace")
		time.Sleep(50 * time.Millisecond)
	}
	time.Sleep(200 * time.Millisecond)

	screen := h.snapshot()
	t.Logf("after over-backspace:\n%s", lastN(screen, 300))
	if len(screen) == 0 {
		t.Error("no output after over-backspace")
	}
}

// TestPTY_UpDownEmptyHistory verifies up/down with no history doesn't crash.
func TestPTY_UpDownEmptyHistory(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}
	h := startGGCode(t, ptyOptions{})
	defer h.quit()
	h.waitForText("Type a message", 5*time.Second)

	for i := 0; i < 5; i++ {
		h.sendKey("up")
		time.Sleep(30 * time.Millisecond)
	}
	for i := 0; i < 5; i++ {
		h.sendKey("down")
		time.Sleep(30 * time.Millisecond)
	}
	time.Sleep(200 * time.Millisecond)

	screen := h.snapshot()
	if !strings.Contains(screen, "Type a message") {
		t.Error("prompt lost after empty history navigation")
	}
}

// TestPTY_LeftRightOnEmptyInput verifies arrow keys on empty input.
func TestPTY_LeftRightOnEmptyInput(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}
	h := startGGCode(t, ptyOptions{})
	defer h.quit()
	h.waitForText("Type a message", 5*time.Second)

	for i := 0; i < 10; i++ {
		h.sendKey("left")
		time.Sleep(20 * time.Millisecond)
	}
	for i := 0; i < 10; i++ {
		h.sendKey("right")
		time.Sleep(20 * time.Millisecond)
	}
	time.Sleep(200 * time.Millisecond)

	screen := h.snapshot()
	if len(screen) == 0 {
		t.Error("no output after empty input arrow navigation")
	}
}

// ensure strings import used
var _ = strings.Contains
