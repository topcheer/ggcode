package knight

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/util"
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
	UsageCount          int       `json:"usage_count"`
	LastUsed            time.Time `json:"last_used"`
	PromptExposureCount int       `json:"prompt_exposure_count,omitempty"`
	LastPromptExposure  time.Time `json:"last_prompt_exposure,omitempty"`
	PromptSuccessCount  int       `json:"prompt_success_count,omitempty"`
	PromptFailureCount  int       `json:"prompt_failure_count,omitempty"`
	// Decayed counters retain a fractional, time-weighted view of the same
	// success/failure signal. They are the values consumed by the prompt-tuning
	// gates so historical noise eventually fades.
	PromptSuccessDecayed float64   `json:"prompt_success_decayed,omitempty"`
	PromptFailureDecayed float64   `json:"prompt_failure_decayed,omitempty"`
	LastPromptDecay      time.Time `json:"last_prompt_decay,omitempty"`
	Effectiveness        []int     `json:"effectiveness_scores,omitempty"`
}

const defaultWriteInterval = 30 * time.Second

// promptOutcomeHalfLife controls how quickly old prompt success/failure
// signals fade. A 30-day half-life means a 6-month-old failure contributes
// ~1/64 of its original weight, so historical noise stops dominating the
// gates that read PromptSuccessDecayed / PromptFailureDecayed.
const promptOutcomeHalfLife = 30 * 24 * time.Hour

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

// RecordPromptExposure increments the count of times a skill was listed in the
// system prompt. Exposure is not the same as use; it only proves the model could
// see the skill when deciding whether to invoke it.
func (ut *UsageTracker) RecordPromptExposure(name string) {
	ut.mu.Lock()
	defer ut.mu.Unlock()
	ut.ensureLoaded()

	entry := ut.getOrCreate(name)
	entry.PromptExposureCount++
	entry.LastPromptExposure = time.Now()
	ut.markDirty()
}

// RecordPromptOutcome records a weak task-level outcome for a skill that was
// visible in the prompt during a run. It is attribution, not proof of causality.
func (ut *UsageTracker) RecordPromptOutcome(name string, success bool) {
	ut.mu.Lock()
	defer ut.mu.Unlock()
	ut.ensureLoaded()

	entry := ut.getOrCreate(name)
	now := time.Now()
	entry.applyPromptDecayLocked(now)
	if success {
		entry.PromptSuccessCount++
		entry.PromptSuccessDecayed += 1
	} else {
		entry.PromptFailureCount++
		entry.PromptFailureDecayed += 1
	}
	entry.LastPromptDecay = now
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

// GetFeedback returns the average effectiveness score and sample count.
func (ut *UsageTracker) GetFeedback(name string) (avgScore float64, samples int) {
	ut.mu.Lock()
	defer ut.mu.Unlock()
	ut.ensureLoaded()

	entry, ok := ut.data[name]
	if !ok {
		return 0, 0
	}
	return entry.avgScore(), len(entry.Effectiveness)
}

// GetPromptExposure returns prompt exposure data for a skill.
func (ut *UsageTracker) GetPromptExposure(name string) (count int, lastExposed time.Time) {
	ut.mu.Lock()
	defer ut.mu.Unlock()
	ut.ensureLoaded()

	entry, ok := ut.data[name]
	if !ok {
		return 0, time.Time{}
	}
	return entry.PromptExposureCount, entry.LastPromptExposure
}

// GetPromptOutcome returns weak task-level outcome counts for prompt-visible use.
// The returned counts are the time-decayed values consumed by Knight's
// prompt-tuning gates: a stale historical failure no longer dominates the
// recent signal. Callers wanting raw lifetime counts should read
// PromptSuccessCount/PromptFailureCount via Snapshot.
func (ut *UsageTracker) GetPromptOutcome(name string) (successes int, failures int) {
	ut.mu.Lock()
	defer ut.mu.Unlock()
	ut.ensureLoaded()

	entry, ok := ut.data[name]
	if !ok {
		return 0, 0
	}
	entry.applyPromptDecayLocked(time.Now())
	return int(entry.PromptSuccessDecayed + 0.5), int(entry.PromptFailureDecayed + 0.5)
}

func (ut *UsageTracker) Snapshot(name string) (skillUsage, bool) {
	ut.mu.Lock()
	defer ut.mu.Unlock()
	ut.ensureLoaded()

	entry, ok := ut.data[name]
	if !ok {
		return skillUsage{}, false
	}
	entry.applyPromptDecayLocked(time.Now())
	return *entry, true
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

// applyPromptDecayLocked decays the prompt outcome counters toward zero based
// on the elapsed time since the last update. Callers must already hold the
// tracker mutex (or operate on a private snapshot during migration).
func (su *skillUsage) applyPromptDecayLocked(now time.Time) {
	if su == nil {
		return
	}
	// Backfill: existing entries that pre-date decay accounting use raw counts
	// once, then enter the decayed regime. This keeps historical data visible
	// without "double counting" a fresh signal.
	if su.LastPromptDecay.IsZero() {
		if su.PromptSuccessDecayed == 0 && su.PromptSuccessCount > 0 {
			su.PromptSuccessDecayed = float64(su.PromptSuccessCount)
		}
		if su.PromptFailureDecayed == 0 && su.PromptFailureCount > 0 {
			su.PromptFailureDecayed = float64(su.PromptFailureCount)
		}
		su.LastPromptDecay = now
		return
	}
	elapsed := now.Sub(su.LastPromptDecay)
	if elapsed <= 0 {
		return
	}
	if su.PromptSuccessDecayed == 0 && su.PromptFailureDecayed == 0 {
		su.LastPromptDecay = now
		return
	}
	factor := math.Pow(0.5, float64(elapsed)/float64(promptOutcomeHalfLife))
	su.PromptSuccessDecayed *= factor
	su.PromptFailureDecayed *= factor
	if su.PromptSuccessDecayed < 0.001 {
		su.PromptSuccessDecayed = 0
	}
	if su.PromptFailureDecayed < 0.001 {
		su.PromptFailureDecayed = 0
	}
	su.LastPromptDecay = now
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
	if err := util.AtomicWriteFile(ut.path, data, 0600); err == nil {
		ut.dirty = false
		ut.lastWrite = time.Now()
	}
}
