package wailskit

import (
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

type LogEntry struct {
	Seq      int64  `json:"seq"`
	Category string `json:"category"`
	Message  string `json:"message"`
	Time     string `json:"time"`
}

type LogStream struct {
	mu       sync.Mutex
	enabled  atomic.Bool
	buf      []LogEntry
	cap      int
	head     int
	count    int
	seq      int64
	pending  []LogEntry
	maxPend  int
	overflow uint64
}

const maxPendingEntries = 5000

func NewLogStream(capacity int) *LogStream {
	if capacity <= 0 {
		capacity = 2000
	}
	return &LogStream{
		buf:     make([]LogEntry, capacity),
		cap:     capacity,
		pending: make([]LogEntry, 0, 128),
		maxPend: maxPendingEntries,
	}
}

func (s *LogStream) Enable(on bool) {
	s.enabled.Store(on)
}

func (s *LogStream) IsEnabled() bool {
	return s.enabled.Load()
}

func (s *LogStream) Write(category, message string) {
	if !s.enabled.Load() {
		return
	}
	now := time.Now().Format("15:04:05.000")
	s.mu.Lock()
	seq := atomic.AddInt64(&s.seq, 1)
	entry := LogEntry{
		Seq:      seq,
		Category: category,
		Message:  message,
		Time:     now,
	}

	s.buf[s.head] = entry
	s.head = (s.head + 1) % s.cap
	if s.count < s.cap {
		s.count++
	}

	if len(s.pending) >= s.maxPend {
		copy(s.pending, s.pending[1:])
		s.pending[len(s.pending)-1] = entry
		s.overflow++
	} else {
		s.pending = append(s.pending, entry)
	}
	s.mu.Unlock()
}

func (s *LogStream) Drain() []LogEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.pending) == 0 {

		return nil
	}
	out := make([]LogEntry, 0, len(s.pending)+1)
	if s.overflow > 0 {
		out = append(out, LogEntry{

			Seq:      s.pending[0].Seq - int64(s.overflow),
			Category: "logstream",
			Message:  fmt.Sprintf("log stream overflow: %d entries dropped (pending queue capped at %d)", s.overflow, s.maxPend),
			Time:     time.Now().Format("15:04:05.000"),
		})
		s.overflow = 0
	}
	out = append(out, s.pending...)
	s.pending = s.pending[:0]
	return out
}

func (s *LogStream) History(n int) []LogEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	if n > s.count {
		n = s.count
	}
	if n <= 0 {
		return nil
	}
	out := make([]LogEntry, n)

	start := (s.head - n + s.cap) % s.cap
	for i := 0; i < n; i++ {
		out[i] = s.buf[(start+i)%s.cap]
	}
	return out
}

func (s *LogStream) DrainLogStreamJSON() string {
	entries := s.Drain()
	if len(entries) == 0 {
		return "[]"
	}
	data, _ := json.Marshal(entries)
	return string(data)
}

func (s *LogStream) ToggleLogStream(enabled bool) {
	s.Enable(enabled)
}
