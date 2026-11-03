package knight

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/util"
)

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

	PromptSuccessDecayed float64   `json:"prompt_success_decayed,omitempty"`
	PromptFailureDecayed float64   `json:"prompt_failure_decayed,omitempty"`
	LastPromptDecay      time.Time `json:"last_prompt_decay,omitempty"`
	Effectiveness        []int     `json:"effectiveness_scores,omitempty"`
}

const defaultWriteInterval = 30 * time.Second

const promptOutcomeHalfLife = 30 * 24 * time.Hour

func NewUsageTracker(path string) *UsageTracker {
	return &UsageTracker{
		path:          path,
		data:          make(map[string]*skillUsage),
		writeInterval: defaultWriteInterval,
	}
}

func (ut *UsageTracker) RecordUse(name string) {
	ut.mu.Lock()
	defer ut.mu.Unlock()
	ut.ensureLoaded()

	entry := ut.getOrCreate(name)
	entry.UsageCount++
	entry.LastUsed = time.Now()
	ut.markDirty()
}

func (ut *UsageTracker) RecordPromptExposure(name string) {
	ut.mu.Lock()
	defer ut.mu.Unlock()
	ut.ensureLoaded()

	entry := ut.getOrCreate(name)
	entry.PromptExposureCount++
	entry.LastPromptExposure = time.Now()
	ut.markDirty()
}

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

func (ut *UsageTracker) RecordEffectiveness(name string, score int) {
	ut.mu.Lock()
	defer ut.mu.Unlock()
	ut.ensureLoaded()

	entry := ut.getOrCreate(name)
	entry.Effectiveness = append(entry.Effectiveness, score)

	if len(entry.Effectiveness) > 10 {
		entry.Effectiveness = entry.Effectiveness[len(entry.Effectiveness)-10:]
	}
	ut.markDirty()
}

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

func (ut *UsageTracker) IsStale(name string, threshold time.Duration) bool {
	ut.mu.Lock()
	defer ut.mu.Unlock()
	ut.ensureLoaded()

	entry, ok := ut.data[name]
	if !ok {
		return true
	}
	if entry.UsageCount == 0 {
		return true
	}
	return time.Since(entry.LastUsed) > threshold
}

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

func (ut *UsageTracker) Flush() {
	ut.mu.Lock()
	defer ut.mu.Unlock()
	if !ut.dirty {
		return
	}
	ut.saveLocked()
}

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

func (su *skillUsage) applyPromptDecayLocked(now time.Time) {
	if su == nil {
		return
	}

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
	data, err := os.ReadFile(ut.path)
	if err != nil {

		if os.IsNotExist(err) {
			ut.loaded = true
		} else {
			debug.Log("knight.usage", "read %s failed (will retry, refusing overwrite): %v", ut.path, err)
		}
		return
	}
	if err := json.Unmarshal(data, &ut.data); err != nil {

		debug.Log("knight.usage", "parse %s failed (refusing overwrite): %v", ut.path, err)
		return
	}
	ut.loaded = true
}

func (ut *UsageTracker) markDirty() {
	ut.dirty = true
	if time.Since(ut.lastWrite) >= ut.writeInterval {
		ut.saveLocked()
	}
}

func (ut *UsageTracker) saveLocked() {
	if !ut.loaded {

		debug.Log("knight.usage", "save skipped: %s never loaded successfully; refusing to overwrite history", ut.path)
		return
	}
	data, err := json.MarshalIndent(ut.data, "", "  ")
	if err != nil {
		return
	}
	if err := util.AtomicWriteFile(ut.path, data, 0600); err == nil {
		ut.dirty = false
		ut.lastWrite = time.Now()
	}
}
