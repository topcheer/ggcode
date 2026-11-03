//go:build integration_local

package tui

import (
	"strings"
	"testing"
	"time"
)

func TestPTY_EmptyEnter(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}
	h := startGGCode(t, ptyOptions{})
	defer h.quit()
	h.waitForText("Type a message", 5*time.Second)
	h.drainOutput()

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

func TestPTY_LongInput(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}
	h := startGGCode(t, ptyOptions{})
	defer h.quit()
	h.waitForText("Type a message", 5*time.Second)
	h.drainOutput()

	for i := 0; i < 200; i++ {
		h.sendKey("x")
	}
	time.Sleep(500 * time.Millisecond)

	screen := h.snapshot()
	t.Logf("after 200 chars:\n%s", lastN(screen, 400))

	if len(screen) == 0 {
		t.Error("no output after long input")
	}
}

func TestPTY_RapidKeyPresses(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}
	h := startGGCode(t, ptyOptions{})
	defer h.quit()
	h.waitForText("Type a message", 5*time.Second)
	h.drainOutput()

	for _, ch := range "rapid typing test 1234567890" {
		h.ptmx.WriteString(string(ch))
	}
	time.Sleep(300 * time.Millisecond)

	screen := h.snapshot()
	t.Logf("after rapid typing:\n%s", lastN(screen, 300))

	if len(screen) == 0 {
		t.Error("no output after rapid typing")
	}
}

func TestPTY_CtrlU(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}
	h := startGGCode(t, ptyOptions{})
	defer h.quit()
	h.waitForText("Type a message", 5*time.Second)
	h.drainOutput()

	for _, ch := range "delete this line" {
		h.sendKey(string(ch))
	}
	time.Sleep(200 * time.Millisecond)

	h.sendKey("ctrl+u")
	time.Sleep(300 * time.Millisecond)

	screen := h.snapshot()
	t.Logf("after ctrl+u:\n%s", lastN(screen, 300))

	plain := compressSpaces(screen)
	if strings.Contains(plain, "delete") && strings.Contains(plain, "this") {
		t.Error("ctrl+u didn't clear the line")
	}
}

func TestPTY_CtrlW(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}
	h := startGGCode(t, ptyOptions{})
	defer h.quit()
	h.waitForText("Type a message", 5*time.Second)
	h.drainOutput()

	for _, ch := range "hello world" {
		h.sendKey(string(ch))
	}
	time.Sleep(200 * time.Millisecond)

	h.sendKey("ctrl+w")
	time.Sleep(300 * time.Millisecond)

	screen := h.snapshot()
	t.Logf("after ctrl+w:\n%s", lastN(screen, 300))
}

func TestPTY_CtrlA_CtrlE(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}
	h := startGGCode(t, ptyOptions{})
	defer h.quit()
	h.waitForText("Type a message", 5*time.Second)
	h.drainOutput()

	for _, ch := range "abcde" {
		h.sendKey(string(ch))
	}
	time.Sleep(200 * time.Millisecond)

	h.sendKey("ctrl+a")
	time.Sleep(100 * time.Millisecond)

	h.sendKey("X")
	time.Sleep(100 * time.Millisecond)

	h.sendKey("ctrl+e")
	time.Sleep(100 * time.Millisecond)

	h.sendKey("Y")
	time.Sleep(200 * time.Millisecond)

	screen := h.snapshot()
	t.Logf("after ctrl+a/ctrl+e:\n%s", lastN(screen, 300))
}

func TestPTY_DeleteKey(t *testing.T) {
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
	h.sendKey("left")
	h.sendKey("left")
	time.Sleep(100 * time.Millisecond)

	h.sendKey("delete")
	time.Sleep(300 * time.Millisecond)

	screen := h.snapshot()
	t.Logf("after delete key:\n%s", lastN(screen, 300))
}

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

	h.sendKey("home")
	h.sendKey("X")
	time.Sleep(100 * time.Millisecond)

	h.sendKey("end")
	h.sendKey("Y")
	time.Sleep(200 * time.Millisecond)

	screen := h.snapshot()
	t.Logf("after home/end:\n%s", lastN(screen, 300))
}

func TestPTY_PageUpDown(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}
	h := startGGCode(t, ptyOptions{})
	defer h.quit()
	h.waitForText("Type a message", 5*time.Second)

	h.sendKey("pgdown")
	time.Sleep(100 * time.Millisecond)
	h.sendKey("pgup")
	time.Sleep(200 * time.Millisecond)

	screen := h.snapshot()
	if len(screen) == 0 {
		t.Error("no output after pgup/pgdown")
	}
}

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

func TestPTY_TabAutocomplete(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}
	h := startGGCode(t, ptyOptions{})
	defer h.quit()
	h.waitForText("Type a message", 5*time.Second)
	h.drainOutput()

	h.sendKey("tab")
	time.Sleep(300 * time.Millisecond)

	screen := h.snapshot()
	t.Logf("after tab on empty:\n%s", lastN(screen, 300))
}

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

	for _, ch := range "helloworldtest" {
		if !strings.ContainsRune(screen, ch) {
			t.Errorf("char %q not found", ch)
		}
	}
}

func TestPTY_UnicodeInput(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}
	h := startGGCode(t, ptyOptions{})
	defer h.quit()
	h.waitForText("Type a message", 5*time.Second)
	h.drainOutput()

	h.ptmx.WriteString("你好世界")
	time.Sleep(300 * time.Millisecond)

	screen := h.snapshot()
	t.Logf("after unicode:\n%s", lastN(screen, 300))

	if len(screen) == 0 {
		t.Error("no output after unicode input")
	}
}

func TestPTY_DoubleCtrlC(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}
	h := startGGCode(t, ptyOptions{})
	defer h.quit()
	h.waitForText("Type a message", 5*time.Second)

	h.sendKey("ctrl+c")
	time.Sleep(200 * time.Millisecond)
	h.sendKey("ctrl+c")
	time.Sleep(300 * time.Millisecond)

	screen := h.snapshot()
	t.Logf("after double ctrl+c:\n%s", lastN(screen, 200))
}

func TestPTY_MixedCommandsAndTyping(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}
	h := startGGCode(t, ptyOptions{})
	defer h.quit()
	h.waitForText("Type a message", 5*time.Second)
	h.drainOutput()

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

	if len(screen) == 0 {
		t.Error("no output after invalid command")
	}
}

func TestPTY_UnknownSlashPartial(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}
	h := startGGCode(t, ptyOptions{})
	defer h.quit()
	h.waitForText("Type a message", 5*time.Second)
	h.drainOutput()

	for _, ch := range "/str" {
		h.sendKey(string(ch))
	}
	time.Sleep(100 * time.Millisecond)
	h.sendKey("tab")
	time.Sleep(300 * time.Millisecond)

	screen := h.snapshot()
	t.Logf("after /str + tab:\n%s", lastN(screen, 300))
}

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
