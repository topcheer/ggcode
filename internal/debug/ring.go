package debug

import (
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

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

func RingHistory(n int, categoryFilter string) []RingEntry {
	if n <= 0 || n > maxRingReturn {
		n = maxRingReturn
	}
	return ringHistoryImpl(n, categoryFilter)
}

func RingHistoryMax(n int, categoryFilter string) []RingEntry {
	if n <= 0 {
		n = maxRingReturn
	}
	if n > 2000 {
		n = 2000
	}
	return ringHistoryImpl(n, categoryFilter)
}

func ringHistoryImpl(n int, categoryFilter string) []RingEntry {

	if cat, ok := tagToCategory[categoryFilter]; ok {
		categoryFilter = cat
	}
	ringMu.Lock()
	defer ringMu.Unlock()
	if ringCount == 0 {
		return nil
	}

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

func RingStats() (count, capacity int) {
	ringMu.Lock()
	defer ringMu.Unlock()
	return ringCount, ringCap
}
