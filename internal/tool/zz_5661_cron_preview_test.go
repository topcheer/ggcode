package tool

import (
	"fmt"
	"strings"
	"testing"
)

// Regression tests for #5661: cron tool prompt previews must never render
// truncated text without an explicit marker. The pre-fix bug rendered only the
// first non-blank line of an 80-rune-clamped preview, dropping the "..."
// marker whenever the clamp spanned multiple lines, so agents verifying a
// persisted prompt could not distinguish truncated output from complete text.

func TestCronPromptPreviewLine5661(t *testing.T) {
	t.Run("short single-line prompt is verbatim without marker", func(t *testing.T) {
		got := cronPromptPreviewLine("hello world")
		if got != "hello world" {
			t.Fatalf("got %q", got)
		}
		if strings.Contains(got, "truncated") {
			t.Fatalf("unexpected marker: %q", got)
		}
	})

	t.Run("long single-line prompt is clamped with explicit drop count", func(t *testing.T) {
		long := strings.Repeat("x", 140)
		got := cronPromptPreviewLine(long)
		if !strings.HasPrefix(got, strings.Repeat("x", 80)) {
			t.Fatalf("preview must keep first 80 runes: %d", len([]rune(got)))
		}
		if !strings.Contains(got, "...") {
			t.Fatalf("missing ellipsis: %q", got)
		}
		if !strings.Contains(got, "[+60 chars truncated]") {
			t.Fatalf("missing drop-count marker: %q", got)
		}
	})

	t.Run("multi-line prompt never renders marker-less truncation (pre-fix bug)", func(t *testing.T) {
		// First line short (so pre-fix code rendered it bare), later lines long
		// (so the 80-rune clamp spanned lines and the "..." landed on a dropped
		// segment). Verify sentence must stay verifiable via the marker.
		prompt := "task anchor\n" + strings.Repeat("discipline ", 20) + "\nfinal line"
		got := cronPromptPreviewLine(prompt)
		if !strings.Contains(got, "task anchor") {
			t.Fatalf("first line lost: %q", got)
		}
		if !strings.Contains(got, "[+") || !strings.Contains(got, "chars truncated]") {
			t.Fatalf("marker-less truncation regression: %q", got)
		}
		// Total dropped must account for both later lines + the non-first part.
		want := len([]rune(strings.Repeat("discipline ", 20))) + len([]rune("final line"))
		if !strings.Contains(got, fmt.Sprintf("[+%d chars truncated]", want)) {
			t.Fatalf("drop count mismatch: %q (want +%d)", got, want)
		}
	})

	t.Run("blank prompt yields empty preview", func(t *testing.T) {
		if got := cronPromptPreviewLine("\n \n"); got != "" {
			t.Fatalf("got %q", got)
		}
	})

	t.Run("clamp within first line counts both parts of dropped text", func(t *testing.T) {
		prompt := strings.Repeat("y", 100) + "\ntail content here"
		got := cronPromptPreviewLine(prompt)
		// 100-rune first line: 80 kept, 20 dropped; tail line 17 runes.
		if !strings.Contains(got, "[+37 chars truncated]") {
			t.Fatalf("combined drop count wrong: %q", got)
		}
	})
}
