package commands

import (
	"encoding/json"
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

	// Outcome-based health (procedural-memory lifecycle: retain/revise/prune).
	// A skill needs at least skillOutcomeMinSamples recorded outcomes before
	// its success rate is trusted, and a fully failing skill never drops
	// below skillOutcomeMinFactor so it can still recover by being used again.
	skillOutcomeMinSamples = 3
	skillOutcomeMinFactor  = 0.15
)

type skillUsageEntry struct {
	UsageCount    int   `json:"usage_count"`
	LastUsedAt    int64 `json:"last_used_at"`
	SuccessCount  int   `json:"success_count,omitempty"`
	FailureCount  int   `json:"failure_count,omitempty"`
	LastOutcomeAt int64 `json:"last_outcome_at,omitempty"`
}

// OutcomeStat summarizes persisted execution outcomes for one skill.
type OutcomeStat struct {
	Runs      int `json:"runs"`
	Successes int `json:"successes"`
	Failures  int `json:"failures"`
}

// Failing reports whether a skill has enough recorded outcomes and a success
// rate low enough to be considered unhealthy. Unsampled skills are healthy.
func (s OutcomeStat) Failing() bool {
	if s.Runs < skillOutcomeMinSamples {
		return false
	}
	return float64(s.Successes)/float64(s.Runs) < 0.5
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

// RecordOutcome persists a skill execution outcome (success or failure).
// Unlike RecordUsage there is no debounce: outcomes are low-frequency and
// each one feeds the health score that ranks skills in the system prompt.
func RecordOutcome(name string, success bool) error {
	trimmed := normalizeSkillName(name)
	if trimmed == "" {
		return nil
	}

	now := time.Now()
	skillUsageMu.Lock()
	defer skillUsageMu.Unlock()

	usage, err := loadUsageLocked()
	if err != nil {
		return err
	}
	entry := usage[trimmed]
	if success {
		entry.SuccessCount++
	} else {
		entry.FailureCount++
	}
	entry.LastUsedAt = now.UnixMilli()
	entry.LastOutcomeAt = now.UnixMilli()
	usage[trimmed] = entry

	return saveUsageLocked(usage)
}

// OutcomeSnapshot returns persisted execution outcomes for all skills.
// Callers use it to demote or annotate chronically failing skills.
func OutcomeSnapshot() map[string]OutcomeStat {
	skillUsageMu.Lock()
	defer skillUsageMu.Unlock()

	usage, err := loadUsageLocked()
	if err != nil {
		return map[string]OutcomeStat{}
	}
	out := make(map[string]OutcomeStat, len(usage))
	for name, entry := range usage {
		runs := entry.SuccessCount + entry.FailureCount
		if runs <= 0 {
			continue
		}
		out[name] = OutcomeStat{Runs: runs, Successes: entry.SuccessCount, Failures: entry.FailureCount}
	}
	return out
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
	return float64(entry.UsageCount) * factor * reliabilityFactor(entry)
}

// reliabilityFactor scales the usage score by the skill's recorded execution
// success rate once enough outcomes exist. Skills that mostly fail rank lower
// in prompt/menu ordering; healthy or unsampled skills are unaffected.
func reliabilityFactor(entry skillUsageEntry) float64 {
	runs := entry.SuccessCount + entry.FailureCount
	if runs < skillOutcomeMinSamples {
		return 1
	}
	rate := float64(entry.SuccessCount) / float64(runs)
	return skillOutcomeMinFactor + (1-skillOutcomeMinFactor)*rate
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
	home := config.HomeDir()
	return filepath.Join(home, ".ggcode", "skill_usage.json"), nil
}

func normalizeSkillName(name string) string {
	return strings.TrimSpace(strings.TrimPrefix(name, "/"))
}
