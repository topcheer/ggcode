package knight

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// UsageTracker tracks skill usage for Knight's lifecycle management.
// Persists to a JSON file in the project's .ggcode/ directory.
// Uses a dirty flag to batch disk writes — only saves when data changed
// and at most once per writeInterval.
type UsageTracker struct {
	mu            sync.Mutex
	path          string
	data          map[string]*skillUsage
	loaded        bool
	dirty         bool
	writeInterval time.Duration
	lastWrite     time.Time
}

type skillUsage struct {
	UsageCount    int       `json:"usage_count"`
	LastUsed      time.Time `json:"last_used"`
	Effectiveness []int     `json:"effectiveness_scores,omitempty"`
}

const defaultWriteInterval = 30 * time.Second

// NewUsageTracker creates a usage tracker that persists to the given path.
func NewUsageTracker(path string) *UsageTracker {
	return &UsageTracker{
		path:          path,
		data:          make(map[string]*skillUsage),
		writeInterval: defaultWriteInterval,
	}
}

// RecordUse increments the usage count and updates last_used for a skill.
func (ut *UsageTracker) RecordUse(name string) {
	ut.mu.Lock()
	defer ut.mu.Unlock()
	ut.ensureLoaded()

	entry := ut.getOrCreate(name)
	entry.UsageCount++
	entry.LastUsed = time.Now()
	ut.markDirty()
}

// RecordEffectiveness adds an effectiveness score (1-5) for a skill.
func (ut *UsageTracker) RecordEffectiveness(name string, score int) {
	ut.mu.Lock()
	defer ut.mu.Unlock()
	ut.ensureLoaded()

	entry := ut.getOrCreate(name)
	entry.Effectiveness = append(entry.Effectiveness, score)
	// Keep only last 10 scores
	if len(entry.Effectiveness) > 10 {
		entry.Effectiveness = entry.Effectiveness[len(entry.Effectiveness)-10:]
	}
	ut.markDirty()
}

// GetUsage returns usage data for a skill.
func (ut *UsageTracker) GetUsage(name string) (count int, lastUsed time.Time, avgScore float64) {
	ut.mu.Lock()
	defer ut.mu.Unlock()
	ut.ensureLoaded()

	entry, ok := ut.data[name]
	if !ok {
		return 0, time.Time{}, 0
	}
	return entry.UsageCount, entry.LastUsed, entry.avgScore()
}

// IsStale returns true if a skill hasn't been used for the given duration.
func (ut *UsageTracker) IsStale(name string, threshold time.Duration) bool {
	ut.mu.Lock()
	defer ut.mu.Unlock()
	ut.ensureLoaded()

	entry, ok := ut.data[name]
	if !ok {
		return true // never used = stale
	}
	if entry.UsageCount == 0 {
		return true
	}
	return time.Since(entry.LastUsed) > threshold
}

// AllUsage returns a snapshot of all usage data.
func (ut *UsageTracker) AllUsage() map[string]skillUsage {
	ut.mu.Lock()
	defer ut.mu.Unlock()
	ut.ensureLoaded()

	result := make(map[string]skillUsage, len(ut.data))
	for k, v := range ut.data {
		result[k] = *v
	}
	return result
}

// Flush persists dirty data to disk. Called periodically by the scheduler.
func (ut *UsageTracker) Flush() {
	ut.mu.Lock()
	defer ut.mu.Unlock()
	if !ut.dirty {
		return
	}
	ut.saveLocked()
}

// EnsureDir creates the parent directory for the usage file.
func (ut *UsageTracker) EnsureDir() error {
	return os.MkdirAll(filepath.Dir(ut.path), 0755)
}

func (ut *UsageTracker) getOrCreate(name string) *skillUsage {
	if entry, ok := ut.data[name]; ok {
		return entry
	}
	entry := &skillUsage{}
	ut.data[name] = entry
	return entry
}

func (su *skillUsage) avgScore() float64 {
	if len(su.Effectiveness) == 0 {
		return 0
	}
	sum := 0
	for _, s := range su.Effectiveness {
		sum += s
	}
	return float64(sum) / float64(len(su.Effectiveness))
}

func (ut *UsageTracker) ensureLoaded() {
	if ut.loaded {
		return
	}
	ut.loaded = true

	data, err := os.ReadFile(ut.path)
	if err != nil {
		return
	}
	json.Unmarshal(data, &ut.data)
}

// markDirty flags data as needing persistence. Saves immediately if enough
// time has passed since last write; otherwise defers until next Flush().
func (ut *UsageTracker) markDirty() {
	ut.dirty = true
	if time.Since(ut.lastWrite) >= ut.writeInterval {
		ut.saveLocked()
	}
}

func (ut *UsageTracker) saveLocked() {
	data, err := json.MarshalIndent(ut.data, "", "  ")
	if err != nil {
		return
	}
	if err := os.WriteFile(ut.path, data, 0600); err == nil {
		ut.dirty = false
		ut.lastWrite = time.Now()
	}
}
