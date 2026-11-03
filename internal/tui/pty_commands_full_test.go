//go:build integration_local

package tui

import (
	"testing"
	"time"
)

func TestPTY_SlashConfig(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}
	h := startGGCode(t, ptyOptions{})
	defer h.quit()
	h.waitForText("Type a message", 5*time.Second)
	h.drainOutput()

	h.typeAndSend("/config")
	time.Sleep(500 * time.Millisecond)

	screen := h.snapshot()
	t.Logf("after /config:\n%s", lastN(screen, 400))
}

func TestPTY_SlashStatus(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}
	h := startGGCode(t, ptyOptions{})
	defer h.quit()
	h.waitForText("Type a message", 5*time.Second)
	h.drainOutput()

	h.typeAndSend("/status")
	time.Sleep(500 * time.Millisecond)

	screen := h.snapshot()
	t.Logf("after /status:\n%s", lastN(screen, 400))
}

func TestPTY_SlashClear(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}
	h := startGGCode(t, ptyOptions{})
	defer h.quit()
	h.waitForText("Type a message", 5*time.Second)
	h.drainOutput()

	h.typeAndSend("test message")
	time.Sleep(500 * time.Millisecond)

	h.typeAndSend("/clear")
	time.Sleep(500 * time.Millisecond)

	screen := h.snapshot()
	t.Logf("after /clear:\n%s", lastN(screen, 300))
}

func TestPTY_SlashCompact(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}
	h := startGGCode(t, ptyOptions{})
	defer h.quit()
	h.waitForText("Type a message", 5*time.Second)
	h.drainOutput()

	h.typeAndSend("/compact")
	time.Sleep(500 * time.Millisecond)

	screen := h.snapshot()
	t.Logf("after /compact:\n%s", lastN(screen, 300))
}

func TestPTY_SlashTodo(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}
	h := startGGCode(t, ptyOptions{})
	defer h.quit()
	h.waitForText("Type a message", 5*time.Second)
	h.drainOutput()

	h.typeAndSend("/todo")
	time.Sleep(500 * time.Millisecond)

	screen := h.snapshot()
	t.Logf("after /todo:\n%s", lastN(screen, 400))
}

func TestPTY_SlashBug(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}
	h := startGGCode(t, ptyOptions{})
	defer h.quit()
	h.waitForText("Type a message", 5*time.Second)
	h.drainOutput()

	h.typeAndSend("/bug")
	time.Sleep(500 * time.Millisecond)

	screen := h.snapshot()
	t.Logf("after /bug:\n%s", lastN(screen, 300))
}

func TestPTY_SlashMemory(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}
	h := startGGCode(t, ptyOptions{})
	defer h.quit()
	h.waitForText("Type a message", 5*time.Second)
	h.drainOutput()

	h.typeAndSend("/memory")
	time.Sleep(500 * time.Millisecond)

	screen := h.snapshot()
	t.Logf("after /memory:\n%s", lastN(screen, 400))
}

func TestPTY_SlashModel(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}
	h := startGGCode(t, ptyOptions{})
	defer h.quit()
	h.waitForText("Type a message", 5*time.Second)
	h.drainOutput()

	h.typeAndSend("/model")
	time.Sleep(500 * time.Millisecond)

	screen := h.snapshot()
	t.Logf("after /model:\n%s", lastN(screen, 400))
}

func TestPTY_SlashProvider(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}
	h := startGGCode(t, ptyOptions{})
	defer h.quit()
	h.waitForText("Type a message", 5*time.Second)
	h.drainOutput()

	h.typeAndSend("/provider")
	time.Sleep(500 * time.Millisecond)

	screen := h.snapshot()
	t.Logf("after /provider:\n%s", lastN(screen, 400))
}

func TestPTY_SlashCheckpoints(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}
	h := startGGCode(t, ptyOptions{})
	defer h.quit()
	h.waitForText("Type a message", 5*time.Second)
	h.drainOutput()

	h.typeAndSend("/checkpoints")
	time.Sleep(500 * time.Millisecond)

	screen := h.snapshot()
	t.Logf("after /checkpoints:\n%s", lastN(screen, 400))
}

func TestPTY_SlashTelegram(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}
	h := startGGCode(t, ptyOptions{})
	defer h.quit()
	h.waitForText("Type a message", 5*time.Second)
	h.drainOutput()

	h.typeAndSend("/telegram")
	time.Sleep(500 * time.Millisecond)

	screen := h.snapshot()
	t.Logf("after /telegram:\n%s", lastN(screen, 400))
}

func TestPTY_SlashDiscord(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}
	h := startGGCode(t, ptyOptions{})
	defer h.quit()
	h.waitForText("Type a message", 5*time.Second)
	h.drainOutput()

	h.typeAndSend("/discord")
	time.Sleep(500 * time.Millisecond)

	screen := h.snapshot()
	t.Logf("after /discord:\n%s", lastN(screen, 400))
}

func TestPTY_SlashDingtalk(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}
	h := startGGCode(t, ptyOptions{})
	defer h.quit()
	h.waitForText("Type a message", 5*time.Second)
	h.drainOutput()

	h.typeAndSend("/dingtalk")
	time.Sleep(500 * time.Millisecond)

	screen := h.snapshot()
	t.Logf("after /dingtalk:\n%s", lastN(screen, 400))
}

func TestPTY_SlashFeishu(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}
	h := startGGCode(t, ptyOptions{})
	defer h.quit()
	h.waitForText("Type a message", 5*time.Second)
	h.drainOutput()

	h.typeAndSend("/feishu")
	time.Sleep(500 * time.Millisecond)

	screen := h.snapshot()
	t.Logf("after /feishu:\n%s", lastN(screen, 400))
}

func TestPTY_SlashSlack(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}
	h := startGGCode(t, ptyOptions{})
	defer h.quit()
	h.waitForText("Type a message", 5*time.Second)
	h.drainOutput()

	h.typeAndSend("/slack")
	time.Sleep(500 * time.Millisecond)

	screen := h.snapshot()
	t.Logf("after /slack:\n%s", lastN(screen, 400))
}

func TestPTY_SlashWechat(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}
	h := startGGCode(t, ptyOptions{})
	defer h.quit()
	h.waitForText("Type a message", 5*time.Second)
	h.drainOutput()

	h.typeAndSend("/wechat")
	time.Sleep(500 * time.Millisecond)

	screen := h.snapshot()
	t.Logf("after /wechat:\n%s", lastN(screen, 400))
}

func TestPTY_SlashQQ(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}
	h := startGGCode(t, ptyOptions{})
	defer h.quit()
	h.waitForText("Type a message", 5*time.Second)
	h.drainOutput()

	h.typeAndSend("/qq")
	time.Sleep(500 * time.Millisecond)

	screen := h.snapshot()
	t.Logf("after /qq:\n%s", lastN(screen, 400))
}

func TestPTY_SlashHarnessInit(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}
	h := startGGCode(t, ptyOptions{})
	defer h.quit()
	h.waitForText("Type a message", 5*time.Second)
	h.drainOutput()

	h.typeAndSend("/harness init")
	time.Sleep(500 * time.Millisecond)

	screen := h.snapshot()
	t.Logf("after /harness init:\n%s", lastN(screen, 400))
}

func TestPTY_SlashHarnessDoctor(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}
	h := startGGCode(t, ptyOptions{})
	defer h.quit()
	h.waitForText("Type a message", 5*time.Second)
	h.drainOutput()

	h.typeAndSend("/harness doctor")
	time.Sleep(500 * time.Millisecond)

	screen := h.snapshot()
	t.Logf("after /harness doctor:\n%s", lastN(screen, 400))
}

func TestPTY_SlashHarnessPanel(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}
	h := startGGCode(t, ptyOptions{})
	defer h.quit()
	h.waitForText("Type a message", 5*time.Second)
	h.drainOutput()

	h.typeAndSend("/harness panel")
	time.Sleep(500 * time.Millisecond)

	screen := h.snapshot()
	t.Logf("after /harness panel:\n%s", lastN(screen, 500))
}

func TestPTY_SlashKnight(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}
	h := startGGCode(t, ptyOptions{})
	defer h.quit()
	h.waitForText("Type a message", 5*time.Second)
	h.drainOutput()

	h.typeAndSend("/knight")
	time.Sleep(500 * time.Millisecond)

	screen := h.snapshot()
	t.Logf("after /knight:\n%s", lastN(screen, 400))
}

func (h *ptyHarness) typeAndSend(text string) {
	for _, ch := range text {
		h.sendKey(string(ch))
	}
	time.Sleep(100 * time.Millisecond)
	h.sendKey("enter")
}
