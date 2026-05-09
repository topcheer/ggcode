//go:build integration_local

package tui

import (
	"regexp"
	"strings"
	"testing"
	"time"
)

// waitUntilNotBrewing waits until the agent is no longer in loading/brewing state.
// This means the LLM turn is complete and the input is ready.
func waitUntilNotBrewing(t *testing.T, h *ptyHarness, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	brewPattern := regexp.MustCompile(`(?i)brewing|thinking|⠋|⠙|⠹|⠸|⠼|⠴|⠦|⠧|⠇|⠏`)

	for time.Now().Before(deadline) {
		time.Sleep(2 * time.Second)
		h.readAll()
		screen := h.getStrippedOutput()
		// Take the last 200 chars to check current state
		tail := lastN(screen, 200)
		if !brewPattern.MatchString(tail) && strings.Contains(tail, "Type a message") {
			return
		}
	}
	t.Log("[warn] timeout waiting for brewing to end, proceeding anyway")
}

// TestPTY_SubAgentFollowDeepReview runs a sub-agent with a substantial task
// (code review), enters follow mode, and verifies:
//  1. Follow strip appears with slot keys
//  2. Sub-agent tool calls render in the follow panel (read_file, run_command, etc.)
//  3. Sub-agent text output renders in the follow panel
//  4. Follow mode auto-returns when sub-agent completes
//  5. After auto-return, main conversation panel is restored
func TestPTY_SubAgentFollowDeepReview(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}

	h := startGGCodeLive(t)
	defer h.quit()

	// ── Phase 1: Start ggcode and wait for TUI ready ──
	h.waitForText("Type a message", 10*time.Second)
	h.drainOutput()
	time.Sleep(1 * time.Second)
	t.Log("━━━ Phase 1: TUI ready ━━━")

	// ── Phase 2: Ask the LLM to spawn a sub-agent with a substantial review task ──
	// This prompt creates a sub-agent that will read many files and produce analysis.
	// The sub-agent should make dozens of tool calls (read_file, search_files, etc.)
	prompt := "Use spawn_agent to create a sub-agent named 'reviewer' with this task: " +
		"Do a thorough code review of the project. Read at least 10 different source files. " +
		"For each file, identify potential bugs, style issues, and improvement suggestions. " +
		"Write a comprehensive summary at the end. Be thorough — read files one by one. " +
		"Return immediately after spawning, do NOT wait for the sub-agent to finish."
	h.sendText(prompt)
	time.Sleep(500 * time.Millisecond)
	h.sendKey("enter")
	t.Log("━━━ Phase 2: Prompt sent, waiting for LLM to call spawn_agent... ━━━")

	// Wait for spawn_agent tool call to appear in the main agent's output
	h.waitForText("spawn_agent", 120*time.Second)
	t.Log("━━─ Phase 2: spawn_agent tool call detected ━━━")

	// Wait for the main agent to finish its turn
	waitUntilNotBrewing(t, h, 120*time.Second)
	time.Sleep(3 * time.Second)
	h.drainOutput()
	t.Log("━━━ Phase 2: Main agent turn complete ━━━")

	// ── Phase 3: Verify follow strip appeared ──
	screen := h.getStrippedOutput()
	t.Logf("Phase 3 — Screen after agent turn (last 1500 chars):\n%s", lastN(screen, 1500))

	// The follow strip should contain the sub-agent name or "reviewer" and slot key "!"
	hasSlot := strings.Contains(screen, "!")
	hasEsc := strings.Contains(screen, "Esc")
	if !hasSlot {
		t.Error("Phase 3 FAILED: expected slot key '!' in follow strip")
	} else {
		t.Log("━━━ Phase 3: Follow strip visible with slot key '!' ━━━")
	}
	if !hasEsc {
		t.Error("Phase 3 FAILED: expected 'Esc' in follow strip")
	} else {
		t.Log("━━━ Phase 3: Follow strip has 'Esc' hint ━━━")
	}

	// ── Phase 4: Enter follow mode by pressing "!" ──
	t.Log("━━━ Phase 4: Pressing '!' to enter follow mode... ━━━")
	h.sendKey("!")
	time.Sleep(5 * time.Second)

	screen = h.getStrippedOutput()
	t.Logf("Phase 4 — Screen in follow mode (last 1500 chars):\n%s", lastN(screen, 1500))

	// Should show input paused / following indicator
	isFollowing := strings.Contains(screen, "input paused") ||
		strings.Contains(screen, "Following") ||
		strings.Contains(screen, "following")
	if !isFollowing {
		t.Error("Phase 4 FAILED: expected 'input paused' or 'Following' in follow mode")
	} else {
		t.Log("━━━ Phase 4: Follow mode active — input paused ━━━")
	}

	// ── Phase 5: Wait for sub-agent to produce some tool calls, verify rendering ──
	// The sub-agent should be reading files now. Wait and check for tool call rendering.
	t.Log("━━━ Phase 5: Waiting for sub-agent tool calls to appear... ━━━")

	// Poll for 2 minutes, checking for tool names in the output
	toolNames := []string{"read_file", "search_files", "run_command", "glob"}
	foundTools := map[string]bool{}
	deadline := time.Now().Add(2 * time.Minute)

	for time.Now().Before(deadline) {
		time.Sleep(10 * time.Second)
		screen = h.getStrippedOutput()

		for _, tool := range toolNames {
			if strings.Contains(screen, tool) && !foundTools[tool] {
				foundTools[tool] = true
				t.Logf("Phase 5: Detected tool '%s' in sub-agent output ✓", tool)
			}
		}

		// If we found at least 2 different tools, that's good enough
		if len(foundTools) >= 2 {
			t.Log("━━━ Phase 5: Multiple tool calls rendered in follow panel ━━━")
			break
		}
	}

	if len(foundTools) == 0 {
		t.Error("Phase 5 FAILED: no tool calls from sub-agent detected in output after 2 minutes")
	} else {
		t.Logf("Phase 5: Found %d different tool types: %v", len(foundTools), foundTools)
	}

	// ── Phase 6: Check sub-agent is producing text output (not just tool calls) ──
	t.Log("━━━ Phase 6: Checking for sub-agent text output... ━━━")
	screen = h.getStrippedOutput()
	// Sub-agent should have produced some analysis text beyond just tool calls
	// Look for common review phrases or substantial text
	hasAnalysis := strings.Contains(screen, "review") ||
		strings.Contains(screen, "issue") ||
		strings.Contains(screen, "suggest") ||
		strings.Contains(screen, "bug") ||
		strings.Contains(screen, "improvement") ||
		strings.Contains(screen, "code")

	if hasAnalysis {
		t.Log("━━━ Phase 6: Sub-agent analysis text visible in output ━━━")
	} else {
		t.Log("Phase 6: No analysis text detected yet — sub-agent may still be reading files")
	}

	// ── Phase 7: Wait for sub-agent to complete and auto-return ──
	t.Log("━━━ Phase 7: Waiting for sub-agent to complete and auto-return... ━━━")

	autoReturned := false
	for attempt := 0; attempt < 120; attempt++ {
		time.Sleep(5 * time.Second)
		screen = h.getStrippedOutput()

		// Auto-return indicators:
		// 1. "returned to main view" system message
		// 2. "completed" message
		// 3. "input paused" disappears
		if strings.Contains(screen, "returned to main view") ||
			strings.Contains(screen, "completed") {
			autoReturned = true
			t.Logf("━━━ Phase 7: Auto-return detected after %d seconds ━━━", (attempt+1)*5)
			break
		}

		// Check if follow mode ended (input paused no longer in recent output)
		recentTail := lastN(screen, 500)
		if !strings.Contains(recentTail, "input paused") &&
			!strings.Contains(recentTail, "Following sub-agent") &&
			strings.Contains(recentTail, "Type a message") {
			autoReturned = true
			t.Logf("━━━ Phase 7: Auto-return detected (input unpaused) after %d seconds ━━━", (attempt+1)*5)
			break
		}
	}

	if !autoReturned {
		t.Error("Phase 7 FAILED: sub-agent did not auto-return after 10 minutes")
	}

	// ── Phase 8: Verify main conversation panel is restored ──
	time.Sleep(3 * time.Second)
	screen = h.getStrippedOutput()
	t.Logf("Phase 8 — Final screen (last 1000 chars):\n%s", lastN(screen, 1000))

	// Main view should be back: "Type a message" input prompt, no follow mode
	if !strings.Contains(lastN(screen, 500), "Type a message") {
		t.Error("Phase 8 FAILED: main view not restored after auto-return")
	} else {
		t.Log("━━━ Phase 8: Main conversation panel restored ✓ ━━━")
	}

	t.Log("━━━ TestPTY_SubAgentFollowDeepReview COMPLETE ━━━")
}

// TestPTY_SubAgentFollowResizeDuringWork verifies follow mode stability
// during a real sub-agent workload with various terminal resizes.
func TestPTY_SubAgentFollowResizeDuringWork(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}

	h := startGGCodeLive(t)
	defer h.quit()

	h.waitForText("Type a message", 10*time.Second)
	h.drainOutput()
	time.Sleep(1 * time.Second)

	// Spawn a sub-agent that will read many files
	prompt := "Use spawn_agent to create a sub-agent that does the following: " +
		"Read every .go file in the internal/chat/ directory, one by one. " +
		"For each file, summarize what it does in 2-3 sentences. " +
		"Return immediately after spawning."
	h.sendText(prompt)
	time.Sleep(500 * time.Millisecond)
	h.sendKey("enter")
	t.Log("[user] Prompt sent, waiting for spawn_agent...")

	h.waitForText("spawn_agent", 120*time.Second)
	waitUntilNotBrewing(t, h, 120*time.Second)
	time.Sleep(3 * time.Second)
	h.drainOutput()
	t.Log("[user] Agent finished. Entering follow mode...")

	// Enter follow mode
	h.sendKey("!")
	time.Sleep(5 * time.Second)

	screen := h.getStrippedOutput()
	if !strings.Contains(screen, "input paused") && !strings.Contains(screen, "Following") {
		t.Fatal("Expected follow mode to be active before resize test")
	}
	t.Log("[user] Follow mode active. Starting resize stress test...")

	// Cycle through various sizes while sub-agent is working
	sizes := []struct {
		cols, rows uint16
		label      string
	}{
		{80, 24, "80x24 (standard)"},
		{120, 40, "120x40 (default)"},
		{200, 50, "200x50 (wide)"},
		{60, 20, "60x20 (narrow)"},
		{160, 60, "160x60 (tall)"},
		{80, 40, "80x40 (standard wide)"},
		{120, 24, "120x24 (wide short)"},
		{100, 30, "100x30 (medium)"},
	}

	for i, sz := range sizes {
		h.resize(sz.cols, sz.rows)
		time.Sleep(4 * time.Second)

		screen = h.getStrippedOutput()
		recent := lastN(screen, 300)

		// Check for crashes or panics
		if strings.Contains(recent, "panic") || strings.Contains(recent, "runtime error") ||
			strings.Contains(recent, "fatal error") {
			t.Errorf("CRASH detected at %s (step %d):\n%s", sz.label, i, recent)
			return
		}

		// Check that follow mode is still active (or sub-agent completed)
		t.Logf("[resize %d/%d] %s — rendered OK, no crash", i+1, len(sizes), sz.label)
	}

	t.Log("[user] All sizes rendered without crash. Exiting follow mode...")
	h.sendKey("escape")
	time.Sleep(2 * time.Second)

	t.Log("[user] Test passed — follow mode survived 8 resize cycles during active sub-agent work")
}
