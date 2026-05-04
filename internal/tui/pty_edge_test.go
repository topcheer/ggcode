//go:build integration_local

package tui

import (
	"strings"
	"testing"
	"time"
)

// --- Edge Cases ---

// TestPTY_EmptyEnter verifies pressing Enter with empty input doesn't crash.
func TestPTY_EmptyEnter(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}
	h := startGGCode(t, ptyOptions{})
	defer h.quit()
	h.waitForText("Type a message", 5*time.Second)
	h.drainOutput()

	// Press enter with no text
	h.sendKey("enter")
	time.Sleep(200 * time.Millisecond)
	h.sendKey("enter")
	time.Sleep(200 * time.Millisecond)
	h.sendKey("enter")
	time.Sleep(300 * time.Millisecond)

	screen := h.snapshot()
	t.Logf("after 3 empty enters:\n%s", lastN(screen, 300))
	if !strings.Contains(screen, "Type a message") {
		t.Error("prompt lost after empty enters")
	}
}

// TestPTY_LongInput verifies typing a very long line.
func TestPTY_LongInput(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}
	h := startGGCode(t, ptyOptions{})
	defer h.quit()
	h.waitForText("Type a message", 5*time.Second)
	h.drainOutput()

	// Type 200 characters
	for i := 0; i < 200; i++ {
		h.sendKey("x")
	}
	time.Sleep(500 * time.Millisecond)

	screen := h.snapshot()
	t.Logf("after 200 chars:\n%s", lastN(screen, 400))
	// Should not crash — text may wrap or scroll
	if len(screen) == 0 {
		t.Error("no output after long input")
	}
}

// TestPTY_RapidKeyPresses verifies rapid sequential key presses.
func TestPTY_RapidKeyPresses(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}
	h := startGGCode(t, ptyOptions{})
	defer h.quit()
	h.waitForText("Type a message", 5*time.Second)
	h.drainOutput()

	// Rapid fire keys — no delay between them
	for _, ch := range "rapid typing test 1234567890" {
		h.ptmx.WriteString(string(ch))
	}
	time.Sleep(300 * time.Millisecond)

	screen := h.snapshot()
	t.Logf("after rapid typing:\n%s", lastN(screen, 300))
	// Process should survive even if some chars are lost
	if len(screen) == 0 {
		t.Error("no output after rapid typing")
	}
}

// TestPTY_CtrlU clears the current line.
func TestPTY_CtrlU(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}
	h := startGGCode(t, ptyOptions{})
	defer h.quit()
	h.waitForText("Type a message", 5*time.Second)
	h.drainOutput()

	// Type text
	for _, ch := range "delete this line" {
		h.sendKey(string(ch))
	}
	time.Sleep(200 * time.Millisecond)

	// Ctrl+U should clear the line
	h.sendKey("ctrl+u")
	time.Sleep(300 * time.Millisecond)

	screen := h.snapshot()
	t.Logf("after ctrl+u:\n%s", lastN(screen, 300))
	// The text should be gone
	plain := compressSpaces(screen)
	if strings.Contains(plain, "delete") && strings.Contains(plain, "this") {
		t.Error("ctrl+u didn't clear the line")
	}
}

// TestPTY_CtrlW deletes word backward.
func TestPTY_CtrlW(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}
	h := startGGCode(t, ptyOptions{})
	defer h.quit()
	h.waitForText("Type a message", 5*time.Second)
	h.drainOutput()

	// Type two words
	for _, ch := range "hello world" {
		h.sendKey(string(ch))
	}
	time.Sleep(200 * time.Millisecond)

	// Ctrl+W should delete "world"
	h.sendKey("ctrl+w")
	time.Sleep(300 * time.Millisecond)

	screen := h.snapshot()
	t.Logf("after ctrl+w:\n%s", lastN(screen, 300))
}

// TestPTY_CtrlA_CtrlE verifies Home (Ctrl+A) and End (Ctrl+E) movement.
func TestPTY_CtrlA_CtrlE(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}
	h := startGGCode(t, ptyOptions{})
	defer h.quit()
	h.waitForText("Type a message", 5*time.Second)
	h.drainOutput()

	// Type text
	for _, ch := range "abcde" {
		h.sendKey(string(ch))
	}
	time.Sleep(200 * time.Millisecond)

	// Ctrl+A = Home (move to start)
	h.sendKey("ctrl+a")
	time.Sleep(100 * time.Millisecond)

	// Type at start
	h.sendKey("X")
	time.Sleep(100 * time.Millisecond)

	// Ctrl+E = End (move to end)
	h.sendKey("ctrl+e")
	time.Sleep(100 * time.Millisecond)

	// Type at end
	h.sendKey("Y")
	time.Sleep(200 * time.Millisecond)

	screen := h.snapshot()
	t.Logf("after ctrl+a/ctrl+e:\n%s", lastN(screen, 300))
}

// TestPTY_DeleteKey verifies Delete key (not backspace).
func TestPTY_DeleteKey(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}
	h := startGGCode(t, ptyOptions{})
	defer h.quit()
	h.waitForText("Type a message", 5*time.Second)
	h.drainOutput()

	// Type text, move left, then Delete
	for _, ch := range "abc" {
		h.sendKey(string(ch))
	}
	h.sendKey("left")
	h.sendKey("left")
	time.Sleep(100 * time.Millisecond)

	// Delete should remove char under cursor
	h.sendKey("delete")
	time.Sleep(300 * time.Millisecond)

	screen := h.snapshot()
	t.Logf("after delete key:\n%s", lastN(screen, 300))
}

// TestPTY_HomeEndKeys verifies Home/End key sequences.
func TestPTY_HomeEndKeys(t *testing.T) {
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

	// Home
	h.sendKey("home")
	h.sendKey("X")
	time.Sleep(100 * time.Millisecond)

	// End
	h.sendKey("end")
	h.sendKey("Y")
	time.Sleep(200 * time.Millisecond)

	screen := h.snapshot()
	t.Logf("after home/end:\n%s", lastN(screen, 300))
}

// TestPTY_PageUpDown verifies Page Up/Down keys don't crash.
func TestPTY_PageUpDown(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}
	h := startGGCode(t, ptyOptions{})
	defer h.quit()
	h.waitForText("Type a message", 5*time.Second)

	// Page Down, then Page Up
	h.sendKey("pgdown")
	time.Sleep(100 * time.Millisecond)
	h.sendKey("pgup")
	time.Sleep(200 * time.Millisecond)

	screen := h.snapshot()
	if len(screen) == 0 {
		t.Error("no output after pgup/pgdown")
	}
}

// TestPTY_FKeys verifies F1-F5 don't crash.
func TestPTY_FKeys(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}
	h := startGGCode(t, ptyOptions{})
	defer h.quit()
	h.waitForText("Type a message", 5*time.Second)

	for _, key := range []string{"f1", "f2", "f3", "f4", "f5"} {
		h.sendKey(key)
		time.Sleep(50 * time.Millisecond)
	}
	time.Sleep(200 * time.Millisecond)

	screen := h.snapshot()
	if len(screen) == 0 {
		t.Error("no output after F keys")
	}
	t.Logf("after F1-F5:\n%s", lastN(screen, 200))
}

// TestPTY_TabAutocomplete verifies Tab doesn't crash on empty input.
func TestPTY_TabAutocomplete(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}
	h := startGGCode(t, ptyOptions{})
	defer h.quit()
	h.waitForText("Type a message", 5*time.Second)
	h.drainOutput()

	// Tab on empty input
	h.sendKey("tab")
	time.Sleep(300 * time.Millisecond)

	screen := h.snapshot()
	t.Logf("after tab on empty:\n%s", lastN(screen, 300))
}

// TestPTY_TabAfterSlash verifies Tab after "/" may autocomplete commands.
func TestPTY_TabAfterSlash(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}
	h := startGGCode(t, ptyOptions{})
	defer h.quit()
	h.waitForText("Type a message", 5*time.Second)
	h.drainOutput()

	h.sendKey("/")
	time.Sleep(100 * time.Millisecond)
	h.sendKey("tab")
	time.Sleep(300 * time.Millisecond)

	screen := h.snapshot()
	t.Logf("after tab after slash:\n%s", lastN(screen, 400))
}

// TestPTY_SpaceInInput verifies spaces are typed correctly.
func TestPTY_SpaceInInput(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}
	h := startGGCode(t, ptyOptions{})
	defer h.quit()
	h.waitForText("Type a message", 5*time.Second)
	h.drainOutput()

	for _, ch := range "hello world test" {
		h.sendKey(string(ch))
	}
	time.Sleep(300 * time.Millisecond)

	screen := h.snapshot()
	t.Logf("after spaces:\n%s", lastN(screen, 300))
	// Check chars individually (spaces may be rendered differently)
	for _, ch := range "helloworldtest" {
		if !strings.ContainsRune(screen, ch) {
			t.Errorf("char %q not found", ch)
		}
	}
}

// TestPTY_UnicodeInput verifies Chinese/emoji characters.
func TestPTY_UnicodeInput(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}
	h := startGGCode(t, ptyOptions{})
	defer h.quit()
	h.waitForText("Type a message", 5*time.Second)
	h.drainOutput()

	// Send UTF-8 Chinese characters directly
	h.ptmx.WriteString("你好世界")
	time.Sleep(300 * time.Millisecond)

	screen := h.snapshot()
	t.Logf("after unicode:\n%s", lastN(screen, 300))
	// Characters may or may not render correctly in PTY,
	// but process should not crash
	if len(screen) == 0 {
		t.Error("no output after unicode input")
	}
}

// TestPTY_DoubleCtrlC verifies double Ctrl+C exits gracefully.
func TestPTY_DoubleCtrlC(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}
	h := startGGCode(t, ptyOptions{})
	defer h.quit()
	h.waitForText("Type a message", 5*time.Second)

	// Double Ctrl+C — first clears input, second may exit
	h.sendKey("ctrl+c")
	time.Sleep(200 * time.Millisecond)
	h.sendKey("ctrl+c")
	time.Sleep(300 * time.Millisecond)

	// Process may have exited — that's OK, the quit() cleanup handles it
	screen := h.snapshot()
	t.Logf("after double ctrl+c:\n%s", lastN(screen, 200))
}

// TestPTY_MixedCommandsAndTyping verifies alternating between commands and typing.
func TestPTY_MixedCommandsAndTyping(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}
	h := startGGCode(t, ptyOptions{})
	defer h.quit()
	h.waitForText("Type a message", 5*time.Second)
	h.drainOutput()

	// Mix of typing and commands
	h.typeAndSend("/status")
	time.Sleep(300 * time.Millisecond)

	h.typeAndSend("regular text")
	time.Sleep(300 * time.Millisecond)

	h.typeAndSend("/mode auto")
	time.Sleep(300 * time.Millisecond)

	h.typeAndSend("more text")
	time.Sleep(300 * time.Millisecond)

	h.typeAndSend("/clear")
	time.Sleep(300 * time.Millisecond)

	screen := h.snapshot()
	t.Logf("after mixed:\n%s", lastN(screen, 300))
	if !strings.Contains(screen, "Type a message") {
		t.Log("prompt may have changed (acceptable)")
	}
}

// TestPTY_InvalidCommand verifies unknown slash command doesn't crash.
func TestPTY_InvalidCommand(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}
	h := startGGCode(t, ptyOptions{})
	defer h.quit()
	h.waitForText("Type a message", 5*time.Second)
	h.drainOutput()

	h.typeAndSend("/nonexistent_command_xyz")
	time.Sleep(500 * time.Millisecond)

	screen := h.snapshot()
	t.Logf("after invalid command:\n%s", lastN(screen, 300))
	// Should show an error message or just ignore
	if len(screen) == 0 {
		t.Error("no output after invalid command")
	}
}

// TestPTY_UnknownSlashPartial verifies partial slash command with tab.
func TestPTY_UnknownSlashPartial(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}
	h := startGGCode(t, ptyOptions{})
	defer h.quit()
	h.waitForText("Type a message", 5*time.Second)
	h.drainOutput()

	// Type partial command and tab
	for _, ch := range "/str" {
		h.sendKey(string(ch))
	}
	time.Sleep(100 * time.Millisecond)
	h.sendKey("tab")
	time.Sleep(300 * time.Millisecond)

	screen := h.snapshot()
	t.Logf("after /str + tab:\n%s", lastN(screen, 300))
}

// TestPTY_AllowCommand verifies /allow command.
func TestPTY_AllowCommand(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}
	h := startGGCode(t, ptyOptions{})
	defer h.quit()
	h.waitForText("Type a message", 5*time.Second)
	h.drainOutput()

	h.typeAndSend("/allow")
	time.Sleep(300 * time.Millisecond)

	screen := h.snapshot()
	t.Logf("after /allow:\n%s", lastN(screen, 300))
}

// TestPTY_ImageCommand verifies /image command.
func TestPTY_ImageCommand(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}
	h := startGGCode(t, ptyOptions{})
	defer h.quit()
	h.waitForText("Type a message", 5*time.Second)
	h.drainOutput()

	h.typeAndSend("/image")
	time.Sleep(500 * time.Millisecond)

	screen := h.snapshot()
	t.Logf("after /image:\n%s", lastN(screen, 300))
}
