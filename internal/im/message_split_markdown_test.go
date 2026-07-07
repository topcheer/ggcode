package im

import (
	"strings"
	"testing"
)

func TestSplitMarkdown_NoCodeBlock(t *testing.T) {
	text := strings.Repeat("line of text\n", 300) // 3900 chars
	chunks := SplitMarkdown(text, 2000)
	if len(chunks) < 2 {
		t.Fatalf("expected multiple chunks, got %d", len(chunks))
	}
	// Without code blocks, behavior should be same as SplitMessage
	expected := SplitMessage(text, 2000)
	if len(chunks) != len(expected) {
		t.Errorf("chunk count mismatch: got %d, expected %d", len(chunks), len(expected))
	}
}

func TestSplitMarkdown_CodeBlockSplit(t *testing.T) {
	// Create a message with a code block that will be split
	text := "Here is some code:\n```go\n" +
		strings.Repeat("// code line\n", 200) + // ~2600 chars inside code block
		"```\nDone!"

	chunks := SplitMarkdown(text, 2000)
	if len(chunks) < 2 {
		t.Fatalf("expected multiple chunks, got %d", len(chunks))
	}

	// Every chunk should have balanced code fences (even count of ```)
	for i, chunk := range chunks {
		fenceCount := strings.Count(chunk, "```")
		if fenceCount%2 != 0 {
			t.Errorf("chunk %d has unbalanced code fences (%d ```): %s", i, fenceCount, chunk[:min(100, len(chunk))])
		}
	}
}

func TestSplitMarkdown_CodeBlockContinuation(t *testing.T) {
	// Code block split should reopen in the next chunk
	text := "```python\n" +
		strings.Repeat("x = 1\n", 500) + // ~3000 chars
		"```"

	chunks := SplitMarkdown(text, 2000)
	if len(chunks) < 2 {
		t.Fatalf("expected multiple chunks, got %d", len(chunks))
	}

	// Second chunk should start with ``` (reopened code block)
	if !strings.HasPrefix(chunks[1], "```") {
		t.Errorf("second chunk should reopen code block, got: %q", chunks[1][:min(50, len(chunks[1]))])
	}

	// First chunk should end with ``` (closed code block)
	if !strings.HasSuffix(strings.TrimRight(chunks[0], " \t\r"), "```") {
		t.Errorf("first chunk should close code block, got ending: %q", chunks[0][max(0, len(chunks[0])-50):])
	}
}

func TestSplitMarkdown_ShortMessage(t *testing.T) {
	text := "short message"
	chunks := SplitMarkdown(text, 2000)
	if len(chunks) != 1 || chunks[0] != text {
		t.Errorf("short message should pass through unchanged")
	}
}

func TestSplitMarkdown_EmptyCodeBlock(t *testing.T) {
	text := "```python\nprint('hello')\n```"
	chunks := SplitMarkdown(text, 2000)
	if len(chunks) != 1 {
		t.Errorf("short code block should not be split, got %d chunks", len(chunks))
	}
}
