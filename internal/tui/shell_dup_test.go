package tui

import (
	"strings"
	"testing"
)

// TestShellChunkSingleChunkNoDuplication verifies that a single chunk
// creates exactly one system message (not one per render cycle).
func TestShellChunkSingleChunkNoDuplication(t *testing.T) {
	m := newTestModel()
	m.setShellMode(true)
	if cmd := m.submitShellCommand("echo hello", true); cmd == nil {
		t.Fatal("expected shell submit to return a command")
	}

	// First poll returns output
	next, _ := m.Update(shellCommandStreamMsg{RunID: m.activeShellRunID, Text: "hello\n"})
	m = next.(Model)

	// 10 more polls with empty text — no new output
	countBefore := m.chatList.Len()
	for i := 0; i < 10; i++ {
		next, _ = m.Update(shellCommandStreamMsg{RunID: m.activeShellRunID, Text: ""})
		m = next.(Model)
	}
	countAfter := m.chatList.Len()
	if countAfter != countBefore {
		t.Fatalf("empty chunks should not add chat items: before=%d after=%d", countBefore, countAfter)
	}

	plain := stripAnsi(renderedOutput(&m))
	// "hello" appears once in the user command and once in output = 2 total. That's correct.
	// What would be WRONG is if "hello" appeared 3+ times (duplicated output).
	count := strings.Count(plain, "hello")
	if count > 2 {
		t.Fatalf("expected 'hello' at most 2 times (cmd+output), got %d times.\nOutput:\n%s", count, plain)
	}
}

// TestShellChunkIncrementalAppend verifies that multiple incremental chunks
// all go into the same system message bubble.
func TestShellChunkIncrementalAppend(t *testing.T) {
	m := newTestModel()
	m.setShellMode(true)
	if cmd := m.submitShellCommand("cmd", true); cmd == nil {
		t.Fatal("expected shell submit to return a command")
	}

	// First incremental chunk
	next, _ := m.Update(shellCommandStreamMsg{RunID: m.activeShellRunID, Text: "aaa\n"})
	m = next.(Model)

	// Count chat items after first chunk
	chatLenAfterFirst := m.chatList.Len()

	// Second incremental chunk — should append to same message, NOT create new one
	next, _ = m.Update(shellCommandStreamMsg{RunID: m.activeShellRunID, Text: "bbb\n"})
	m = next.(Model)

	chatLenAfterSecond := m.chatList.Len()
	if chatLenAfterSecond != chatLenAfterFirst {
		t.Fatalf("second chunk should append to existing message, not create new one: before=%d after=%d",
			chatLenAfterFirst, chatLenAfterSecond)
	}

	plain := stripAnsi(renderedOutput(&m))
	if !strings.Contains(plain, "aaa") {
		t.Fatalf("expected 'aaa' in output, got:\n%s", plain)
	}
	if !strings.Contains(plain, "bbb") {
		t.Fatalf("expected 'bbb' in output, got:\n%s", plain)
	}
}

// TestShellChunkOneLineOutputWithLongWait simulates "echo test && sleep 10":
// one line of output, then many empty polls. Output should appear exactly once.
func TestShellChunkOneLineOutputWithLongWait(t *testing.T) {
	m := newTestModel()
	m.setShellMode(true)
	if cmd := m.submitShellCommand("echo test && sleep 10", true); cmd == nil {
		t.Fatal("expected shell submit to return a command")
	}

	// First poll: echo already ran, "test\n" is available
	next, _ := m.Update(shellCommandStreamMsg{RunID: m.activeShellRunID, Text: "test\n"})
	m = next.(Model)

	// 100 subsequent polls while sleep runs — all return empty
	for i := 0; i < 100; i++ {
		next, _ = m.Update(shellCommandStreamMsg{RunID: m.activeShellRunID, Text: ""})
		m = next.(Model)
	}

	plain := stripAnsi(renderedOutput(&m))

	// The output area (below the command) should only have "test" once.
	// Extract just the output area: everything after the command line
	cmdIdx := strings.Index(plain, "$ echo test && sleep 10")
	if cmdIdx < 0 {
		t.Fatalf("command not found in output:\n%s", plain)
	}
	outputArea := plain[cmdIdx+len("$ echo test && sleep 10"):]

	// Count "test" in output area only (not in command line)
	count := strings.Count(outputArea, "test")
	if count != 1 {
		t.Fatalf("expected 'test' exactly once in output area, got %d times.\nOutput area:\n%s", count, outputArea)
	}
}
