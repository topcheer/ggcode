package im

import (
	"strings"
	"testing"
)

func TestSplitMessage_ShortMessage(t *testing.T) {
	chunks := SplitMessage("hello", 100)
	if len(chunks) != 1 || chunks[0] != "hello" {
		t.Errorf("expected single chunk, got %v", chunks)
	}
}

func TestSplitMessage_EmptyMessage(t *testing.T) {
	chunks := SplitMessage("", 100)
	if len(chunks) != 1 || chunks[0] != "" {
		t.Errorf("expected single empty chunk, got %v", chunks)
	}
}

func TestSplitMessage_ExactLength(t *testing.T) {
	msg := strings.Repeat("a", 100)
	chunks := SplitMessage(msg, 100)
	if len(chunks) != 1 || len(chunks[0]) != 100 {
		t.Errorf("expected single chunk of 100, got %d chunks", len(chunks))
	}
}

func TestSplitMessage_OneOver(t *testing.T) {
	msg := strings.Repeat("a", 101)
	chunks := SplitMessage(msg, 100)
	if len(chunks) != 2 {
		t.Fatalf("expected 2 chunks, got %d", len(chunks))
	}
	if len(chunks[0]) != 100 {
		t.Errorf("chunk[0] length=%d, want 100", len(chunks[0]))
	}
	if chunks[1] != "a" {
		t.Errorf("chunk[1]=%q, want 'a'", chunks[1])
	}
}

func TestSplitMessage_AtNewlineBoundary(t *testing.T) {
	msg := "line1\nline2\nline3\nline4"
	chunks := SplitMessage(msg, 12) // "line1\nline2\n" = 12
	if len(chunks) < 2 {
		t.Fatalf("expected >= 2 chunks, got %d", len(chunks))
	}
	// First chunk should split at newline
	if !strings.HasSuffix(chunks[0], "\n") {
		t.Errorf("expected chunk to end with newline, got %q", chunks[0])
	}
}

func TestSplitMessage_LongLine(t *testing.T) {
	// Single line longer than maxLen — should hard-split
	msg := strings.Repeat("x", 300)
	chunks := SplitMessage(msg, 100)
	if len(chunks) != 3 {
		t.Errorf("expected 3 chunks, got %d", len(chunks))
	}
}

func TestSplitMessage_ManySmallLines(t *testing.T) {
	lines := make([]string, 50)
	for i := range lines {
		lines[i] = "short line"
	}
	msg := strings.Join(lines, "\n")
	chunks := SplitMessage(msg, 100)

	// Verify reassembly equals original
	reassembled := strings.Join(chunks, "")
	if reassembled != msg {
		t.Error("reassembled message doesn't match original")
	}
}

func TestSplitMessage_Reassembly(t *testing.T) {
	msg := "This is a long message.\nIt has multiple lines.\nSome lines are short.\n" +
		strings.Repeat("Very long line without breaks ", 20) + "\nFinal line."

	chunks := SplitMessage(msg, 50)
	reassembled := strings.Join(chunks, "")
	if reassembled != msg {
		t.Errorf("reassembled doesn't match original.\nGot:      %q\nExpected: %q", reassembled, msg)
	}
}

func TestSplitMessageForPlatform(t *testing.T) {
	longMsg := strings.Repeat("x", 5000)

	discordChunks := SplitMessageForPlatform(longMsg, PlatformDiscord)
	if len(discordChunks) < 2 {
		t.Error("Discord should split 5000 chars")
	}
	for i, chunk := range discordChunks {
		if len(chunk) > 2000 {
			t.Errorf("Discord chunk[%d] length=%d exceeds 2000", i, len(chunk))
		}
	}

	tgChunks := SplitMessageForPlatform(longMsg, PlatformTelegram)
	if len(tgChunks) < 2 {
		t.Error("Telegram should split 5000 chars")
	}

	dummyChunks := SplitMessageForPlatform(longMsg, PlatformDummy)
	if len(dummyChunks) != 1 {
		t.Error("Dummy platform should not split 5000 chars (limit=50000)")
	}
}

func TestPlatformLimits(t *testing.T) {
	limits := map[Platform]int{
		PlatformDiscord:  2000,
		PlatformSlack:    4000,
		PlatformDingTalk: 4000,
		PlatformTelegram: 4096,
		PlatformQQ:       3000,
		PlatformFeishu:   4000,
	}
	for platform, expected := range limits {
		actual, ok := PlatformLimits[platform]
		if !ok {
			t.Errorf("missing platform limit for %v", platform)
		} else if actual != expected {
			t.Errorf("platform %v: limit=%d, want %d", platform, actual, expected)
		}
	}
}

func BenchmarkSplitMessage(b *testing.B) {
	msg := strings.Repeat("This is a test line.\n", 200) // ~4200 bytes
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		SplitMessage(msg, 1000)
	}
}

func BenchmarkSplitMessageShort(b *testing.B) {
	msg := "short message"
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		SplitMessage(msg, 1000)
	}
}
