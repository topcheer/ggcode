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

type Budget struct {
	mu        sync.Mutex
	dir       string
	daily     int
	todayKey  string
	todayUsed int
	loaded    bool
}

type usageRecord struct {
	Time   time.Time `json:"time"`
	Task   string    `json:"task"`
	Input  int       `json:"input"`
	Output int       `json:"output"`
	Total  int       `json:"total"`
}

const defaultKnightDailyTokenBudget = 5_000_000

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

func NewBudget(dir string, cfg config.KnightConfig) *Budget {
	return &Budget{
		dir:   filepath.Join(dir, "knight"),
		daily: normalizeDailyTokenBudget(cfg),
	}
}

func (b *Budget) EnsureDir() error {
	return os.MkdirAll(b.dir, 0755)
}

func (b *Budget) ensureLoaded() {
	b.ensureLoadedAt(time.Now())
}

func (b *Budget) CanSpend() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.ensureLoaded()
	if b.daily == 0 {
		return true
	}
	return b.todayUsed < b.daily
}

func (b *Budget) Remaining() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.ensureLoaded()
	if b.daily == 0 {

		return -1
	}
	return b.daily - b.todayUsed
}

func (b *Budget) Used() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.ensureLoaded()
	return b.todayUsed
}

func (b *Budget) DailyLimit() int {
	return b.daily
}

func (b *Budget) Record(task string, inputTokens, outputTokens int) error {
	now := time.Now()
	b.mu.Lock()
	defer b.mu.Unlock()
	b.ensureLoadedAt(now)

	total := inputTokens + outputTokens

	b.todayUsed += total

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

	return nil
}

func (b *Budget) usageFileAt(t time.Time) string {
	return filepath.Join(b.dir, "usage-"+t.Format("2006-01-02")+".jsonl")
}

func (b *Budget) ensureLoadedAt(now time.Time) {
	today := now.Format("2006-01-02")
	if b.loaded && b.todayKey == today {
		return
	}
	path := filepath.Join(b.dir, "usage-"+today+".jsonl")
	data, err := os.ReadFile(path)
	if err != nil {

		if !os.IsNotExist(err) {
			if b.todayKey != today {
				b.todayKey = today
				b.loaded = false
			}
			debug.Log("knight", "budget usage read failed (keeping prior state, will retry): %v", err)
			return
		}

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
