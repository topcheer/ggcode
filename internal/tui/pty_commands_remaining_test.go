//go:build integration_local

package tui

import (
	"testing"
	"time"
)

// --- Remaining Slash Commands ---

// TestPTY_SlashExit verifies /exit command exits gracefully.
func TestPTY_SlashExit(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}
	h := startGGCode(t, ptyOptions{})
	h.waitForText("Type a message", 5*time.Second)
	h.drainOutput()

	h.typeAndSend("/exit")
	time.Sleep(1 * time.Second)

	// Process should have exited — just clean up
	if h.ptmx != nil {
		h.ptmx.Close()
		h.ptmx = nil
	}
	if h.cmd.Process != nil {
		h.cmd.Process.Kill()
		h.cmd.Wait()
	}
}

// TestPTY_SlashExport verifies /export command.
func TestPTY_SlashExport(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}
	h := startGGCode(t, ptyOptions{})
	defer h.quit()
	h.waitForText("Type a message", 5*time.Second)
	h.drainOutput()

	h.typeAndSend("/export")
	time.Sleep(500 * time.Millisecond)

	screen := h.snapshot()
	t.Logf("after /export:\n%s", lastN(screen, 400))
}

// TestPTY_SlashImpersonate verifies /impersonate command.
func TestPTY_SlashImpersonate(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}
	h := startGGCode(t, ptyOptions{})
	defer h.quit()
	h.waitForText("Type a message", 5*time.Second)
	h.drainOutput()

	h.typeAndSend("/impersonate")
	time.Sleep(500 * time.Millisecond)

	screen := h.snapshot()
	t.Logf("after /impersonate:\n%s", lastN(screen, 400))
}

// TestPTY_SlashInit verifies /init command.
func TestPTY_SlashInit(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}
	h := startGGCode(t, ptyOptions{})
	defer h.quit()
	h.waitForText("Type a message", 5*time.Second)
	h.drainOutput()

	h.typeAndSend("/init")
	time.Sleep(500 * time.Millisecond)

	screen := h.snapshot()
	t.Logf("after /init:\n%s", lastN(screen, 400))
}

// TestPTY_SlashPC verifies /pc (PushCan) command.
func TestPTY_SlashPC(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}
	h := startGGCode(t, ptyOptions{})
	defer h.quit()
	h.waitForText("Type a message", 5*time.Second)
	h.drainOutput()

	h.typeAndSend("/pc")
	time.Sleep(500 * time.Millisecond)

	screen := h.snapshot()
	t.Logf("after /pc:\n%s", lastN(screen, 400))
}

// TestPTY_SlashRestart verifies /restart command doesn't crash.
func TestPTY_SlashRestart(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}
	h := startGGCode(t, ptyOptions{})
	defer h.quit()
	h.waitForText("Type a message", 5*time.Second)
	h.drainOutput()

	h.typeAndSend("/restart")
	time.Sleep(500 * time.Millisecond)

	screen := h.snapshot()
	t.Logf("after /restart:\n%s", lastN(screen, 300))
}

// TestPTY_SlashResume verifies /resume command (no sessions).
func TestPTY_SlashResume(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}
	h := startGGCode(t, ptyOptions{})
	defer h.quit()
	h.waitForText("Type a message", 5*time.Second)
	h.drainOutput()

	h.typeAndSend("/resume")
	time.Sleep(500 * time.Millisecond)

	screen := h.snapshot()
	t.Logf("after /resume:\n%s", lastN(screen, 400))
}

// TestPTY_SlashUndo verifies /undo command.
func TestPTY_SlashUndo(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}
	h := startGGCode(t, ptyOptions{})
	defer h.quit()
	h.waitForText("Type a message", 5*time.Second)
	h.drainOutput()

	h.typeAndSend("/undo")
	time.Sleep(500 * time.Millisecond)

	screen := h.snapshot()
	t.Logf("after /undo:\n%s", lastN(screen, 300))
}

// TestPTY_SlashUpdate verifies /update command.
func TestPTY_SlashUpdate(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}
	h := startGGCode(t, ptyOptions{})
	defer h.quit()
	h.waitForText("Type a message", 5*time.Second)
	h.drainOutput()

	h.typeAndSend("/update")
	time.Sleep(500 * time.Millisecond)

	screen := h.snapshot()
	t.Logf("after /update:\n%s", lastN(screen, 400))
}

// --- Harness Subcommands ---

// TestPTY_SlashHarnessCheck verifies /harness check command.
func TestPTY_SlashHarnessCheck(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}
	h := startGGCode(t, ptyOptions{})
	defer h.quit()
	h.waitForText("Type a message", 5*time.Second)
	h.drainOutput()

	h.typeAndSend("/harness check")
	time.Sleep(500 * time.Millisecond)

	screen := h.snapshot()
	t.Logf("after /harness check:\n%s", lastN(screen, 400))
}

// TestPTY_SlashHarnessMonitor verifies /harness monitor command.
func TestPTY_SlashHarnessMonitor(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}
	h := startGGCode(t, ptyOptions{})
	defer h.quit()
	h.waitForText("Type a message", 5*time.Second)
	h.drainOutput()

	h.typeAndSend("/harness monitor")
	time.Sleep(500 * time.Millisecond)

	screen := h.snapshot()
	t.Logf("after /harness monitor:\n%s", lastN(screen, 400))
}

// TestPTY_SlashHarnessGC verifies /harness gc command.
func TestPTY_SlashHarnessGC(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}
	h := startGGCode(t, ptyOptions{})
	defer h.quit()
	h.waitForText("Type a message", 5*time.Second)
	h.drainOutput()

	h.typeAndSend("/harness gc")
	time.Sleep(500 * time.Millisecond)

	screen := h.snapshot()
	t.Logf("after /harness gc:\n%s", lastN(screen, 400))
}

// TestPTY_SlashHarnessContexts verifies /harness contexts command.
func TestPTY_SlashHarnessContexts(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}
	h := startGGCode(t, ptyOptions{})
	defer h.quit()
	h.waitForText("Type a message", 5*time.Second)
	h.drainOutput()

	h.typeAndSend("/harness contexts")
	time.Sleep(500 * time.Millisecond)

	screen := h.snapshot()
	t.Logf("after /harness contexts:\n%s", lastN(screen, 400))
}

// TestPTY_SlashHarnessInbox verifies /harness inbox command.
func TestPTY_SlashHarnessInbox(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}
	h := startGGCode(t, ptyOptions{})
	defer h.quit()
	h.waitForText("Type a message", 5*time.Second)
	h.drainOutput()

	h.typeAndSend("/harness inbox")
	time.Sleep(500 * time.Millisecond)

	screen := h.snapshot()
	t.Logf("after /harness inbox:\n%s", lastN(screen, 400))
}

// TestPTY_SlashHarnessReview verifies /harness review command.
func TestPTY_SlashHarnessReview(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}
	h := startGGCode(t, ptyOptions{})
	defer h.quit()
	h.waitForText("Type a message", 5*time.Second)
	h.drainOutput()

	h.typeAndSend("/harness review")
	time.Sleep(500 * time.Millisecond)

	screen := h.snapshot()
	t.Logf("after /harness review:\n%s", lastN(screen, 400))
}

// TestPTY_SlashHarnessAuto verifies /harness auto command.
func TestPTY_SlashHarnessAuto(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}
	h := startGGCode(t, ptyOptions{})
	defer h.quit()
	h.waitForText("Type a message", 5*time.Second)
	h.drainOutput()

	h.typeAndSend("/harness auto")
	time.Sleep(500 * time.Millisecond)

	screen := h.snapshot()
	t.Logf("after /harness auto:\n%s", lastN(screen, 400))
}

// --- Stream Subcommands ---

// TestPTY_SlashStreamHelp verifies /stream help command.
func TestPTY_SlashStreamHelp(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}
	h := startGGCode(t, ptyOptions{})
	defer h.quit()
	h.waitForText("Type a message", 5*time.Second)
	h.drainOutput()

	h.typeAndSend("/stream")
	time.Sleep(500 * time.Millisecond)

	screen := h.snapshot()
	t.Logf("after /stream (help):\n%s", lastN(screen, 400))
}

// --- Mode Variants ---

// TestPTY_SlashModeAuto verifies /mode auto.
func TestPTY_SlashModeAuto(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}
	h := startGGCode(t, ptyOptions{})
	defer h.quit()
	h.waitForText("Type a message", 5*time.Second)
	h.drainOutput()

	h.typeAndSend("/mode auto")
	time.Sleep(500 * time.Millisecond)

	screen := h.snapshot()
	t.Logf("after /mode auto:\n%s", lastN(screen, 300))
}

// TestPTY_SlashModeSupervised verifies /mode supervised.
func TestPTY_SlashModeSupervised(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}
	h := startGGCode(t, ptyOptions{})
	defer h.quit()
	h.waitForText("Type a message", 5*time.Second)
	h.drainOutput()

	h.typeAndSend("/mode supervised")
	time.Sleep(500 * time.Millisecond)

	screen := h.snapshot()
	t.Logf("after /mode supervised:\n%s", lastN(screen, 300))
}

// TestPTY_SlashModeBypass verifies /mode bypass.
func TestPTY_SlashModeBypass(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}
	h := startGGCode(t, ptyOptions{})
	defer h.quit()
	h.waitForText("Type a message", 5*time.Second)
	h.drainOutput()

	h.typeAndSend("/mode bypass")
	time.Sleep(500 * time.Millisecond)

	screen := h.snapshot()
	t.Logf("after /mode bypass:\n%s", lastN(screen, 300))
}

// --- Config Subcommands ---

// TestPTY_SlashConfigStatus verifies /config status.
func TestPTY_SlashConfigStatus(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}
	h := startGGCode(t, ptyOptions{})
	defer h.quit()
	h.waitForText("Type a message", 5*time.Second)
	h.drainOutput()

	h.typeAndSend("/config status")
	time.Sleep(500 * time.Millisecond)

	screen := h.snapshot()
	t.Logf("after /config status:\n%s", lastN(screen, 400))
}

// TestPTY_SlashConfigVendor verifies /config vendor.
func TestPTY_SlashConfigVendor(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}
	h := startGGCode(t, ptyOptions{})
	defer h.quit()
	h.waitForText("Type a message", 5*time.Second)
	h.drainOutput()

	h.typeAndSend("/config vendor")
	time.Sleep(500 * time.Millisecond)

	screen := h.snapshot()
	t.Logf("after /config vendor:\n%s", lastN(screen, 400))
}

// TestPTY_SlashConfigModel verifies /config model.
func TestPTY_SlashConfigModel(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}
	h := startGGCode(t, ptyOptions{})
	defer h.quit()
	h.waitForText("Type a message", 5*time.Second)
	h.drainOutput()

	h.typeAndSend("/config model")
	time.Sleep(500 * time.Millisecond)

	screen := h.snapshot()
	t.Logf("after /config model:\n%s", lastN(screen, 400))
}

// --- Memory Subcommands ---

// TestPTY_SlashMemoryList verifies /memory list.
func TestPTY_SlashMemoryList(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}
	h := startGGCode(t, ptyOptions{})
	defer h.quit()
	h.waitForText("Type a message", 5*time.Second)
	h.drainOutput()

	h.typeAndSend("/memory list")
	time.Sleep(500 * time.Millisecond)

	screen := h.snapshot()
	t.Logf("after /memory list:\n%s", lastN(screen, 400))
}

// TestPTY_SlashMemoryClear verifies /memory clear.
func TestPTY_SlashMemoryClear(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}
	h := startGGCode(t, ptyOptions{})
	defer h.quit()
	h.waitForText("Type a message", 5*time.Second)
	h.drainOutput()

	h.typeAndSend("/memory clear")
	time.Sleep(500 * time.Millisecond)

	screen := h.snapshot()
	t.Logf("after /memory clear:\n%s", lastN(screen, 300))
}

// --- Plugin Subcommands ---

// TestPTY_SlashPluginsList verifies /plugins list.
func TestPTY_SlashPluginsList(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}
	h := startGGCode(t, ptyOptions{})
	defer h.quit()
	h.waitForText("Type a message", 5*time.Second)
	h.drainOutput()

	h.typeAndSend("/plugins list")
	time.Sleep(500 * time.Millisecond)

	screen := h.snapshot()
	t.Logf("after /plugins list:\n%s", lastN(screen, 400))
}

// --- Alias Tests ---

// TestPTY_SlashHelpAlias verifies /? alias for /help.
func TestPTY_SlashHelpAlias(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}
	h := startGGCode(t, ptyOptions{})
	defer h.quit()
	h.waitForText("Type a message", 5*time.Second)
	h.drainOutput()

	h.typeAndSend("/?")
	time.Sleep(500 * time.Millisecond)

	screen := h.snapshot()
	t.Logf("after /?:\n%s", lastN(screen, 400))
}

// TestPTY_SlashTelegramAlias verifies /tg alias for /telegram.
func TestPTY_SlashTelegramAlias(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}
	h := startGGCode(t, ptyOptions{})
	defer h.quit()
	h.waitForText("Type a message", 5*time.Second)
	h.drainOutput()

	h.typeAndSend("/tg")
	time.Sleep(500 * time.Millisecond)

	screen := h.snapshot()
	t.Logf("after /tg:\n%s", lastN(screen, 400))
}

// TestPTY_SlashFeishuAlias verifies /lark alias for /feishu.
func TestPTY_SlashFeishuAlias(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}
	h := startGGCode(t, ptyOptions{})
	defer h.quit()
	h.waitForText("Type a message", 5*time.Second)
	h.drainOutput()

	h.typeAndSend("/lark")
	time.Sleep(500 * time.Millisecond)

	screen := h.snapshot()
	t.Logf("after /lark:\n%s", lastN(screen, 400))
}

// TestPTY_SlashDingtalkAlias verifies /ding alias for /dingtalk.
func TestPTY_SlashDingtalkAlias(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}
	h := startGGCode(t, ptyOptions{})
	defer h.quit()
	h.waitForText("Type a message", 5*time.Second)
	h.drainOutput()

	h.typeAndSend("/ding")
	time.Sleep(500 * time.Millisecond)

	screen := h.snapshot()
	t.Logf("after /ding:\n%s", lastN(screen, 400))
}

// --- helper ---

// typeAndSend is defined in pty_commands_full_test.go
