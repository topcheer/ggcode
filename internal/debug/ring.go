package debug

import (
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// RingEntry is a single debug log entry stored in the ring buffer.
type RingEntry struct {
	Seq      int64  `json:"seq"`
	Category string `json:"category"`
	Message  string `json:"message"`
	Time     string `json:"time"`
}

const (
	defaultRingCap = 2000
	maxRingReturn  = 200
)

var (
	ringMu    sync.Mutex
	ringBuf   []RingEntry
	ringCap   int
	ringHead  int
	ringCount int
	ringSeq   int64
)

func init() {
	ringCap = defaultRingCap
	ringBuf = make([]RingEntry, ringCap)
}

// ringAppend appends an entry to the ring buffer. Called from Log().
func ringAppend(category, msg string) {
	seq := atomic.AddInt64(&ringSeq, 1)
	entry := RingEntry{
		Seq:      seq,
		Category: category,
		Message:  msg,
		Time:     time.Now().Format("15:04:05.000"),
	}
	ringMu.Lock()
	ringBuf[ringHead] = entry
	ringHead = (ringHead + 1) % ringCap
	if ringCount < ringCap {
		ringCount++
	}
	ringMu.Unlock()
}

// RingHistory returns the last n entries from the ring buffer, optionally
// filtered by category substring. If n <= 0 or n > maxRingReturn, it is
// clamped to maxRingReturn. For larger queries (e.g. export), use RingHistoryMax.
func RingHistory(n int, categoryFilter string) []RingEntry {
	if n <= 0 || n > maxRingReturn {
		n = maxRingReturn
	}
	return ringHistoryImpl(n, categoryFilter)
}

// RingHistoryMax returns up to n entries (max 2000) from the ring buffer,
// optionally filtered by category substring. Use this for export use cases
// where more than 200 entries are needed.
func RingHistoryMax(n int, categoryFilter string) []RingEntry {
	if n <= 0 {
		n = maxRingReturn
	}
	if n > 2000 {
		n = 2000
	}
	return ringHistoryImpl(n, categoryFilter)
}

// ringHistoryImpl filters by exact category match. The entry's Category
// field was already routed through tagToCategory on the write side
// (ringAppend), so the read side must compare that field directly — the
// old message-body substring fallback ("[agent]" appearing in prose like
// "discussing [agent] loop") produced false positives (#537).
func ringHistoryImpl(n int, categoryFilter string) []RingEntry {
	// Reuse the write-side routing so a tag filter (e.g. "ctx") maps to its
	// canonical category ("context") exactly as ringAppend stored it.
	if cat, ok := tagToCategory[categoryFilter]; ok {
		categoryFilter = cat
	}
	ringMu.Lock()
	defer ringMu.Unlock()
	if ringCount == 0 {
		return nil
	}
	// Read from ring buffer, starting from the oldest entry
	avail := ringCount
	if n > avail {
		n = avail
	}
	start := (ringHead - avail + ringCap) % ringCap
	out := make([]RingEntry, 0, n)
	for i := 0; i < avail && len(out) < n; i++ {
		entry := ringBuf[(start+i)%ringCap]
		if categoryFilter != "" && !strings.EqualFold(entry.Category, categoryFilter) {
			continue
		}
		out = append(out, entry)
	}
	return out
}

// RingStats returns basic stats about the ring buffer.
func RingStats() (count, capacity int) {
	ringMu.Lock()
	defer ringMu.Unlock()
	return ringCount, ringCap
}
