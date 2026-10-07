package commands

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/config"
)

const (
	skillUsageDebounce   = time.Minute
	skillUsageHalfLife   = 7 * 24 * time.Hour
	skillUsageMinRecency = 0.1
)

type skillUsageEntry struct {
	UsageCount int   `json:"usage_count"`
	LastUsedAt int64 `json:"last_used_at"`
}

var (
	skillUsageMu    sync.Mutex
	lastWriteByName = map[string]time.Time{}
)

func RecordUsage(name string) error {
	trimmed := normalizeSkillName(name)
	if trimmed == "" {
		return nil
	}

	now := time.Now()
	skillUsageMu.Lock()
	defer skillUsageMu.Unlock()

	if lastWrite := lastWriteByName[trimmed]; !lastWrite.IsZero() && now.Sub(lastWrite) < skillUsageDebounce {
		return nil
	}

	usage, err := loadUsageLocked()
	if err != nil {
		return err
	}
	entry := usage[trimmed]
	entry.UsageCount++
	entry.LastUsedAt = now.UnixMilli()
	usage[trimmed] = entry

	if err := saveUsageLocked(usage); err != nil {
		return err
	}
	lastWriteByName[trimmed] = now
	return nil
}

func UsageScore(name string) float64 {
	trimmed := normalizeSkillName(name)
	if trimmed == "" {
		return 0
	}

	skillUsageMu.Lock()
	defer skillUsageMu.Unlock()

	usage, err := loadUsageLocked()
	if err != nil {
		return 0
	}
	return usageScore(usage[trimmed], time.Now())
}

func usageScore(entry skillUsageEntry, now time.Time) float64 {
	if entry.UsageCount <= 0 || entry.LastUsedAt <= 0 {
		return 0
	}
	lastUsed := time.UnixMilli(entry.LastUsedAt)
	recency := now.Sub(lastUsed)
	if recency < 0 {
		recency = 0
	}
	factor := 1.0
	if skillUsageHalfLife > 0 {
		factor = powHalf(float64(recency) / float64(skillUsageHalfLife))
		if factor < skillUsageMinRecency {
			factor = skillUsageMinRecency
		}
	}
	return float64(entry.UsageCount) * factor
}

func powHalf(exponent float64) float64 {
	if exponent <= 0 {
		return 1
	}
	return math.Pow(0.5, exponent)
}

func loadUsageLocked() (map[string]skillUsageEntry, error) {
	path, err := usagePath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]skillUsageEntry{}, nil
		}
		return nil, err
	}
	var usage map[string]skillUsageEntry
	if err := json.Unmarshal(data, &usage); err != nil {
		// #3517: a corrupt skill_usage.json must NOT read as "empty, fine" -
		// that nil-error empty map let RecordUsage overwrite the file and
		// silently erase the entire usage history. Quarantine the damaged
		// file aside (never delete: it is the only copy of the history) and
		// return the error so callers short-circuit instead of rewriting.
		path, perr := usagePath()
		if perr == nil {
			_ = os.Rename(path, path+".corrupt-"+time.Now().Format("20060102-150405"))
		}
		return nil, fmt.Errorf("skill_usage.json is corrupt (quarantined aside): %w", err)
	}
	if usage == nil {
		usage = map[string]skillUsageEntry{}
	}
	return usage, nil
}

func saveUsageLocked(usage map[string]skillUsageEntry) error {
	path, err := usagePath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(usage, "", "  ")
	if err != nil {
		return err
	}
	// #3517: write-then-rename so a crash mid-write can never leave a
	// truncated JSON file behind (which the loader would then quarantine,
	// losing history to a partial write).
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func usagePath() (string, error) {
	home := config.HomeDir()
	return filepath.Join(home, ".ggcode", "skill_usage.json"), nil
}

func normalizeSkillName(name string) string {
	return strings.TrimSpace(strings.TrimPrefix(name, "/"))
}
