//go:build integration_local

package tui

import (
	"strings"
	"testing"
	"time"
)

func TestPTY_CtrlK(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}
	h := startGGCode(t, ptyOptions{})
	defer h.quit()
	h.waitForText("Type a message", 5*time.Second)
	h.drainOutput()

	for _, ch := range "abcdefghij" {
		h.sendKey(string(ch))
	}
	time.Sleep(100 * time.Millisecond)

	for i := 0; i < 4; i++ {
		h.sendKey("left")
	}
	time.Sleep(100 * time.Millisecond)

	h.sendKey("ctrl+k")
	time.Sleep(300 * time.Millisecond)

	screen := h.snapshot()
	t.Logf("after ctrl+k:\n%s", lastN(screen, 300))
}

func TestPTY_CtrlP_CtrlN(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}
	h := startGGCode(t, ptyOptions{})
	defer h.quit()
	h.waitForText("Type a message", 5*time.Second)
	h.drainOutput()

	h.typeAndSend("first msg")
	time.Sleep(300 * time.Millisecond)

	h.sendKey("ctrl+p")
	time.Sleep(200 * time.Millisecond)

	screen := h.snapshot()
	t.Logf("after ctrl+p:\n%s", lastN(screen, 300))

	h.sendKey("ctrl+n")
	time.Sleep(200 * time.Millisecond)

	screen = h.snapshot()
	t.Logf("after ctrl+n:\n%s", lastN(screen, 300))
}

func TestPTY_CtrlR(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}
	h := startGGCode(t, ptyOptions{})
	defer h.quit()
	h.waitForText("Type a message", 5*time.Second)

	h.sendKey("ctrl+r")
	time.Sleep(200 * time.Millisecond)

	for _, ch := range "test" {
		h.sendKey(string(ch))
	}
	time.Sleep(200 * time.Millisecond)

	screen := h.snapshot()
	t.Logf("after ctrl+r search:\n%s", lastN(screen, 300))

	h.sendKey("escape")
	time.Sleep(100 * time.Millisecond)
}

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

func TestPTY_RapidResizeStorm(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}
	h := startGGCode(t, ptyOptions{Cols: 120, Rows: 40})
	defer h.quit()
	h.waitForText("Type a message", 5*time.Second)

	for i := 0; i < 30; i++ {
		cols := uint16(40 + (i*7)%160)
		rows := uint16(10 + (i*3)%50)
		h.resize(cols, rows)
	}
	time.Sleep(500 * time.Millisecond)

	h.resize(120, 40)
	time.Sleep(300 * time.Millisecond)

	screen := h.snapshot()
	if len(screen) == 0 {
		t.Error("no output after resize storm")
	}
	t.Logf("after resize storm:\n%s", lastN(screen, 200))
}

func TestPTY_IdleThenInteract(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}
	h := startGGCode(t, ptyOptions{})
	defer h.quit()
	h.waitForText("Type a message", 5*time.Second)

	time.Sleep(2 * time.Second)

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

func TestPTY_TypeWhileStreaming(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}
	h := startGGCode(t, ptyOptions{})
	defer h.quit()
	h.waitForText("Type a message", 5*time.Second)
	h.drainOutput()

	h.typeAndSend("test query")
	time.Sleep(300 * time.Millisecond)

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

func TestPTY_OpenCloseReopenPanel(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}
	h := startGGCode(t, ptyOptions{})
	defer h.quit()
	h.waitForText("Type a message", 5*time.Second)
	h.drainOutput()

	for i := 0; i < 3; i++ {

		h.typeAndSend("/skills")
		time.Sleep(500 * time.Millisecond)

		screen := h.snapshot()
		if !strings.Contains(screen, "skill") && !strings.Contains(screen, "Skill") {
			t.Logf("cycle %d: panel didn't open", i)
		}

		h.sendKey("escape")
		time.Sleep(300 * time.Millisecond)
	}

	screen := h.snapshot()
	if !strings.Contains(screen, "Type a message") {
		t.Error("prompt lost after open/close cycle")
	}
}

func TestPTY_CommandAfterPanelClose(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}
	h := startGGCode(t, ptyOptions{})
	defer h.quit()
	h.waitForText("Type a message", 5*time.Second)
	h.drainOutput()

	h.typeAndSend("/skills")
	time.Sleep(500 * time.Millisecond)

	h.sendKey("escape")
	time.Sleep(300 * time.Millisecond)

	h.typeAndSend("/status")
	time.Sleep(500 * time.Millisecond)

	screen := h.snapshot()
	t.Logf("command after panel close:\n%s", lastN(screen, 300))
}

func TestPTY_SwitchPanels(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}
	h := startGGCode(t, ptyOptions{})
	defer h.quit()
	h.waitForText("Type a message", 5*time.Second)
	h.drainOutput()

	h.typeAndSend("/skills")
	time.Sleep(400 * time.Millisecond)
	h.sendKey("escape")
	time.Sleep(200 * time.Millisecond)

	h.typeAndSend("/im")
	time.Sleep(400 * time.Millisecond)
	h.sendKey("escape")
	time.Sleep(200 * time.Millisecond)

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

func TestPTY_ResizePanelOpenClose(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}
	h := startGGCode(t, ptyOptions{Cols: 120, Rows: 40})
	defer h.quit()
	h.waitForText("Type a message", 5*time.Second)
	h.drainOutput()

	h.typeAndSend("/skills")
	time.Sleep(400 * time.Millisecond)

	h.resize(80, 24)
	time.Sleep(200 * time.Millisecond)

	h.sendKey("escape")
	time.Sleep(200 * time.Millisecond)

	h.resize(120, 40)
	time.Sleep(200 * time.Millisecond)

	screen := h.snapshot()
	if !strings.Contains(screen, "Type a message") {
		t.Error("prompt lost after resize+panel cycle")
	}
}

func TestPTY_InputAccumulation(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}
	h := startGGCode(t, ptyOptions{})
	defer h.quit()
	h.waitForText("Type a message", 5*time.Second)
	h.drainOutput()

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

	for _, ch := range "part123" {
		if !strings.ContainsRune(screen, ch) {
			t.Errorf("char %q missing from accumulated input", ch)
		}
	}
}

func TestPTY_BackspacePastInput(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}
	h := startGGCode(t, ptyOptions{})
	defer h.quit()
	h.waitForText("Type a message", 5*time.Second)
	h.drainOutput()

	h.sendKey("a")
	h.sendKey("b")
	time.Sleep(100 * time.Millisecond)

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

var _ = strings.Contains
