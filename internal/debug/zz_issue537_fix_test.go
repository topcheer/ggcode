package debug

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestTruncateMessageRuneSafe(t *testing.T) {

	pad := strings.Repeat("中", 1361)
	msg := "[agent] " + pad + strings.Repeat("中", 5)

	got := truncateMessage(msg)
	if !utf8.ValidString(got) {
		t.Fatalf("truncated message is not valid UTF-8 (rune split mid-sequence): tail=%q", got[len(got)-12:])
	}
	if got == msg {
		t.Fatal("expected message to be truncated")
	}
	if !strings.HasSuffix(got, "...") {
		t.Fatalf("expected \"...\" truncation marker, got tail=%q", got[len(got)-12:])
	}

	if len(got) > maxMessageLen {
		t.Fatalf("truncated message %d bytes exceeds cap %d", len(got), maxMessageLen)
	}
}

func TestTruncateMessageShortUnchanged(t *testing.T) {
	msg := "[wechat] 收到用户消息：你好世界"
	if got := truncateMessage(msg); got != msg {
		t.Fatalf("short message was modified: %q -> %q", msg, got)
	}
}

func TestLogRingTruncationRuneSafe(t *testing.T) {

	const marker = "ZZ537-CJK"
	long := strings.Repeat("界", maxMessageLen/3+10)
	Log("agent", "%s %s", marker, long)

	var mine *RingEntry
	for _, e := range RingHistoryMax(2000, "agent") {
		if strings.Contains(e.Message, marker) {
			mine = &e
			break
		}
	}
	if mine == nil {
		t.Fatal("could not find this test's ring entry")
	}
	msg := mine.Message
	if !utf8.ValidString(msg) {
		t.Fatalf("ring message invalid UTF-8 after truncation: tail=%q", msg[len(msg)-12:])
	}
	if !strings.HasSuffix(msg, "...") {
		t.Fatalf("expected \"...\" marker on truncated ring message, got tail=%q", msg[len(msg)-12:])
	}
}

func TestRingHistoryCategoryFilterExact(t *testing.T) {

	Log("unknown_tag", "discussing [agent] loop behavior")

	Log("agent", "real agent message")

	agentEntries := RingHistory(100, "agent")
	for _, e := range agentEntries {
		if e.Category != "agent" {
			t.Fatalf("filter \"agent\" returned entry with category %q: %+v", e.Category, e)
		}
	}
	found := false
	for _, e := range agentEntries {
		if strings.Contains(e.Message, "real agent message") {
			found = true
		}
	}
	if !found {
		t.Fatal("expected the real agent entry to be returned by the agent filter")
	}

	Log("ctx", "context tagged message")
	ctxEntries := RingHistory(100, "ctx")
	if len(ctxEntries) == 0 {
		t.Fatal("expected tag filter \"ctx\" to route to category \"context\" and match")
	}
	for _, e := range ctxEntries {
		if e.Category != "context" {
			t.Fatalf("ctx filter returned category %q, want \"context\"", e.Category)
		}
	}
}
