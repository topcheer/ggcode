package agent

// Cross-Session Guidance Promoter -- Inter-Test-Time Experience Accumulation
//
// Research basis:
//   - Self-Evolving Agents Survey (arXiv:2507.21046, 2025): identifies
//     "inter-test-time evolution" as a key dimension -- agents should learn
//     BETWEEN task completions, not just during them.
//   - ExpeL (Zhao et al., 2024): collects trajectories and converts them
//     into reusable natural-language insights and rules.
//   - AutoGuide (Fu et al., 2024): compresses offline logs into concise,
//     conditional, context-aware guidelines injected proactively.
//   - Memento / AgentFly (arXiv:2508.16153): memory-based continual
//     adaptation without fine-tuning -- agents learn from past successes
//     and failures stored in episodic memory.
//
// Problem: ggcode has 100+ intra-test-time detectors that fire reactively
// DURING a run. When the SAME detector fires across MULTIPLE sessions
// (e.g., the agent repeatedly makes "silent error advancement" mistakes,
// or repeatedly enters "analysis paralysis"), each new session rediscovers
// the same problem from scratch. The reactive guidance arrives too late --
// after the mistake has already been made.
//
// Gap: No mechanism promotes recurring reactive guidance into proactive
// rules. The agent never "learns its lesson" between sessions. This is
// the inter-test-time evolution gap identified by the survey.
//
// Design:
//   - Records each guidance tag that fires during a run
//   - Persists per-tag recurrence counts to .ggcode/guidance-recurrence.json
//   - When a tag fires in N distinct sessions (threshold=3), it is promoted
//     to a proactive one-liner injected into the system prompt at run start
//   - Promoted reminders are concise (one line each), capped at 5 total
//   - Decay: entries not seen in 30 days are pruned
//   - Zero LLM cost -- pure deterministic tracking + text injection

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
)

const (
	// guidancePromoteThreshold: distinct sessions a tag must fire in
	// before it is promoted to proactive injection.
	guidancePromoteThreshold = 3

	// guidancePromoteMaxEntries: maximum promoted entries to inject.
	guidancePromoteMaxEntries = 5

	// guidancePromoteDecayDays: entries not seen in this many days are pruned.
	guidancePromoteDecayDays = 30

	// guidancePromoteStalePromotedDays: promoted entries not seen in this
	// many days are demoted (stop injecting).
	guidancePromoteStalePromotedDays = 14
)

// GuidanceRecurrenceEntry tracks how often a guidance tag recurs across sessions.
type GuidanceRecurrenceEntry struct {
	Tag          string    `json:"tag"`
	FirstSeen    time.Time `json:"first_seen"`
	LastSeen     time.Time `json:"last_seen"`
	SessionCount int       `json:"session_count"` // distinct sessions where this tag fired
	TotalFires   int       `json:"total_fires"`   // total firings across all sessions/iterations
	Promoted     bool      `json:"promoted"`      // true when promoted to proactive injection
	PromotedAt   time.Time `json:"promoted_at"`   // when promotion happened (for demotion)
	Sessions     []string  `json:"sessions"`      // session IDs where this tag fired (capped)
}

// GuidancePromoter tracks cross-session guidance tag recurrence and promotes
// frequently recurring tags to proactive system-prompt reminders.
type GuidancePromoter struct {
	mu           sync.Mutex
	path         string
	sessionID    string
	entries      map[string]*GuidanceRecurrenceEntry
	firedThisRun map[string]bool // tags that fired in the current run
	loaded       bool
}

// NewGuidancePromoter creates a promoter for the given working directory.
// Returns nil if workingDir is empty.
func NewGuidancePromoter(workingDir, sessionID string) *GuidancePromoter {
	if workingDir == "" {
		return nil
	}
	return &GuidancePromoter{
		path:         filepath.Join(workingDir, ".ggcode", "guidance-recurrence.json"),
		sessionID:    sessionID,
		entries:      make(map[string]*GuidanceRecurrenceEntry),
		firedThisRun: make(map[string]bool),
	}
}

func (gp *GuidancePromoter) load() {
	if gp == nil || gp.loaded || gp.path == "" {
		return
	}
	gp.loaded = true
	data, err := os.ReadFile(gp.path)
	if err != nil {
		return // first run
	}
	var entries []*GuidanceRecurrenceEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		debug.Log("guidance-promoter", "failed to load recurrence data: %v", err)
		return
	}
	for _, e := range entries {
		gp.entries[e.Tag] = e
	}
}

func (gp *GuidancePromoter) save() {
	if gp == nil || gp.path == "" {
		return
	}
	dir := filepath.Dir(gp.path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		debug.Log("guidance-promoter", "failed to create dir: %v", err)
		return
	}
	entries := make([]*GuidanceRecurrenceEntry, 0, len(gp.entries))
	for _, e := range gp.entries {
		entries = append(entries, e)
	}
	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		debug.Log("guidance-promoter", "failed to marshal: %v", err)
		return
	}
	tmp := gp.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		debug.Log("guidance-promoter", "failed to write tmp: %v", err)
		return
	}
	if err := os.Rename(tmp, gp.path); err != nil {
		debug.Log("guidance-promoter", "failed to rename: %v", err)
	}
}

// RecordTag marks that a guidance tag fired in the current run.
// Called after coalesceGuidance processes hints.
func (gp *GuidancePromoter) RecordTag(tag string) {
	if gp == nil || tag == "" {
		return
	}
	gp.mu.Lock()
	defer gp.mu.Unlock()
	gp.load()
	gp.firedThisRun[tag] = true
}

// RunStartHook loads data and returns proactive reminders for promoted tags.
// Called at the beginning of each run to inject brief one-liner reminders
// into the system prompt.
func (gp *GuidancePromoter) RunStartHook() string {
	if gp == nil {
		return ""
	}
	gp.mu.Lock()
	defer gp.mu.Unlock()
	gp.load()

	// Prune stale entries.
	gp.pruneStale()

	var promoted []*GuidanceRecurrenceEntry
	for _, e := range gp.entries {
		if e.Promoted && !gp.isStalePromoted(e) {
			promoted = append(promoted, e)
		}
	}
	if len(promoted) == 0 {
		return ""
	}

	// Sort by session count descending (most frequent first).
	sort.Slice(promoted, func(i, j int) bool {
		return promoted[i].SessionCount > promoted[j].SessionCount
	})
	if len(promoted) > guidancePromoteMaxEntries {
		promoted = promoted[:guidancePromoteMaxEntries]
	}

	var lines []string
	for _, e := range promoted {
		lines = append(lines, fmt.Sprintf("- [%s] has recurred across %d sessions -- proactively avoid this pattern.", e.Tag, e.SessionCount))
	}

	return "## Recurring Agent Patterns (learned from past sessions)\n" +
		"The following guidance tags have fired repeatedly across sessions. " +
		"Proactively avoid these patterns:\n" +
		strings.Join(lines, "\n")
}

// RunEndHook finalizes the current run: updates session counts, promotes
// tags that crossed the threshold, and persists. Called when a run completes.
func (gp *GuidancePromoter) RunEndHook() {
	if gp == nil {
		return
	}
	gp.mu.Lock()
	defer gp.mu.Unlock()
	gp.load()

	now := time.Now()
	for firedTag := range gp.firedThisRun {
		entry, exists := gp.entries[firedTag]
		if !exists {
			entry = &GuidanceRecurrenceEntry{
				Tag:       firedTag,
				FirstSeen: now,
				Sessions:  []string{},
			}
			gp.entries[firedTag] = entry
		}
		entry.LastSeen = now
		entry.TotalFires++

		// Track distinct sessions (cap at 50 to bound growth).
		if !sliceContains(entry.Sessions, gp.sessionID) && len(entry.Sessions) < 50 {
			entry.Sessions = append(entry.Sessions, gp.sessionID)
			entry.SessionCount++
		}

		// Promote when threshold is crossed.
		if !entry.Promoted && entry.SessionCount >= guidancePromoteThreshold {
			entry.Promoted = true
			entry.PromotedAt = now
			debug.Log("guidance-promoter", "promoted tag %s after %d sessions", firedTag, entry.SessionCount)
		}
	}

	gp.pruneStale()
	gp.save()

	// Reset for next run.
	gp.firedThisRun = make(map[string]bool)
}

// pruneStale removes entries not seen in guidancePromoteDecayDays.
// Promoted entries not seen in guidancePromoteStalePromotedDays are demoted.
func (gp *GuidancePromoter) pruneStale() {
	if gp == nil {
		return
	}
	now := time.Now()
	decayCutoff := now.AddDate(0, 0, -guidancePromoteDecayDays)
	stalePromotedCutoff := now.AddDate(0, 0, -guidancePromoteStalePromotedDays)

	for staleTag, staleEntry := range gp.entries {
		if staleEntry.LastSeen.Before(decayCutoff) {
			delete(gp.entries, staleTag)
			continue
		}
		// Demote promoted entries that haven't fired recently.
		if staleEntry.Promoted && staleEntry.LastSeen.Before(stalePromotedCutoff) {
			staleEntry.Promoted = false
			debug.Log("guidance-promoter", "demoted stale tag %s", staleTag)
		}
	}
}

func (gp *GuidancePromoter) isStalePromoted(e *GuidanceRecurrenceEntry) bool {
	cutoff := time.Now().AddDate(0, 0, -guidancePromoteStalePromotedDays)
	return e.LastSeen.Before(cutoff)
}

func sliceContains(slice []string, s string) bool {
	for _, v := range slice {
		if v == s {
			return true
		}
	}
	return false
}
