// Package knight implements the background auto-evolution agent.
// Knight runs during idle time in daemon mode, analyzing sessions,
// creating and validating skills, generating tests, and maintaining
// project health.
package knight

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/debug"
)

// Budget tracks daily token consumption for Knight.
type Budget struct {
	mu        sync.Mutex
	dir       string
	daily     int // daily token budget
	todayKey  string
	todayUsed int
	loaded    bool
}

// usageRecord is one line in the daily usage JSONL file.
type usageRecord struct {
	Time   time.Time `json:"time"`
	Task   string    `json:"task"`
	Input  int       `json:"input"`
	Output int       `json:"output"`
	Total  int       `json:"total"`
}

// defaultKnightDailyTokenBudget is the fallback daily total applied when the
// config value is negative or unset (explicit 0 means unlimited and is kept).
const defaultKnightDailyTokenBudget = 5_000_000

// normalizeDailyTokenBudget returns the effective daily token budget for a
// KnightConfig. Negative values are ignored and unset (non-explicit) zero
// values fall back to defaultKnightDailyTokenBudget. Budget and the
// bucket-level guardrail must both derive their total from this helper so
// their caps stay consistent (issue #978).
func normalizeDailyTokenBudget(cfg config.KnightConfig) int {
	if cfg.DailyTokenBudget < 0 {
		debug.Log("knight", "ignoring negative knight daily_token_budget %d, falling back to default %d", cfg.DailyTokenBudget, defaultKnightDailyTokenBudget)
		return defaultKnightDailyTokenBudget
	}
	if cfg.DailyTokenBudget == 0 && !cfg.HasExplicitDailyTokenBudget() {
		return defaultKnightDailyTokenBudget
	}
	return cfg.DailyTokenBudget
}

// NewBudget creates a Budget tracker. Data is stored under dir/knight/.
func NewBudget(dir string, cfg config.KnightConfig) *Budget {
	return &Budget{
		dir:   filepath.Join(dir, "knight"),
		daily: normalizeDailyTokenBudget(cfg),
	}
}

// EnsureDir creates the knight data directory if needed.
func (b *Budget) EnsureDir() error {
	return os.MkdirAll(b.dir, 0755)
}

// ensureLoaded is a convenience wrapper that calls ensureLoadedAt with time.Now().
func (b *Budget) ensureLoaded() {
	b.ensureLoadedAt(time.Now())
}

// CanSpend returns true if Knight has enough remaining budget to start a task.
func (b *Budget) CanSpend() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.ensureLoaded()
	if b.daily == 0 {
		return true
	}
	return b.todayUsed < b.daily
}

// Remaining returns how many tokens are left in today's budget.
func (b *Budget) Remaining() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.ensureLoaded()
	if b.daily == 0 {
		// #1575-C: 0 means UNLIMITED here (CanSpend always true) - reporting
		// Remaining 0 made the WebUI read as "budget exhausted" while tasks
		// kept running. -1 is the unlimited sentinel for display layers.
		return -1
	}
	return b.daily - b.todayUsed
}

// Used returns today's total token consumption.
func (b *Budget) Used() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.ensureLoaded()
	return b.todayUsed
}

// DailyLimit returns the configured daily budget.
func (b *Budget) DailyLimit() int {
	return b.daily
}

// Record adds a token usage entry to today's log.
func (b *Budget) Record(task string, inputTokens, outputTokens int) error {
	now := time.Now()
	b.mu.Lock()
	defer b.mu.Unlock()
	b.ensureLoadedAt(now)

	total := inputTokens + outputTokens
	rec := usageRecord{
		Time:   now,
		Task:   task,
		Input:  inputTokens,
		Output: outputTokens,
		Total:  total,
	}

	line, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("knight budget: marshal record: %w", err)
	}

	path := b.usageFileAt(now)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return fmt.Errorf("knight budget: open %s: %w", path, err)
	}
	defer f.Close()

	if _, err := f.Write(append(line, '\n')); err != nil {
		return fmt.Errorf("knight budget: write record: %w", err)
	}

	b.todayUsed += total
	return nil
}

// usageFileAt returns the path to the usage JSONL file for the given time.
func (b *Budget) usageFileAt(t time.Time) string {
	return filepath.Join(b.dir, "usage-"+t.Format("2006-01-02")+".jsonl")
}

// ensureLoadedAt reads usage from disk for the given date if not already done.
func (b *Budget) ensureLoadedAt(now time.Time) {
	today := now.Format("2006-01-02")
	if b.loaded && b.todayKey == today {
		return
	}
	path := filepath.Join(b.dir, "usage-"+today+".jsonl")
	data, err := os.ReadFile(path)
	if err != nil {
		// #1575-A: only a MISSING file means zero usage - a transient read
		// failure (EACCES, antivirus, indexer lock) with the state already
		// flipped to loaded=true zeroed today's usage for the whole day and
		// CanSpend kept approving past an exhausted budget. Keep the prior
		// state (or stay unloaded) so the load retries.
		if !os.IsNotExist(err) {
			if b.todayKey != today {
				b.todayKey = today
				b.loaded = false
			}
			debug.Log("knight", "budget usage read failed (keeping prior state, will retry): %v", err)
			return
		}
		// No file yet - a fresh day with genuinely zero usage.
		b.todayKey = today
		b.todayUsed = 0
		b.loaded = true
		return
	}
	b.todayKey = today
	b.todayUsed = 0
	b.loaded = true

	for _, line := range splitLines(string(data)) {
		line = trimSpace(line)
		if line == "" {
			continue
		}
		var rec usageRecord
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			continue
		}
		b.todayUsed += rec.Total
	}
}

// splitLines splits text into lines without allocations for empty strings.
func splitLines(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines = append(lines, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}

// trimSpace trims whitespace from a string.
func trimSpace(s string) string {
	start := 0
	for start < len(s) && (s[start] == ' ' || s[start] == '\t' || s[start] == '\r') {
		start++
	}
	end := len(s)
	for end > start && (s[end-1] == ' ' || s[end-1] == '\t' || s[end-1] == '\r') {
		end--
	}
	return s[start:end]
}
