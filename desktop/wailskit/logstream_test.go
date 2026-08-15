//go:build goolm

package wailskit

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestLogStream_DisabledByDefault(t *testing.T) {
	s := NewLogStream(100)
	if s.IsEnabled() {
		t.Fatal("expected disabled by default")
	}
}

func TestLogStream_WriteWhenDisabled(t *testing.T) {
	s := NewLogStream(100)
	s.Write("agent", "test message")
	if len(s.Drain()) != 0 {
		t.Fatal("expected no entries when disabled")
	}
}

func TestLogStream_WriteWhenEnabled(t *testing.T) {
	s := NewLogStream(100)
	s.Enable(true)
	s.Write("agent", "hello")

	entries := s.Drain()
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].Category != "agent" || entries[0].Message != "hello" {
		t.Fatalf("unexpected entry: %+v", entries[0])
	}
	if entries[0].Seq != 1 {
		t.Fatalf("expected seq 1, got %d", entries[0].Seq)
	}
}

func TestLogStream_DrainClearsPending(t *testing.T) {
	s := NewLogStream(100)
	s.Enable(true)
	s.Write("cat", "a")
	s.Write("cat", "b")

	first := s.Drain()
	if len(first) != 2 {
		t.Fatalf("expected 2, got %d", len(first))
	}

	second := s.Drain()
	if second != nil {
		t.Fatalf("expected nil on second drain, got %d entries", len(second))
	}
}

func TestLogStream_HistoryReturnsLastN(t *testing.T) {
	s := NewLogStream(100)
	s.Enable(true)
	for i := 0; i < 5; i++ {
		s.Write("cat", string(rune('a'+i)))
	}

	hist := s.History(3)
	if len(hist) != 3 {
		t.Fatalf("expected 3, got %d", len(hist))
	}
	if hist[0].Message != "c" || hist[1].Message != "d" || hist[2].Message != "e" {
		t.Fatalf("expected c,d,e, got %s,%s,%s", hist[0].Message, hist[1].Message, hist[2].Message)
	}
}

func TestLogStream_HistoryNExceedsCount(t *testing.T) {
	s := NewLogStream(100)
	s.Enable(true)
	s.Write("cat", "only")

	hist := s.History(10)
	if len(hist) != 1 {
		t.Fatalf("expected 1, got %d", len(hist))
	}
}

func TestLogStream_HistoryZero(t *testing.T) {
	s := NewLogStream(100)
	s.Enable(true)
	s.Write("cat", "x")

	if s.History(0) != nil {
		t.Fatal("expected nil for History(0)")
	}
}

func TestLogStream_RingBufferWraparound(t *testing.T) {
	s := NewLogStream(3) // tiny capacity
	s.Enable(true)
	for i := 0; i < 5; i++ {
		s.Write("cat", string(rune('a'+i)))
	}

	// Only last 3 should be in history
	hist := s.History(3)
	if len(hist) != 3 {
		t.Fatalf("expected 3, got %d", len(hist))
	}
	if hist[0].Message != "c" || hist[1].Message != "d" || hist[2].Message != "e" {
		t.Fatalf("expected c,d,e after wraparound, got %v", hist)
	}
}

func TestLogStream_DrainLogStreamJSON(t *testing.T) {
	s := NewLogStream(100)
	s.Enable(true)
	s.Write("cat", "hello")

	jsonStr := s.DrainLogStreamJSON()
	if jsonStr == "[]" {
		t.Fatal("expected non-empty JSON")
	}

	var entries []LogEntry
	if err := json.Unmarshal([]byte(jsonStr), &entries); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if len(entries) != 1 || entries[0].Message != "hello" {
		t.Fatalf("unexpected entries: %+v", entries)
	}

	// Second call should be empty
	if s.DrainLogStreamJSON() != "[]" {
		t.Fatal("expected empty array after drain")
	}
}

func TestLogStream_ToggleLogStream(t *testing.T) {
	s := NewLogStream(100)
	s.ToggleLogStream(true)
	if !s.IsEnabled() {
		t.Fatal("expected enabled after toggle(true)")
	}
	s.ToggleLogStream(false)
	if s.IsEnabled() {
		t.Fatal("expected disabled after toggle(false)")
	}
}

func TestLogStream_DisablePreservesEntries(t *testing.T) {
	s := NewLogStream(100)
	s.Enable(true)
	s.Write("cat", "captured")
	s.Enable(false)

	// History should still have the entry
	hist := s.History(1)
	if len(hist) != 1 || hist[0].Message != "captured" {
		t.Fatalf("expected entry preserved after disable: %+v", hist)
	}
}

func TestLogStream_NewLogStreamZeroCapacity(t *testing.T) {
	s := NewLogStream(0)
	if s.cap != 2000 {
		t.Fatalf("expected default capacity 2000, got %d", s.cap)
	}
}

func TestLogStream_SequenceIncrements(t *testing.T) {
	s := NewLogStream(100)
	s.Enable(true)
	s.Write("cat", "a")
	s.Write("cat", "b")
	s.Write("cat", "c")

	entries := s.Drain()
	if entries[0].Seq != 1 || entries[1].Seq != 2 || entries[2].Seq != 3 {
		t.Fatalf("expected seq 1,2,3, got %d,%d,%d", entries[0].Seq, entries[1].Seq, entries[2].Seq)
	}
}

// Verify _ = strings is used (import guard for potential future use)
var _ = strings.TrimSpace

// TestLogStream_PendingCapped verifies the pending queue is capped at maxPend
// entries: beyond the cap, the oldest entries are dropped and the overflow is
// counted (#286).
func TestLogStream_PendingCapped(t *testing.T) {
	s := NewLogStream(10000) // ring cap larger than pending cap
	s.Enable(true)
	for i := 0; i < s.maxPend+500; i++ {
		s.Write("cat", fmt.Sprintf("msg-%d", i))
	}
	s.mu.Lock()
	plen := len(s.pending)
	over := s.overflow
	s.mu.Unlock()
	if plen != s.maxPend {
		t.Fatalf("expected pending capped at %d, got %d", s.maxPend, plen)
	}
	if over != 500 {
		t.Fatalf("expected 500 overflow drops, got %d", over)
	}
}

// TestLogStream_PendingDropsOldest verifies that overflow drops the OLDEST
// pending entries, keeping the newest (#286).
func TestLogStream_PendingDropsOldest(t *testing.T) {
	s := NewLogStream(10000)
	s.Enable(true)
	for i := 0; i < s.maxPend+10; i++ {
		s.Write("cat", fmt.Sprintf("msg-%d", i))
	}
	entries := s.Drain()
	// First entry is the overflow notice when overflow > 0.
	if len(entries) != s.maxPend+1 {
		t.Fatalf("expected %d entries (cap + overflow notice), got %d", s.maxPend+1, len(entries))
	}
	if entries[0].Category != "logstream" || !strings.Contains(entries[0].Message, "10 entries dropped") {
		t.Fatalf("expected overflow notice first, got: %+v", entries[0])
	}
	// Oldest 10 messages must have been dropped; first data entry is msg-10.
	if entries[1].Message != "msg-10" {
		t.Fatalf("expected oldest dropped (first data entry msg-10), got %s", entries[1].Message)
	}
	if entries[len(entries)-1].Message != fmt.Sprintf("msg-%d", s.maxPend+9) {
		t.Fatalf("expected newest entry kept, got %s", entries[len(entries)-1].Message)
	}
}

// TestLogStream_DrainOverflowNoticeOnly verifies the overflow notice is still
// surfaced when nothing remains pending (#286).
func TestLogStream_DrainOverflowNoticeOnly(t *testing.T) {
	s := NewLogStream(10)
	s.Enable(true)
	s.Write("cat", "a")
	// #428: overflow>0 with an empty pending list is unreachable in real
	// operation (overflow only increments while pending is FULL, and Drain
	// resets both together under the lock), so the old "overflow-only
	// notice" branch was dead code and was removed. Manipulating the state
	// directly to force it must now simply drain to nothing.
	s.mu.Lock()
	s.pending = s.pending[:0]
	s.overflow = 42
	s.mu.Unlock()

	if entries := s.Drain(); entries != nil {
		t.Fatalf("expected nil drain from unreachable overflow-only state, got: %+v", entries)
	}
}

// TestLogStream_DrainNoSpuriousNotice verifies no overflow notice when the
// cap was never hit (#286).
func TestLogStream_DrainNoSpuriousNotice(t *testing.T) {
	s := NewLogStream(10)
	s.Enable(true)
	s.Write("cat", "a")
	entries := s.Drain()
	if len(entries) != 1 || entries[0].Category != "cat" {
		t.Fatalf("expected exactly one cat entry, got: %+v", entries)
	}
}
