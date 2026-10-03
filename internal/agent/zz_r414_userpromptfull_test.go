package agent

// r414 probes: RunStats carries the full untruncated user input alongside
// the 200-char journal form, and the full form is never serialized.

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestR414_UserPromptFullUntruncated(t *testing.T) {
	long := strings.Repeat("x", 500) + " from now on always use pnpm"
	stats := newRunStats(long)
	// Truncated form: bounded, prefix of the input, and lossy (the exact
	// suffix shape is util.Truncate's business, not this contract's).
	if n := len(stats.UserPrompt); n == 0 || n > 220 {
		t.Fatalf("UserPrompt len=%d, want bounded ~200", n)
	}
	if !strings.HasPrefix(long, stats.UserPrompt[:100]) {
		t.Fatal("UserPrompt is not a prefix of the input")
	}
	if stats.UserPromptFull != long {
		t.Fatalf("UserPromptFull len=%d, want %d (full input)", len(stats.UserPromptFull), len(long))
	}
	if !strings.Contains(stats.UserPromptFull, "pnpm") {
		t.Fatal("UserPromptFull lost late-position content")
	}
	if strings.Contains(stats.UserPrompt, "pnpm") {
		t.Fatal("UserPrompt unexpectedly contains content past the truncation point")
	}
}

func TestR414_UserPromptFullNotSerialized(t *testing.T) {
	stats := newRunStats("never use tabs from now on" + strings.Repeat("y", 300))
	stats.UserPromptFull = "SECRET-TAIL-should-not-serialize"
	data, err := json.Marshal(stats)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "SECRET-TAIL") {
		t.Fatal("UserPromptFull leaked into JSON serialization")
	}
	if !strings.Contains(string(data), "UserPrompt") {
		t.Fatal("serialized form lost UserPrompt entirely (sanity)")
	}
}

// Empty-input resilience for both fields.
func TestR414_NewRunStatsEmpty(t *testing.T) {
	stats := newRunStats("")
	if stats.UserPrompt != "" || stats.UserPromptFull != "" {
		t.Fatalf("empty input: UserPrompt=%q UserPromptFull=%q", stats.UserPrompt, stats.UserPromptFull)
	}
}
