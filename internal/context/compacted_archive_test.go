package context

import (
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
)

// TestCompactedArchiveSummaryReplacesButArchiveKeeps: after a successful
// ApplyCompactResult, the summarized-away messages must be retrievable from
// the archive even though they are gone from the live window.
func TestCompactedArchiveSummaryReplacesButArchiveKeeps(t *testing.T) {
	cm := NewManager(1000)
	seedWindowMessages(cm)

	snapshot := cm.CompactSnapshot()
	result := summaryResult()

	// The old question text must be findable BEFORE compaction (sanity).
	live := cm.Messages()
	foundLive := false
	for _, msg := range live {
		for _, blk := range msg.Content {
			if strings.Contains(blk.Text, "old question") {
				foundLive = true
			}
		}
	}
	if !foundLive {
		t.Fatal("fixture setup: 'old question' expected in live window before compaction")
	}

	applied, _ := cm.ApplyCompactResult(snapshot, result)
	if !applied {
		t.Fatal("fixture setup: ApplyCompactResult refused the fabricated result")
	}

	hits := cm.SearchCompactedArchive("OLD QUESTION", 5)
	if len(hits) == 0 {
		t.Fatal("post-compaction search must find the summarized-away content (case-insensitive)")
	}
	if !strings.Contains(hits[0].Excerpt, "old question") {
		t.Fatalf("excerpt must contain the original text, got: %q", hits[0].Excerpt)
	}
	if hits[0].Reason != "summarized" {
		t.Fatalf("expected reason 'summarized', got %q", hits[0].Reason)
	}

	// The live window no longer carries it.
	live = cm.Messages()
	for _, msg := range live {
		for _, blk := range msg.Content {
			if strings.Contains(blk.Text, "old question") {
				t.Fatal("live window should no longer contain the summarized-away text")
			}
		}
	}
}

// TestCompactedArchiveSkipsSystemPrompt: the (huge) system prompt must not
// be archived, only user/assistant content.
func TestCompactedArchiveSkipsSystemPrompt(t *testing.T) {
	cm := NewManager(1000)
	seedWindowMessages(cm)
	snapshot := cm.CompactSnapshot()
	cm.ApplyCompactResult(snapshot, summaryResult())
	if got := cm.CompactedArchiveSize(); got != 4 {
		t.Fatalf("expected 4 archived entries (2 user + 2 assistant, no system), got %d", got)
	}
	hits := cm.SearchCompactedArchive("System prompt.", 5)
	if len(hits) != 0 {
		t.Fatal("system prompt must never be archived")
	}
}

// TestCompactedArchiveSupersededToolResult: mechanical supersession must
// archive the original tool output before stubbing it.
func TestCompactedArchiveSupersededToolResult(t *testing.T) {
	cm := NewManager(10000)
	long := strings.Repeat("make: *** [target] Error 1\n", 40) // >200 chars
	cm.Add(provider.Message{
		Role: "user",
		Content: []provider.ContentBlock{
			{
				Type:     "tool_result",
				ToolID:   "toolu_old_1",
				ToolName: "run_command",
				Output:   long,
			},
		},
	})
	freed, compacted := cm.compactSupersededToolResults(
		map[string]bool{"toolu_old_1": true}, "newer read")
	if compacted != 1 || freed == 0 {
		t.Fatalf("expected 1 compacted result, got compacted=%d freed=%d", compacted, freed)
	}
	hits := cm.SearchCompactedArchive("Error 1", 5)
	if len(hits) == 0 {
		t.Fatal("superseded tool output must be retrievable from the archive")
	}
	if hits[0].ToolID != "toolu_old_1" {
		t.Fatalf("hit must carry the tool ID, got %q", hits[0].ToolID)
	}
	if hits[0].Reason != "newer read" {
		t.Fatalf("hit reason must be the supersession reason, got %q", hits[0].Reason)
	}
}

// TestCompactedArchiveNewestFirstAndLimit: hits are returned newest first
// and honor the limit.
func TestCompactedArchiveNewestFirstAndLimit(t *testing.T) {
	cm := NewManager(1000)
	cm.mu.Lock()
	cm.compactedArchive = []CompactedEntry{
		{Role: "user", Reason: "summarized", Text: "ancient mention of SIGSEGV here"},
		{Role: "assistant", Reason: "summarized", Text: "later note about SIGSEGV too"},
		{Role: "user", Reason: "summarized", Text: "newest SIGSEGV mention"},
	}
	cm.mu.Unlock()
	hits := cm.SearchCompactedArchive("sigsegv", 2)
	if len(hits) != 2 {
		t.Fatalf("limit=2 must return at most 2 hits, got %d", len(hits))
	}
	if !strings.HasPrefix(hits[0].Excerpt, "newest SIGSEGV") {
		t.Fatalf("hits must be newest-first, first excerpt: %q", hits[0].Excerpt)
	}
	if hits[1].Excerpt != "later note about SIGSEGV too" {
		t.Fatalf("second hit should be the middle entry, got %q", hits[1].Excerpt)
	}
}

// TestCompactedArchiveCapacityEviction: exceeding the byte cap evicts the
// oldest entries first.
func TestCompactedArchiveCapacityEviction(t *testing.T) {
	cm := NewManager(1000)
	big := strings.Repeat("x", maxCompactedArchiveBytes/2+100)
	cm.mu.Lock()
	for i := 0; i < 4; i++ {
		cm.compactedArchive = append(cm.compactedArchive, CompactedEntry{
			Role:   "user",
			Reason: "summarized",
			Text:   big + "-marker-" + string(rune('A'+i)),
		})
	}
	// total = 4 * (~half cap) > cap -> trim keeps ~2 newest
	cm.trimCompactedArchiveLocked()
	size := len(cm.compactedArchive)
	cm.mu.Unlock()
	if size > 2 {
		t.Fatalf("cap trim should keep at most ~2 half-cap entries, got %d", size)
	}
	// Newest must survive, oldest must be gone.
	if got := cm.SearchCompactedArchive("marker-D", 5); len(got) == 0 {
		t.Fatal("newest entry must survive eviction")
	}
	if got := cm.SearchCompactedArchive("marker-A", 5); len(got) != 0 {
		t.Fatal("oldest entry must have been evicted")
	}
}

// TestCompactedArchiveEmptyQueryGuard: blank queries and non-positive
// limits are rejected without touching the archive.
func TestCompactedArchiveEmptyQueryGuard(t *testing.T) {
	cm := NewManager(1000)
	cm.mu.Lock()
	cm.compactedArchive = []CompactedEntry{{Role: "user", Text: "anything"}}
	cm.mu.Unlock()
	if got := cm.SearchCompactedArchive("   ", 5); got != nil {
		t.Fatal("blank query must return nil")
	}
	if got := cm.SearchCompactedArchive("anything", 0); got != nil {
		t.Fatal("non-positive limit must return nil")
	}
}
