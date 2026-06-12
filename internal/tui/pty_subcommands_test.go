//go:build integration_local

package tui

import (
	"testing"
	"time"
)

// --- Harness: Task lifecycle subcommands ---
// These all require existing tasks but should handle empty state gracefully.

func TestPTY_SlashHarnessRun(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping")
	}
	h := startGGCode(t, ptyOptions{})
	defer h.quit()
	h.waitForText("Type a message", 5*time.Second)
	h.drainOutput()
	h.typeAndSend("/harness run")
	time.Sleep(500 * time.Millisecond)
	h.snapshot()
}

func TestPTY_SlashHarnessPromote(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping")
	}
	h := startGGCode(t, ptyOptions{})
	defer h.quit()
	h.waitForText("Type a message", 5*time.Second)
	h.drainOutput()
	h.typeAndSend("/harness promote")
	time.Sleep(500 * time.Millisecond)
	h.snapshot()
}

func TestPTY_SlashHarnessRelease(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping")
	}
	h := startGGCode(t, ptyOptions{})
	defer h.quit()
	h.waitForText("Type a message", 5*time.Second)
	h.drainOutput()
	h.typeAndSend("/harness release")
	time.Sleep(500 * time.Millisecond)
	h.snapshot()
}

func TestPTY_SlashHarnessWaves(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping")
	}
	h := startGGCode(t, ptyOptions{})
	defer h.quit()
	h.waitForText("Type a message", 5*time.Second)
	h.drainOutput()
	h.typeAndSend("/harness release waves")
	time.Sleep(500 * time.Millisecond)
	h.snapshot()
}

func TestPTY_SlashHarnessApply(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping")
	}
	h := startGGCode(t, ptyOptions{})
	defer h.quit()
	h.waitForText("Type a message", 5*time.Second)
	h.drainOutput()
	h.typeAndSend("/harness release apply")
	time.Sleep(500 * time.Millisecond)
	h.snapshot()
}

func TestPTY_SlashHarnessRollouts(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping")
	}
	h := startGGCode(t, ptyOptions{})
	defer h.quit()
	h.waitForText("Type a message", 5*time.Second)
	h.drainOutput()
	h.typeAndSend("/harness release rollouts")
	time.Sleep(500 * time.Millisecond)
	h.snapshot()
}

func TestPTY_SlashHarnessAdvance(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping")
	}
	h := startGGCode(t, ptyOptions{})
	defer h.quit()
	h.waitForText("Type a message", 5*time.Second)
	h.drainOutput()
	h.typeAndSend("/harness release advance")
	time.Sleep(500 * time.Millisecond)
	h.snapshot()
}

func TestPTY_SlashHarnessPause(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping")
	}
	h := startGGCode(t, ptyOptions{})
	defer h.quit()
	h.waitForText("Type a message", 5*time.Second)
	h.drainOutput()
	h.typeAndSend("/harness release pause")
	time.Sleep(500 * time.Millisecond)
	h.snapshot()
}

func TestPTY_SlashHarnessResume(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping")
	}
	h := startGGCode(t, ptyOptions{})
	defer h.quit()
	h.waitForText("Type a message", 5*time.Second)
	h.drainOutput()
	h.typeAndSend("/harness release resume")
	time.Sleep(500 * time.Millisecond)
	h.snapshot()
}

func TestPTY_SlashHarnessAbort(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping")
	}
	h := startGGCode(t, ptyOptions{})
	defer h.quit()
	h.waitForText("Type a message", 5*time.Second)
	h.drainOutput()
	h.typeAndSend("/harness release abort")
	time.Sleep(500 * time.Millisecond)
	h.snapshot()
}

func TestPTY_SlashHarnessQueue(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping")
	}
	h := startGGCode(t, ptyOptions{})
	defer h.quit()
	h.waitForText("Type a message", 5*time.Second)
	h.drainOutput()
	h.typeAndSend("/harness queue")
	time.Sleep(500 * time.Millisecond)
	h.snapshot()
}

func TestPTY_SlashHarnessTasks(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping")
	}
	h := startGGCode(t, ptyOptions{})
	defer h.quit()
	h.waitForText("Type a message", 5*time.Second)
	h.drainOutput()
	h.typeAndSend("/harness tasks")
	time.Sleep(500 * time.Millisecond)
	h.snapshot()
}

// --- Slash: review subcommands ---

func TestPTY_SlashHarnessReviewApprove(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping")
	}
	h := startGGCode(t, ptyOptions{})
	defer h.quit()
	h.waitForText("Type a message", 5*time.Second)
	h.drainOutput()
	h.typeAndSend("/harness review approve")
	time.Sleep(500 * time.Millisecond)
	h.snapshot()
}

func TestPTY_SlashHarnessReviewReject(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping")
	}
	h := startGGCode(t, ptyOptions{})
	defer h.quit()
	h.waitForText("Type a message", 5*time.Second)
	h.drainOutput()
	h.typeAndSend("/harness review reject")
	time.Sleep(500 * time.Millisecond)
	h.snapshot()
}

func TestPTY_SlashHarnessRunQueued(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping")
	}
	h := startGGCode(t, ptyOptions{})
	defer h.quit()
	h.waitForText("Type a message", 5*time.Second)
	h.drainOutput()
	h.typeAndSend("/harness run-queued")
	time.Sleep(500 * time.Millisecond)
	h.snapshot()
}

// --- Slash subcommands from commands_slash.go ---

func TestPTY_SlashMemoryAudit(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping")
	}
	h := startGGCode(t, ptyOptions{})
	defer h.quit()
	h.waitForText("Type a message", 5*time.Second)
	h.drainOutput()
	h.typeAndSend("/memory audit")
	time.Sleep(500 * time.Millisecond)
	h.snapshot()
}

func TestPTY_SlashTodoClear(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping")
	}
	h := startGGCode(t, ptyOptions{})
	defer h.quit()
	h.waitForText("Type a message", 5*time.Second)
	h.drainOutput()
	h.typeAndSend("/todo clear")
	time.Sleep(500 * time.Millisecond)
	h.snapshot()
}

func TestPTY_SlashConfigAPIKey(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping")
	}
	h := startGGCode(t, ptyOptions{})
	defer h.quit()
	h.waitForText("Type a message", 5*time.Second)
	h.drainOutput()
	h.typeAndSend("/config apikey")
	time.Sleep(500 * time.Millisecond)
	h.snapshot()
}

func TestPTY_SlashConfigLanguage(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping")
	}
	h := startGGCode(t, ptyOptions{})
	defer h.quit()
	h.waitForText("Type a message", 5*time.Second)
	h.drainOutput()
	h.typeAndSend("/config language")
	time.Sleep(500 * time.Millisecond)
	h.snapshot()
}

func TestPTY_SlashConfigEndpoint(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping")
	}
	h := startGGCode(t, ptyOptions{})
	defer h.quit()
	h.waitForText("Type a message", 5*time.Second)
	h.drainOutput()
	h.typeAndSend("/config endpoint")
	time.Sleep(500 * time.Millisecond)
	h.snapshot()
}

func TestPTY_SlashModelWithArg(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping")
	}
	h := startGGCode(t, ptyOptions{})
	defer h.quit()
	h.waitForText("Type a message", 5*time.Second)
	h.drainOutput()
	h.typeAndSend("/model list")
	time.Sleep(500 * time.Millisecond)
	h.snapshot()
}

func TestPTY_SlashKnightStatus(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping")
	}
	h := startGGCode(t, ptyOptions{})
	defer h.quit()
	h.waitForText("Type a message", 5*time.Second)
	h.drainOutput()
	h.typeAndSend("/knight status")
	time.Sleep(500 * time.Millisecond)
	h.snapshot()
}

func TestPTY_SlashKnightQueue(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping")
	}
	h := startGGCode(t, ptyOptions{})
	defer h.quit()
	h.waitForText("Type a message", 5*time.Second)
	h.drainOutput()
	h.typeAndSend("/knight queue")
	time.Sleep(500 * time.Millisecond)
	h.snapshot()
}

func TestPTY_SlashKnightPropose(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping")
	}
	h := startGGCode(t, ptyOptions{})
	defer h.quit()
	h.waitForText("Type a message", 5*time.Second)
	h.drainOutput()
	h.typeAndSend("/knight propose")
	time.Sleep(500 * time.Millisecond)
	h.snapshot()
}

func TestPTY_SlashKnightProposals(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping")
	}
	h := startGGCode(t, ptyOptions{})
	defer h.quit()
	h.waitForText("Type a message", 5*time.Second)
	h.drainOutput()
	h.typeAndSend("/knight proposals")
	time.Sleep(500 * time.Millisecond)
	h.snapshot()
}

func TestPTY_SlashKnightPolicies(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping")
	}
	h := startGGCode(t, ptyOptions{})
	defer h.quit()
	h.waitForText("Type a message", 5*time.Second)
	h.drainOutput()
	h.typeAndSend("/knight policies")
	time.Sleep(500 * time.Millisecond)
	h.snapshot()
}

func TestPTY_SlashKnightReview(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping")
	}
	h := startGGCode(t, ptyOptions{})
	defer h.quit()
	h.waitForText("Type a message", 5*time.Second)
	h.drainOutput()
	h.typeAndSend("/knight review")
	time.Sleep(500 * time.Millisecond)
	h.snapshot()
}

func TestPTY_SlashKnightRun(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping")
	}
	h := startGGCode(t, ptyOptions{})
	defer h.quit()
	h.waitForText("Type a message", 5*time.Second)
	h.drainOutput()
	h.typeAndSend("/knight run")
	time.Sleep(500 * time.Millisecond)
	h.snapshot()
}

func TestPTY_SlashKnightBudget(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping")
	}
	h := startGGCode(t, ptyOptions{})
	defer h.quit()
	h.waitForText("Type a message", 5*time.Second)
	h.drainOutput()
	h.typeAndSend("/knight budget")
	time.Sleep(500 * time.Millisecond)
	h.snapshot()
}

func TestPTY_SlashKnightSkills(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping")
	}
	h := startGGCode(t, ptyOptions{})
	defer h.quit()
	h.waitForText("Type a message", 5*time.Second)
	h.drainOutput()
	h.typeAndSend("/knight skills")
	time.Sleep(500 * time.Millisecond)
	h.snapshot()
}

func TestPTY_SlashKnightRate(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping")
	}
	h := startGGCode(t, ptyOptions{})
	defer h.quit()
	h.waitForText("Type a message", 5*time.Second)
	h.drainOutput()
	h.typeAndSend("/knight rate")
	time.Sleep(500 * time.Millisecond)
	h.snapshot()
}

func TestPTY_SlashKnightReflect(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping")
	}
	h := startGGCode(t, ptyOptions{})
	defer h.quit()
	h.waitForText("Type a message", 5*time.Second)
	h.drainOutput()
	h.typeAndSend("/knight reflect")
	time.Sleep(500 * time.Millisecond)
	h.snapshot()
}

func TestPTY_SlashKnightScenarios(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping")
	}
	h := startGGCode(t, ptyOptions{})
	defer h.quit()
	h.waitForText("Type a message", 5*time.Second)
	h.drainOutput()
	h.typeAndSend("/knight scenarios")
	time.Sleep(500 * time.Millisecond)
	h.snapshot()
}
