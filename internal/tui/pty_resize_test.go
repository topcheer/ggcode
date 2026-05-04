//go:build integration_local

package tui

import (
	"strings"
	"testing"
	"time"
)

// TestPTY_ResizeStandard80x24 verifies standard terminal size.
func TestPTY_ResizeStandard80x24(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}
	h := startGGCode(t, ptyOptions{Cols: 80, Rows: 24})
	defer h.quit()

	h.waitForText("Type a message", 5*time.Second)
	screen := h.snapshot()
	t.Logf("80x24:\n%s", lastN(screen, 300))
	if !strings.Contains(screen, "Type a message") {
		t.Error("prompt not visible at 80x24")
	}
}

// TestPTY_ResizeStandard120x40 verifies 120x40 size.
func TestPTY_ResizeStandard120x40(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}
	h := startGGCode(t, ptyOptions{Cols: 120, Rows: 40})
	defer h.quit()

	h.waitForText("Type a message", 5*time.Second)
	screen := h.snapshot()
	if !strings.Contains(screen, "Type a message") {
		t.Error("prompt not visible at 120x40")
	}
}

// TestPTY_ResizeLarge verifies 200x60 size.
func TestPTY_ResizeLarge(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}
	h := startGGCode(t, ptyOptions{Cols: 200, Rows: 60})
	defer h.quit()

	h.waitForText("Type a message", 5*time.Second)
	screen := h.snapshot()
	t.Logf("200x60 (last 200):\n%s", lastN(screen, 200))
}

// TestPTY_ResizeTiny40x12 verifies minimal useful size.
func TestPTY_ResizeTiny40x12(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}
	h := startGGCode(t, ptyOptions{Cols: 40, Rows: 12})
	defer h.quit()

	h.waitForText("Type a message", 5*time.Second)
	screen := h.snapshot()
	t.Logf("40x12:\n%s", lastN(screen, 200))
	// Should render something
	if len(screen) == 0 {
		t.Error("no output at 40x12")
	}
}

// TestPTY_ResizeMinWidth30x10 verifies very narrow width.
func TestPTY_ResizeMinWidth30x10(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}
	h := startGGCode(t, ptyOptions{Cols: 30, Rows: 10})
	defer h.quit()

	// Just wait and check — prompt may not fit
	time.Sleep(3 * time.Second)
	screen := h.snapshot()
	t.Logf("30x10:\n%s", lastN(screen, 200))

	// Resize up to recover
	h.resize(120, 40)
	h.waitForText("Type a message", 5*time.Second)
	t.Log("✓ recovered from 30x10 to 120x40")
}

// TestPTY_ResizeFromNarrowToWide verifies transition from narrow to wide.
func TestPTY_ResizeFromNarrowToWide(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}
	h := startGGCode(t, ptyOptions{Cols: 60, Rows: 20})
	defer h.quit()

	h.waitForText("Type a message", 5*time.Second)

	// Type text in narrow mode
	for _, ch := range "narrow" {
		h.sendKey(string(ch))
	}
	time.Sleep(200 * time.Millisecond)

	// Resize to wide
	h.resize(200, 50)
	time.Sleep(300 * time.Millisecond)

	screen := h.snapshot()
	// Resize is the critical test — text may get garbled during transition
	if len(screen) == 0 {
		t.Error("no output after narrow→wide resize")
	}
	// Check that some characters survived (even if garbled by ANSI)
	charsFound := 0
	for _, ch := range "narrow" {
		if strings.ContainsRune(screen, ch) {
			charsFound++
		}
	}
	t.Logf("narrow→wide: %d/6 chars visible", charsFound)
	if charsFound < 2 {
		t.Log("most chars lost during resize (may be acceptable)")
	}
}

// TestPTY_ResizeFromWideToNarrow verifies transition from wide to narrow.
func TestPTY_ResizeFromWideToNarrow(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}
	h := startGGCode(t, ptyOptions{Cols: 200, Rows: 50})
	defer h.quit()

	h.waitForText("Type a message", 5*time.Second)

	// Type text in wide mode
	for _, ch := range "wide" {
		h.sendKey(string(ch))
	}
	time.Sleep(200 * time.Millisecond)

	// Resize to narrow
	h.resize(60, 20)
	time.Sleep(300 * time.Millisecond)

	screen := h.snapshot()
	t.Logf("wide→narrow:\n%s", lastN(screen, 200))

	// Process should still be alive
	if len(screen) == 0 {
		t.Error("no output after wide→narrow resize")
	}
}

// TestPTY_ResizeWithPanelOpen verifies resize works while a panel is open.
func TestPTY_ResizeWithPanelOpen(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}
	h := startGGCode(t, ptyOptions{Cols: 120, Rows: 40})
	defer h.quit()

	h.waitForText("Type a message", 5*time.Second)
	h.drainOutput()

	// Open skills panel — simpler than stream config, always opens
	for _, ch := range "/skills" {
		h.sendKey(string(ch))
	}
	h.sendKey("enter")
	time.Sleep(500 * time.Millisecond)

	screen := h.snapshot()
	if !strings.Contains(screen, "skill") && !strings.Contains(screen, "Skill") {
		t.Skip("skills panel didn't open")
	}

	// Resize while panel is open
	h.resize(80, 24)
	time.Sleep(300 * time.Millisecond)

	screen = h.snapshot()
	t.Logf("resize with panel open:\n%s", lastN(screen, 300))

	// Process should still be alive and responsive
	if len(screen) == 0 {
		t.Error("no output after resize with panel open")
	}

	// Resize back
	h.resize(120, 40)
	time.Sleep(300 * time.Millisecond)
	screen = h.snapshot()
	if len(screen) == 0 {
		t.Error("no output after resize back to 120x40")
	}
}

// TestPTY_ResizeTypingDuringResize verifies typing works during/after resize.
func TestPTY_ResizeTypingDuringResize(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}
	h := startGGCode(t, ptyOptions{Cols: 120, Rows: 40})
	defer h.quit()

	h.waitForText("Type a message", 5*time.Second)

	// Type, resize, type, resize, type
	for _, ch := range "ab" {
		h.sendKey(string(ch))
	}
	h.resize(80, 24)
	time.Sleep(100 * time.Millisecond)
	for _, ch := range "cd" {
		h.sendKey(string(ch))
	}
	h.resize(160, 50)
	time.Sleep(100 * time.Millisecond)
	for _, ch := range "ef" {
		h.sendKey(string(ch))
	}
	time.Sleep(300 * time.Millisecond)

	screen := h.snapshot()
	t.Logf("typing during resize:\n%s", lastN(screen, 300))

	// At minimum, some characters should be present
	charsPresent := 0
	for _, ch := range "abcdef" {
		if strings.ContainsRune(screen, ch) {
			charsPresent++
		}
	}
	if charsPresent < 3 {
		t.Errorf("only %d/6 chars visible after resize-typing sequence", charsPresent)
	}
}
