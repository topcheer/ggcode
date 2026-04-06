package commands

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
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
		return map[string]skillUsageEntry{}, nil
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
	return os.WriteFile(path, data, 0o644)
}

func usagePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".ggcode", "skill_usage.json"), nil
}

func normalizeSkillName(name string) string {
	return strings.TrimSpace(strings.TrimPrefix(name, "/"))
}
