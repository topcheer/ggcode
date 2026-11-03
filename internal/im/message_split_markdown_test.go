package im

import (
	"strings"
	"testing"
)

func TestSplitMarkdown_NoCodeBlock(t *testing.T) {
	text := strings.Repeat("line of text\n", 300)
	chunks := SplitMarkdown(text, 2000)
	if len(chunks) < 2 {
		t.Fatalf("expected multiple chunks, got %d", len(chunks))
	}

	expected := SplitMessage(text, 2000)
	if len(chunks) != len(expected) {
		t.Errorf("chunk count mismatch: got %d, expected %d", len(chunks), len(expected))
	}
}

func TestSplitMarkdown_CodeBlockSplit(t *testing.T) {

	text := "Here is some code:\n```go\n" +
		strings.Repeat("// code line\n", 200) +
		"```\nDone!"

	chunks := SplitMarkdown(text, 2000)
	if len(chunks) < 2 {
		t.Fatalf("expected multiple chunks, got %d", len(chunks))
	}

	for i, chunk := range chunks {
		fenceCount := strings.Count(chunk, "```")
		if fenceCount%2 != 0 {
			t.Errorf("chunk %d has unbalanced code fences (%d ```): %s", i, fenceCount, chunk[:min(100, len(chunk))])
		}
	}
}

func TestSplitMarkdown_CodeBlockContinuation(t *testing.T) {

	text := "```python\n" +
		strings.Repeat("x = 1\n", 500) +
		"```"

	chunks := SplitMarkdown(text, 2000)
	if len(chunks) < 2 {
		t.Fatalf("expected multiple chunks, got %d", len(chunks))
	}

	if !strings.HasPrefix(chunks[1], "```") {
		t.Errorf("second chunk should reopen code block, got: %q", chunks[1][:min(50, len(chunks[1]))])
	}

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

func TestSplitMarkdown_ChunkSizeRespectsMaxLen(t *testing.T) {

	maxLen := 2000
	text := "```go\n" + strings.Repeat("x", 5000) + "\n```"
	chunks := SplitMarkdown(text, maxLen)
	for i, chunk := range chunks {
		if len(chunk) > maxLen {
			t.Errorf("chunk %d is %d chars, exceeds maxLen %d (code block markers overflow)",
				i, len(chunk), maxLen)
		}
	}
}

func TestSplitMarkdown_ChunkSizeRespectsMaxLenSlack(t *testing.T) {

	maxLen := 4000
	text := "```js\n" + strings.Repeat("const x = 1;\n", 500) + "\n```"
	chunks := SplitMarkdown(text, maxLen)
	for i, chunk := range chunks {
		if len(chunk) > maxLen {
			t.Errorf("chunk %d is %d chars, exceeds maxLen %d (code block markers overflow)",
				i, len(chunk), maxLen)
		}
	}
}

func TestSplitMarkdown_LanguageTagPreserved(t *testing.T) {

	tests := []struct {
		name string
		lang string
	}{
		{"go", "go"},
		{"python", "python"},
		{"javascript", "javascript"},
		{"no_language", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var text string
			if tt.lang != "" {
				text = "```" + tt.lang + "\n" + strings.Repeat("x = 1\n", 500) + "```"
			} else {
				text = "```\n" + strings.Repeat("x = 1\n", 500) + "```"
			}

			chunks := SplitMarkdown(text, 2000)
			if len(chunks) < 2 {
				t.Fatalf("expected multiple chunks, got %d", len(chunks))
			}

			for i := 1; i < len(chunks); i++ {
				if !strings.HasPrefix(chunks[i], "```") {
					continue
				}
				if tt.lang != "" {
					expected := "```" + tt.lang + "\n"
					if !strings.HasPrefix(chunks[i], expected) {
						t.Errorf("chunk %d: expected prefix %q, got %q",
							i, expected, chunks[i][:min(len(expected)+10, len(chunks[i]))])
					}
				} else {

					if !strings.HasPrefix(chunks[i], "```\n") {
						t.Errorf("chunk %d: expected bare ```\\n prefix, got %q",
							i, chunks[i][:min(20, len(chunks[i]))])
					}
				}
			}
		})
	}
}

func TestSplitMarkdown_MultipleCodeBlocksWithLanguage(t *testing.T) {

	text := "```go\n" + strings.Repeat("a\n", 1000) +
		"```\n```python\n" + strings.Repeat("b\n", 1000) + "```"

	chunks := SplitMarkdown(text, 2000)
	if len(chunks) < 2 {
		t.Fatalf("expected at least 2 chunks, got %d", len(chunks))
	}

	for i, chunk := range chunks {
		fenceCount := strings.Count(chunk, "```")
		if fenceCount%2 != 0 {
			t.Errorf("chunk %d has unbalanced fences (%d): %s",
				i, fenceCount, chunk[:min(80, len(chunk))])
		}
	}

	foundGo := false
	for i := 1; i < len(chunks); i++ {
		if strings.HasPrefix(chunks[i], "```go\n") {
			foundGo = true
		}
	}

	if !foundGo {
		t.Error("no continuation chunk with 'go' language tag found")
	}
}
