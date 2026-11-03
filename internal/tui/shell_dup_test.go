package tui

import (
	"strings"
	"testing"
)

func TestShellChunkSingleChunkNoDuplication(t *testing.T) {
	m := newTestModel()
	m.setShellMode(true)
	if cmd := m.submitShellCommand("echo hello", true); cmd == nil {
		t.Fatal("expected shell submit to return a command")
	}

	next, _ := m.Update(shellCommandStreamMsg{RunID: m.activeShellRunID, Text: "hello\n"})
	m = next.(Model)

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

	count := strings.Count(plain, "hello")
	if count > 2 {
		t.Fatalf("expected 'hello' at most 2 times (cmd+output), got %d times.\nOutput:\n%s", count, plain)
	}
}

func TestShellChunkIncrementalAppend(t *testing.T) {
	m := newTestModel()
	m.setShellMode(true)
	if cmd := m.submitShellCommand("cmd", true); cmd == nil {
		t.Fatal("expected shell submit to return a command")
	}

	next, _ := m.Update(shellCommandStreamMsg{RunID: m.activeShellRunID, Text: "aaa\n"})
	m = next.(Model)

	chatLenAfterFirst := m.chatList.Len()

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

func TestShellChunkOneLineOutputWithLongWait(t *testing.T) {
	m := newTestModel()
	m.setShellMode(true)
	if cmd := m.submitShellCommand("echo test && sleep 10", true); cmd == nil {
		t.Fatal("expected shell submit to return a command")
	}

	next, _ := m.Update(shellCommandStreamMsg{RunID: m.activeShellRunID, Text: "test\n"})
	m = next.(Model)

	for i := 0; i < 100; i++ {
		next, _ = m.Update(shellCommandStreamMsg{RunID: m.activeShellRunID, Text: ""})
		m = next.(Model)
	}

	plain := stripAnsi(renderedOutput(&m))

	cmdIdx := strings.Index(plain, "$ echo test && sleep 10")
	if cmdIdx < 0 {
		t.Fatalf("command not found in output:\n%s", plain)
	}
	outputArea := plain[cmdIdx+len("$ echo test && sleep 10"):]

	count := strings.Count(outputArea, "test")
	if count != 1 {
		t.Fatalf("expected 'test' exactly once in output area, got %d times.\nOutput area:\n%s", count, outputArea)
	}
}
