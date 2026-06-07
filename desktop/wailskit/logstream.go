package wailskit

import (
	"encoding/json"
	"sync"
	"sync/atomic"
	"time"
)

// LogEntry represents a single debug log line.
type LogEntry struct {
	Seq      int64  `json:"seq"`
	Category string `json:"category"`
	Message  string `json:"message"`
	Time     string `json:"time"`
}

// LogStream is a ring-buffer of recent log entries that can be
// toggled on/off from the frontend and drained via polling.
type LogStream struct {
	mu      sync.Mutex
	enabled atomic.Bool
	buf     []LogEntry
	cap     int
	head    int // next write position
	count   int // number of valid entries
	seq     int64
	pending []LogEntry // entries since last Drain
}

// NewLogStream creates a LogStream with the given ring-buffer capacity.
func NewLogStream(capacity int) *LogStream {
	if capacity <= 0 {
		capacity = 2000
	}
	return &LogStream{
		buf:     make([]LogEntry, capacity),
		cap:     capacity,
		pending: make([]LogEntry, 0, 128),
	}
}

// Enable turns the stream on or off. When enabled, debug.Log output is
// captured into the ring buffer. When disabled, captures stop but
// existing entries are preserved.
func (s *LogStream) Enable(on bool) {
	s.enabled.Store(on)
}

// IsEnabled returns whether the stream is currently capturing.
func (s *LogStream) IsEnabled() bool {
	return s.enabled.Load()
}

// Write appends a log entry. Safe to call from any goroutine.
// If the stream is disabled, the write is silently dropped.
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
	// Ring buffer
	s.buf[s.head] = entry
	s.head = (s.head + 1) % s.cap
	if s.count < s.cap {
		s.count++
	}
	// Pending queue for drain
	s.pending = append(s.pending, entry)
	s.mu.Unlock()
}

// Drain returns all entries accumulated since the last Drain call,
// then clears the pending list. Returns empty slice if nothing new.
func (s *LogStream) Drain() []LogEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.pending) == 0 {
		return nil
	}
	out := make([]LogEntry, len(s.pending))
	copy(out, s.pending)
	s.pending = s.pending[:0]
	return out
}

// History returns the last N entries from the ring buffer.
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
	// Read from ring buffer, starting from the oldest entry
	start := (s.head - n + s.cap) % s.cap
	for i := 0; i < n; i++ {
		out[i] = s.buf[(start+i)%s.cap]
	}
	return out
}

// DrainLogStreamJSON is the Wails binding that returns pending logs as JSON.
func (s *LogStream) DrainLogStreamJSON() string {
	entries := s.Drain()
	if len(entries) == 0 {
		return "[]"
	}
	data, _ := json.Marshal(entries)
	return string(data)
}

// ToggleLogStream is the Wails binding to enable/disable log capture.
func (s *LogStream) ToggleLogStream(enabled bool) {
	s.Enable(enabled)
}
