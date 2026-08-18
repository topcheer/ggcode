package context

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/topcheer/ggcode/internal/provider"
)

// seedWindowMessages adds the pre-snapshot conversation used by the Defect A
// tests: a system prompt plus alternating user/assistant turns.
func seedWindowMessages(cm *Manager) {
	cm.Add(provider.Message{
		Role: "system",
		Content: []provider.ContentBlock{
			{Type: "text", Text: "System prompt."},
		},
	})
	cm.Add(provider.Message{
		Role: "user",
		Content: []provider.ContentBlock{
			{Type: "text", Text: strings.Repeat("old question ", 80)},
		},
	})
	cm.Add(provider.Message{
		Role: "assistant",
		Content: []provider.ContentBlock{
			{Type: "text", Text: strings.Repeat("old answer ", 80)},
		},
	})
	cm.Add(provider.Message{
		Role: "user",
		Content: []provider.ContentBlock{
			{Type: "text", Text: "snapshot tail"},
		},
	})
	cm.Add(provider.Message{
		Role: "assistant",
		Content: []provider.ContentBlock{
			{Type: "text", Text: "snapshot tail answer"},
		},
	})
}

// summaryResult fabricates a plausible compaction result (the real path goes
// through snapshot.Compact with a provider; these tests only exercise Apply).
func summaryResult() CompactResult {
	summary := provider.Message{
		Role: "system",
		Content: []provider.ContentBlock{
			{Type: "text", Text: "[Previous conversation summary] Summary of old conversation."},
		},
	}
	return CompactResult{
		Messages:   []provider.Message{summary},
		TokenCount: 100,
		Changed:    true,
	}
}

// truncateForTest drops the last n live messages — simulating a rewind
// during the compaction window. Mutates the same internal state the
// production rewind path maintains (user reset per #663).
func truncateForTest(cm *Manager, n int) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	if n > len(cm.messages) {
		n = len(cm.messages)
	}
	cm.messages = cm.messages[:len(cm.messages)-n]
	cm.version++
	cm.nonTailMutSeq++
	cm.userReset = true // rewind semantics
	cm.recalcTokens()
}

func TestApplyCompactResult_RejectsEqualLengthDeleteAppend(t *testing.T) {
	cm := NewManager(1000)
	seedWindowMessages(cm)

	snapshot := cm.CompactSnapshot()
	result := summaryResult()

	// Race window: rewind removes the last 2 messages (k=2, LastMsgID gone),
	// then 2 new messages arrive (j=2). Equal length previously bypassed the
	// shrink guard and the OrigLen fallback made extra empty — the new
	// messages were silently replaced by the lossy summary.
	truncateForTest(cm, 2)
	cm.Add(provider.Message{
		Role: "user",
		Content: []provider.ContentBlock{
			{Type: "text", Text: "NEW message during window 1"},
		},
	})
	cm.Add(provider.Message{
		Role: "user",
		Content: []provider.ContentBlock{
			{Type: "text", Text: "NEW message during window 2"},
		},
	})

	live := cm.Messages()
	if len(live) != len(snapshot.Messages) {
		t.Fatalf("fixture setup: expected equal length (live=%d snapshot=%d) — this is exactly the unguarded case",
			len(live), len(snapshot.Messages))
	}

	applied, _ := cm.ApplyCompactResult(snapshot, result)
	if applied {
		t.Fatal("expected apply to be REJECTED when LastMsgID is missing, even at equal length")
	}
	if rr := cm.LastCompactRejectReason(); rr != CompactRejectUserReset {
		t.Fatalf("expected CompactRejectUserReset (agent #663 refund branch engages), got %v", rr)
	}
	msgs := cm.Messages()
	found := 0
	for _, m := range msgs {
		for _, b := range m.Content {
			if b.Type == "text" && strings.Contains(b.Text, "NEW message during window") {
				found++
			}
		}
	}
	if found != 2 {
		t.Fatalf("expected both window messages preserved after reject, found %d", found)
	}
	if messageContainsTextInList(msgs, "[Previous conversation summary]") {
		t.Fatal("summary must NOT be applied on reject")
	}
}

func TestApplyCompactResult_RejectsMoreAppendThanRemoval(t *testing.T) {
	cm := NewManager(1000)
	seedWindowMessages(cm)

	snapshot := cm.CompactSnapshot()
	result := summaryResult()

	// k=1 removal, j=2 appends: the old OrigLen fallback replaced the first
	// new message with the summary range.
	truncateForTest(cm, 1)
	cm.Add(provider.Message{
		Role: "user",
		Content: []provider.ContentBlock{
			{Type: "text", Text: "new 1"},
		},
	})
	cm.Add(provider.Message{
		Role: "user",
		Content: []provider.ContentBlock{
			{Type: "text", Text: "new 2"},
		},
	})

	applied, _ := cm.ApplyCompactResult(snapshot, result)
	if applied {
		t.Fatal("expected reject when LastMsgID missing and appends exceed removals")
	}
	if rr := cm.LastCompactRejectReason(); rr != CompactRejectUserReset {
		t.Fatalf("expected CompactRejectUserReset, got %v", rr)
	}
}

func TestApplyCompactResult_AnchorPresentStillApplies(t *testing.T) {
	cm := NewManager(1000)
	seedWindowMessages(cm)

	snapshot := cm.CompactSnapshot()
	result := summaryResult()

	cm.Add(provider.Message{
		Role: "user",
		Content: []provider.ContentBlock{
			{Type: "text", Text: "arrived during window"},
		},
	})

	applied, _ := cm.ApplyCompactResult(snapshot, result)
	if !applied {
		t.Fatal("expected apply when anchor present")
	}
	msgs := cm.Messages()
	if !messageContainsTextInList(msgs, "arrived during window") {
		t.Fatal("window arrival must be preserved")
	}
	if !messageContainsTextInList(msgs, "[Previous conversation summary]") {
		t.Fatal("summary must be applied")
	}
}

// addReadPair appends a read_file tool_use + tool_result pair.
func addReadPair(m *Manager, id, input string, output string) {
	m.Add(provider.Message{
		Role: "assistant",
		Content: []provider.ContentBlock{
			{
				Type:     "tool_use",
				ToolID:   id,
				ToolName: "read_file",
				Input:    json.RawMessage(input),
			},
		},
	})
	m.Add(provider.Message{
		Role: "user",
		Content: []provider.ContentBlock{
			provider.ToolResultBlock(id, output, false),
		},
	})
}

func TestCompactSupersededReads_OffsetRangesDoNotSupersede(t *testing.T) {
	m := NewManager(100000)
	addReadPair(m, "read-head", `{"path":"/big.go","offset":1,"limit":2000}`, strings.Repeat("H", 500))
	addReadPair(m, "read-tail", `{"path":"/big.go","offset":5000,"limit":2000}`, strings.Repeat("T", 500))

	freed := m.CompactSupersededReads()
	if freed != 0 {
		t.Fatalf("expected 0 tokens freed — non-overlapping segments must not supersede, got %d", freed)
	}
	for _, msg := range m.Messages() {
		for _, b := range msg.Content {
			if b.Type == "tool_result" && strings.HasPrefix(b.Output, "[superseded:") {
				t.Fatalf("output %s must not be marked superseded", b.ToolID)
			}
		}
	}
}

func TestCompactSupersededReads_WholeReadSupersedesPartial(t *testing.T) {
	m := NewManager(100000)
	addReadPair(m, "read-part", `{"path":"/whole.go","offset":100,"limit":50}`, strings.Repeat("P", 500))
	addReadPair(m, "read-full", `{"path":"/whole.go"}`, strings.Repeat("F", 500))

	freed := m.CompactSupersededReads()
	if freed <= 0 {
		t.Fatal("expected tokens freed — whole read covers the partial read")
	}
	partSuperseded := false
	for _, msg := range m.Messages() {
		for _, b := range msg.Content {
			if b.Type == "tool_result" && b.ToolID == "read-part" {
				if !strings.HasPrefix(b.Output, "[superseded:") {
					t.Fatal("partial read must be superseded by later whole read")
				}
				partSuperseded = true
			}
		}
	}
	if !partSuperseded {
		t.Fatal("expected to find read-part result")
	}
}

func TestCompactSupersededReads_CoveringPartialReadSupersedes(t *testing.T) {
	m := NewManager(100000)
	addReadPair(m, "narrow", `{"path":"/cov.go","offset":100,"limit":50}`, strings.Repeat("N", 500))
	addReadPair(m, "wide", `{"path":"/cov.go","offset":1,"limit":300}`, strings.Repeat("W", 500))

	if freed := m.CompactSupersededReads(); freed <= 0 {
		t.Fatal("expected tokens freed — wider window covers the narrow read")
	}
}

func TestCompactSupersededReads_WholeReadsStillSupersede(t *testing.T) {
	m := NewManager(100000)
	addReadPair(m, "whole-0", `{"path":"/compat.go"}`, strings.Repeat("x", 500))
	addReadPair(m, "whole-1", `{"path":"/compat.go"}`, strings.Repeat("y", 500))

	if freed := m.CompactSupersededReads(); freed <= 0 {
		t.Fatal("expected whole-file re-read to still supersede (pre-#718 semantics)")
	}
}

func TestHeadRunesPlain_RuneSafe(t *testing.T) {
	cjk := strings.Repeat("界", 100) // 3 bytes each
	got := headRunesPlain(cjk, 100) // at most 33 full runes
	if !utf8.ValidString(got) {
		t.Fatal("headRunesPlain must cut on rune boundary")
	}
	if len(got) > 100 {
		t.Fatalf("byte cap exceeded: %d", len(got))
	}
	if headRunesPlain("abc", 100) != "abc" {
		t.Fatal("short string must pass through")
	}
	if headRunesPlain("abcdef", 3) != "abc" {
		t.Fatalf("ascii cut mismatch: %q", headRunesPlain("abcdef", 3))
	}
}

func TestTruncStr_RuneSafe(t *testing.T) {
	s := strings.Repeat("界", 10) // 30 bytes; maxLen 10 keeps 2 full runes + "..."
	got := truncStr(s, 10)
	if !utf8.ValidString(got) {
		t.Fatalf("truncStr cut mid-rune: %q", got)
	}
	if got != "界界..." {
		t.Fatalf("unexpected result: %q", got)
	}
}
