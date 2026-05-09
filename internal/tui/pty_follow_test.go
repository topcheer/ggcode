//go:build integration_local

package tui

import (
	"strings"
	"testing"
	"time"
)

// TestPTY_SubAgentFollowStrip verifies the follow strip appears after spawn_agent.
//
// User workflow: type a prompt → wait for LLM to call spawn_agent →
// observe follow strip with slot key "!" and "Esc close" hint.
func TestPTY_SubAgentFollowStrip(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}

	h := startGGCodeLive(t)
	defer h.quit()

	// Step 1: Wait for TUI ready
	h.waitForText("Type a message", 10*time.Second)
	h.drainOutput()
	time.Sleep(1 * time.Second)

	// Step 2: Type prompt character by character, like a real user
	prompt := "Use spawn_agent to create one sub-agent that lists all .go files in the current directory recursively. Return immediately after spawning."
	h.sendText(prompt)
	time.Sleep(500 * time.Millisecond)
	h.sendKey("enter")
	t.Log("[user] Prompt sent, waiting for LLM to respond...")

	// Step 3: Wait for the LLM to call spawn_agent (can take 10-30s)
	h.waitForText("spawn_agent", 120*time.Second)
	t.Log("[user] Saw spawn_agent in output. Waiting for agent to finish processing...")

	// Step 4: Wait for the agent to complete its turn (loading ends, input reappears)
	// The agent should return text after spawning, then stop.
	h.waitForText("Type a message", 60*time.Second)
	time.Sleep(2 * time.Second)
	h.drainOutput()

	// Step 5: Check screen — follow strip should show with "!" and "Esc"
	screen := h.snapshot()
	plain := compressSpaces(stripAnsi(screen))
	t.Logf("Screen after agent finished (last 1000 chars):\n%s", lastN(plain, 1000))

	hasSlot := strings.Contains(plain, "!")
	hasEsc := strings.Contains(plain, "Esc")

	if !hasSlot {
		t.Error("expected slot key '!' in follow strip after spawn_agent")
	}
	if !hasEsc {
		t.Error("expected 'Esc' hint in follow strip after spawn_agent")
	}
	if hasSlot && hasEsc {
		t.Log("[user] Follow strip visible with slot keys. Test passed.")
	}
}

// TestPTY_SubAgentFollowToggle verifies entering and exiting follow mode.
//
// User workflow: spawn a long-running sub-agent → press "!" to follow →
// verify "input paused" → press Esc → verify main view restored.
func TestPTY_SubAgentFollowToggle(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}

	h := startGGCodeLive(t)
	defer h.quit()

	// Step 1: Wait for TUI ready
	h.waitForText("Type a message", 10*time.Second)
	h.drainOutput()
	time.Sleep(1 * time.Second)

	// Step 2: Spawn a sub-agent with a longer task so it stays running
	prompt := "Use spawn_agent to create a sub-agent that reads every .go file in the current directory and writes a summary for each file. This will take a while. Return immediately after spawning, do NOT wait for the result."
	h.sendText(prompt)
	time.Sleep(500 * time.Millisecond)
	h.sendKey("enter")
	t.Log("[user] Prompt sent for long-running sub-agent...")

	// Step 3: Wait for spawn_agent call
	h.waitForText("spawn_agent", 120*time.Second)
	t.Log("[user] Saw spawn_agent. Waiting for agent turn to complete...")

	// Step 4: Wait for agent to finish its turn
	h.waitForText("Type a message", 60*time.Second)
	time.Sleep(2 * time.Second)
	h.drainOutput()

	// Step 5: Verify follow strip is visible
	screen := h.snapshot()
	plain := compressSpaces(stripAnsi(screen))
	if !strings.Contains(plain, "!") {
		t.Fatal("follow strip not visible — cannot proceed with toggle test")
	}
	t.Log("[user] Follow strip visible. Pressing '!' to enter follow mode...")

	// Step 6: Press "!" to enter follow mode (input must be empty — agent just finished)
	h.sendKey("!")
	time.Sleep(3 * time.Second)

	// Step 7: Verify follow mode — should show "input paused" or "Following"
	screen = h.snapshot()
	plain = compressSpaces(stripAnsi(screen))
	t.Logf("Screen after pressing '!' (follow on, last 1000 chars):\n%s", lastN(plain, 1000))

	if !strings.Contains(plain, "input paused") && !strings.Contains(plain, "Following") && !strings.Contains(plain, "following") {
		t.Error("expected 'input paused' or 'Following' when in follow mode")
	} else {
		t.Log("[user] Follow mode active — input paused, sub-agent output visible.")
	}

	// Step 8: Press Esc to exit follow mode
	t.Log("[user] Pressing Esc to exit follow mode...")
	h.sendKey("escape")
	time.Sleep(2 * time.Second)

	screen = h.snapshot()
	plain = compressSpaces(stripAnsi(screen))
	t.Logf("Screen after pressing Esc (follow off, last 1000 chars):\n%s", lastN(plain, 1000))

	if strings.Contains(plain, "input paused") {
		t.Error("'input paused' should disappear after pressing Esc")
	} else {
		t.Log("[user] Back to main view. Test passed.")
	}
}

// TestPTY_SubAgentFollowAutoReturn verifies auto-return when sub-agent completes.
//
// User workflow: spawn a quick sub-agent → follow it → wait →
// verify auto-return to main view with system message.
func TestPTY_SubAgentFollowAutoReturn(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}

	h := startGGCodeLive(t)
	defer h.quit()

	h.waitForText("Type a message", 10*time.Second)
	h.drainOutput()
	time.Sleep(1 * time.Second)

	// Spawn a quick sub-agent
	prompt := "Use spawn_agent to create a sub-agent that simply lists files in the current directory. It should finish very quickly. Return immediately after spawning."
	h.sendText(prompt)
	time.Sleep(500 * time.Millisecond)
	h.sendKey("enter")
	t.Log("[user] Prompt sent for quick sub-agent...")

	h.waitForText("spawn_agent", 120*time.Second)
	t.Log("[user] Saw spawn_agent. Waiting for agent turn to complete...")

	h.waitForText("Type a message", 60*time.Second)
	time.Sleep(2 * time.Second)
	h.drainOutput()

	// Verify follow strip, then enter follow mode
	screen := h.snapshot()
	plain := compressSpaces(stripAnsi(screen))
	if !strings.Contains(plain, "!") {
		t.Fatal("follow strip not visible — cannot proceed with auto-return test")
	}
	t.Log("[user] Follow strip visible. Pressing '!' to follow...")

	h.sendKey("!")
	time.Sleep(3 * time.Second)

	screen = h.snapshot()
	plain = compressSpaces(stripAnsi(screen))
	t.Logf("Screen in follow mode (last 1000 chars):\n%s", lastN(plain, 1000))

	// Now wait for auto-return — sub-agent completes → system message → main view
	t.Log("[user] Waiting for sub-agent to complete and auto-return...")
	for attempt := 0; attempt < 90; attempt++ {
		time.Sleep(3 * time.Second)
		screen = h.snapshot()
		plain = compressSpaces(stripAnsi(screen))

		// Auto-return indicators: "input paused" gone, or "returned to main view" appeared
		if strings.Contains(plain, "returned to main view") || strings.Contains(plain, "completed") {
			t.Logf("[user] Auto-return detected after %d seconds. Test passed.", (attempt+1)*3)
			return
		}
		if !strings.Contains(plain, "input paused") && !strings.Contains(plain, "Following sub-agent") {
			t.Logf("[user] Auto-return detected after %d seconds (input unpaused). Test passed.", (attempt+1)*3)
			return
		}
	}

	screen = h.snapshot()
	plain = compressSpaces(stripAnsi(screen))
	t.Logf("Final screen (last 1000 chars):\n%s", lastN(plain, 1000))
	t.Error("expected auto-return to main view after sub-agent completed")
}

// TestPTY_SubAgentFollowResize verifies follow mode renders correctly at different terminal sizes.
//
// User workflow: spawn sub-agent → follow it → resize to various sizes →
// verify no crash and content still renders.
func TestPTY_SubAgentFollowResize(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}

	h := startGGCodeLive(t)
	defer h.quit()

	h.waitForText("Type a message", 10*time.Second)
	h.drainOutput()
	time.Sleep(1 * time.Second)

	// Spawn a sub-agent
	prompt := "Use spawn_agent to create a sub-agent that lists all .go files in the current directory. Return immediately after spawning."
	h.sendText(prompt)
	time.Sleep(500 * time.Millisecond)
	h.sendKey("enter")
	t.Log("[user] Prompt sent...")

	h.waitForText("spawn_agent", 120*time.Second)
	h.waitForText("Type a message", 60*time.Second)
	time.Sleep(2 * time.Second)
	h.drainOutput()

	// Enter follow mode
	h.sendKey("!")
	time.Sleep(3 * time.Second)

	// Verify follow mode is active
	screen := h.snapshot()
	plain := compressSpaces(stripAnsi(screen))
	if !strings.Contains(plain, "input paused") && !strings.Contains(plain, "Following") {
		t.Fatal("expected follow mode to be active before resize test")
	}
	t.Log("[user] Follow mode active. Testing various terminal sizes...")

	// Test standard 80x24
	h.resize(80, 24)
	time.Sleep(2 * time.Second)
	screen = h.snapshot()
	t.Logf("Follow mode at 80x24 (last 500 chars):\n%s", lastN(stripAnsi(screen), 500))
	if strings.Contains(screen, "panic") || strings.Contains(screen, "runtime error") {
		t.Error("crash detected at 80x24")
	}

	// Test wide 200x40
	h.resize(200, 40)
	time.Sleep(2 * time.Second)
	screen = h.snapshot()
	t.Logf("Follow mode at 200x40 (last 500 chars):\n%s", lastN(stripAnsi(screen), 500))
	if strings.Contains(screen, "panic") || strings.Contains(screen, "runtime error") {
		t.Error("crash detected at 200x40")
	}

	// Test narrow 60x20
	h.resize(60, 20)
	time.Sleep(2 * time.Second)
	screen = h.snapshot()
	t.Logf("Follow mode at 60x20 (last 500 chars):\n%s", lastN(stripAnsi(screen), 500))
	if strings.Contains(screen, "panic") || strings.Contains(screen, "runtime error") {
		t.Error("crash detected at 60x20")
	}

	// Test tall 120x60
	h.resize(120, 60)
	time.Sleep(2 * time.Second)
	screen = h.snapshot()
	t.Logf("Follow mode at 120x60 (last 500 chars):\n%s", lastN(stripAnsi(screen), 500))
	if strings.Contains(screen, "panic") || strings.Contains(screen, "runtime error") {
		t.Error("crash detected at 120x60")
	}

	t.Log("[user] All sizes rendered without crash. Test passed.")

	// Exit follow mode
	h.sendKey("escape")
	time.Sleep(1 * time.Second)
}
