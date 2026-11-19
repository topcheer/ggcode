package context

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
)

// ctxStats records one JSONL line per completed agent turn:
// the stable-prefix fingerprint plus the provider-reported cache usage.
// It is the prompt-cache analogue of the memstats sampler (#5812).
type ctxStatsRecord struct {
	TS            string  `json:"ts"`
	HeadSHA256    string  `json:"head_sha256"`
	HeadBytes     int     `json:"head_bytes"`
	NonTailMutSeq int64   `json:"non_tail_mut_seq"`
	InputTokens   int     `json:"input_tokens"`
	CacheRead     int     `json:"cache_read_tokens"`
	CacheWrite    int     `json:"cache_write_tokens"`
	HitRatio      float64 `json:"hit_ratio"`
	DriftEvent    bool    `json:"drift_event"`
	DriftReason   string  `json:"drift_reason,omitempty"`
}

type ctxStatsSampler struct {
	mu       sync.Mutex
	path     string
	lastHead string
	// pending holds usage numbers reported by the provider between turns;
	// RecordTurn flushes one line combining them with the head snapshot.
	pendingRead  int
	pendingWrite int
	pendingInput int
	enabled      bool
}

func newCtxStatsSampler() *ctxStatsSampler {
	s := &ctxStatsSampler{enabled: true}
	home, err := os.UserHomeDir()
	if err != nil {
		s.enabled = false
		return s
	}
	marker := filepath.Join(home, ".ggcode", "ctx-sample")
	if _, err := os.Stat(marker); err != nil {
		// Marker-gated like memstats: opt-in sampling keeps default I/O at zero.
		s.enabled = false
		return s
	}
	s.path = filepath.Join(home, ".ggcode", fmt.Sprintf("ctxstats-%d.jsonl", os.Getpid()))
	return s
}

// RecordProviderUsage buffers the provider-reported cache usage for the
// turn currently in flight. Called by the conversation loop right after a
// model response arrives (resp.Usage).
func (m *Manager) RecordProviderUsage(cacheRead, cacheWrite, inputTokens int) {
	m.ctxStats.mu.Lock()
	defer m.ctxStats.mu.Unlock()
	if !m.ctxStats.enabled {
		return
	}
	m.ctxStats.pendingRead += cacheRead
	m.ctxStats.pendingWrite += cacheWrite
	m.ctxStats.pendingInput += inputTokens
}

// RecordTurn flushes one ctxstats line: head fingerprint + buffered usage
// + drift classification against the previous line.
func (m *Manager) RecordTurn(reason string) {
	m.ctxStats.mu.Lock()
	enabled := m.ctxStats.enabled
	path := m.ctxStats.path
	lastHead := m.ctxStats.lastHead
	r, w, in := m.ctxStats.pendingRead, m.ctxStats.pendingWrite, m.ctxStats.pendingInput
	m.ctxStats.pendingRead, m.ctxStats.pendingWrite, m.ctxStats.pendingInput = 0, 0, 0
	m.ctxStats.mu.Unlock()
	if !enabled {
		return
	}

	m.mu.Lock()
	headText := ""
	for _, msg := range m.messages {
		if msg.Role == "system" {
			headText = systemMessageText(msg)
			break
		}
	}
	seq := m.nonTailMutSeq
	m.mu.Unlock()

	sum := sha256.Sum256([]byte(headText))
	headSHA := hex.EncodeToString(sum[:8])
	rec := ctxStatsRecord{
		TS:            time.Now().UTC().Format(time.RFC3339),
		HeadSHA256:    headSHA,
		HeadBytes:     len(headText),
		NonTailMutSeq: seq,
		InputTokens:   in,
		CacheRead:     r,
		CacheWrite:    w,
		DriftReason:   reason,
	}
	if r+w > 0 {
		rec.HitRatio = float64(r) / float64(r+w)
	}
	if lastHead != "" && headSHA != lastHead {
		rec.DriftEvent = true
		if reason == "" {
			rec.DriftReason = "unknown"
		}
	}
	m.ctxStats.mu.Lock()
	m.ctxStats.lastHead = headSHA
	m.ctxStats.mu.Unlock()

	if rec.DriftEvent && rec.DriftReason == "unknown" {
		debug.Log("ctxstats", "WARN: unexplained prefix drift (head=%s bytes=%d seq=%d hit_ratio=%.2f)", headSHA, rec.HeadBytes, seq, rec.HitRatio)
	}
	line, err := json.Marshal(rec)
	if err != nil {
		return
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write(append(line, '\n'))
}
