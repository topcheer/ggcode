package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
)

// r462 shadow-holdout counterfactual ledger.
//
// r461's outcome loop (InjectedRuns/AfterSuccess/AfterFail) measures the
// injection arm only: "success rate after injection was high" is
// correlation, not causation - the runs might have succeeded regardless
// of the injected insight. r462 adds the missing control arm: for each
// category exactly one candidate insight is silently held out (skipped
// at prompt-render time) per day. Its runs' outcomes feed the holdout
// ledger, and once both arms have enough samples the delta decides:
//
//	Δ = injectArmRate − holdoutArmRate
//	Δ <= 0   → no positive contribution; stored Confidence decays one
//	           trajConfidenceStep through the r459 channel (below the
//	           injection floor the existing r459/r461 gates retire it -
//	           no new eviction logic here).
//	Δ >= 10pp → the insight demonstrably helps; release the holdout so
//	           it resumes injecting.
//
// Selection is deterministic: slot = hashStable(day, category) over the
// category's candidates sorted by key - epoch is the natural day, so the
// held insight rotates without random jitter, and repeated renders
// within one run agree. The ledger is workspace-shared, guarded by the
// same #1512 cross-process flock discipline as the learning store.

const (
	// trajHoldoutMinRuns is the sample floor on the control arm before
	// any verdict is allowed.
	trajHoldoutMinRuns = 5
	// trajHoldoutReleaseDelta is the Δ above which the holdout is
	// released as demonstrably helpful.
	trajHoldoutReleaseDelta = 0.10
	// trajHoldoutMinInjected guards the verdict against unmeasured
	// injection arms (e.g. global-tier entries absent from the local
	// store, whose InjectedRuns read 0): below this the delta would
	// compare against noise, so the entry stays held and unjudged.
	trajHoldoutMinInjected = 3
	// trajHoldoutDecayFloor is the lower clamp for the verdict decay.
	trajHoldoutDecayFloor = 0.05
	// trajHoldoutReleaseWindow (#3275): how long a released key is
	// suppressed from holdout re-claim before it may re-enter rotation.
	// One week balances "let the verdict breathe" against never
	// re-measuring; decayed entries usually fall below the injection
	// confidence floor anyway.
	trajHoldoutReleaseWindow = 7 * 24 * time.Hour
)

// trajHoldoutDay is the epoch-granularity clock (days). Var so tests can
// pin the rotation day deterministically.
var trajHoldoutDay = func() int64 { return time.Now().UTC().Unix() / 86400 }

// trajHoldoutEnabled gates the control arm globally. Production default
// on; r461-era injection-arm tests disable it because their fixtures seed
// a single candidate per category, which the holdout would always claim -
// those tests assert injection-arm behavior that is orthogonal to (and
// must not be coupled to) the control arm.
var trajHoldoutEnabled = true

// trajHoldoutEntry is one row of .ggcode/trajectory-holdout.jsonl: the
// currently (or last) held-out insight of a category plus its control-
// arm counters. One entry per category.
type trajHoldoutEntry struct {
	InsightKey       string    `json:"insight_key"` // cat + "\x00" + typ
	Category         string    `json:"category"`
	Type             string    `json:"type"`
	HeldSince        time.Time `json:"held_since"`
	HoldoutRuns      int       `json:"holdout_runs"`
	HoldoutSuccesses int       `json:"holdout_successes"`
	// ReleasedUntil (#3275): after a verdict releases this key, the entry
	// stays in the ledger marked with a suppression window instead of
	// being deleted - deleting let the next trajHoldoutSelect rebuild the
	// row by slot and re-claim the same key, making release a no-op.
	ReleasedUntil time.Time `json:"released_until,omitempty"`
}

func trajHoldoutPath(workingDir string) string {
	return filepath.Join(workingDir, ".ggcode", "trajectory-holdout.jsonl")
}

func trajHoldoutKeyString(k trajKey) string { return k.cat + "\x00" + k.typ }

// loadHoldoutLedger reads the ledger best-effort; missing file is empty.
func loadHoldoutLedger(path string) ([]trajHoldoutEntry, error) {
	entries, err := loadRawHoldout(path)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	return entries, nil
}

// trajHoldoutSelect computes the set of keys to hold out this day and
// persists the rotation to the ledger under the cross-process lock.
// Same-key streaks across days keep their counters; a rotation resets
// them. Best effort: on any lock/IO error it returns nil (fail open -
// injection behavior is never blocked by the control arm).
func trajHoldoutSelect(workingDir string, entries []trajectoryLearning) map[trajKey]bool {
	if !trajHoldoutEnabled || workingDir == "" || len(entries) == 0 {
		return nil
	}
	// Candidates per category, deterministic key order. Only entries the
	// injection arm would actually use (confidence gate + not retired by
	// the r461 effectiveness gate) enter rotation: holding out an entry
	// that never injects measures nothing.
	//
	// #3275 fix: entries with InjectedRuns == 0 are excluded - a holdout
	// verdict needs >= trajHoldoutMinInjected injection-arm samples to
	// compare against, so claiming a never-injected insight freezes its
	// InjectedRuns at 0 and deadlocks the verdict forever. New insights
	// must pass through the injection arm first.
	byCat := map[string][]trajKey{}
	injectedOnce := map[trajKey]bool{}
	for _, l := range entries {
		if l.EffectiveConfidence() < trajPromptMinConfidence || effectivenessGated(l) {
			continue
		}
		k := trajKeyOf(l)
		if l.InjectedRuns > 0 {
			injectedOnce[k] = true
		}
		if _, seen := containsKey(byCat[k.cat], k); !seen {
			byCat[k.cat] = append(byCat[k.cat], k)
		}
	}
	if len(byCat) == 0 {
		return nil
	}
	day := trajHoldoutDay()
	held := map[trajKey]bool{}

	path := trajHoldoutPath(workingDir)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil
	}
	unlock, lockErr := lockTrajFile(path + ".lock")
	if lockErr != nil {
		return nil
	}
	defer unlock()

	ledger, _ := loadHoldoutLedger(path)
	// #3284: the ledger is multi-row per category (keyed by insight key).
	// The pre-fix single-row-per-category map silently discarded rows on
	// the L202 full rewrite, which (a) killed the #3275 released-key
	// suppression window on the first subsequent select (the pause path
	// dropped the row, the next rotation re-claimed the key blind) and
	// (b) reset control-arm counters on every rotation day, so sparse
	// workspaces could never reach HoldoutRuns >= trajHoldoutMinRuns.
	byCatLedger := map[string]map[string]trajHoldoutEntry{}
	for _, e := range ledger {
		if byCatLedger[e.Category] == nil {
			byCatLedger[e.Category] = map[string]trajHoldoutEntry{}
		}
		byCatLedger[e.Category][e.InsightKey] = e
	}
	now := time.Now().UTC()
	var out []trajHoldoutEntry
	for _, cat := range sortedKeys(byCat) {
		keys := byCat[cat]
		// #3275 fix (a): keep only keys that already have injection-arm
		// samples - never hold out a never-injected insight (verdict
		// deadlock) ...
		var eligible []trajKey
		for _, k := range keys {
			if injectedOnce[k] {
				eligible = append(eligible, k)
			}
		}
		// ... and (b) a lone candidate has no rotation and no counterfactual
		// meaning: holdout measures "this insight withheld vs present", and
		// with exactly one candidate per category (the production norm -
		// consolidation merges (cat,typ) pairs 1:1) the slot is always 0 and
		// the sole insight is claimed forever. Skip such categories entirely.
		if len(eligible) <= 1 {
			continue
		}
		keys = eligible
		sort.Slice(keys, func(i, j int) bool { return trajHoldoutKeyString(keys[i]) < trajHoldoutKeyString(keys[j]) })
		target := keys[day%int64(len(keys))]
		tks := trajHoldoutKeyString(target)
		prev, ok := byCatLedger[cat][tks]
		// #3275 fix (c): honor the release suppression window. A released
		// key that rotates back in before its window expires must not be
		// re-claimed (release was a no-op before this check existed).
		// #3284: the released row SURVIVES the rewrite - the pause path
		// appends prev so the window keeps working on every subsequent
		// select until it expires.
		if ok && prev.ReleasedUntil.After(now) {
			out = append(out, prev) // category pauses holdout today; full injection resumes
			continue
		}
		held[target] = true
		if ok {
			prev.HeldSince = now // streak continues, counters preserved
			prev.ReleasedUntil = time.Time{}
			out = append(out, prev)
		} else {
			out = append(out, trajHoldoutEntry{
				InsightKey: tks,
				Category:   cat, Type: target.typ,
				HeldSince: now,
			})
		}
		// #3284: preserve every other row of this category (counters of
		// keys not currently held, and rows still inside their release
		// window) - cross-day counter accumulation now survives rotation.
		for k, e := range byCatLedger[cat] {
			if k != tks {
				out = append(out, e)
			}
		}
	}
	if err := writeHoldoutLedger(path, out); err != nil {
		return nil
	}
	return held
}

// recordHoldoutLocked mirrors recordInjectedLocked for the control arm:
// the key was skipped by the holdout during THIS run's prompt render.
// Caller must hold s.mu; set semantics keep repeated renders idempotent.
func (s *trajIntelState) recordHoldoutLocked(l trajectoryLearning) {
	if s.holdoutThisRun == nil {
		s.holdoutThisRun = make(map[trajKey]bool)
	}
	s.holdoutThisRun[trajKeyOf(l)] = true
}

// recordHoldoutOutcome (r462) closes the control-arm loop at run end:
// every key held out of this run's prompt gets HoldoutRuns++ (and
// HoldoutSuccesses++ on success), then each matured entry (>= min runs)
// faces the counterfactual verdict against the r461 injection counters.
// Non-positive delta decays stored Confidence via the shared
// rewriteAllLocked channel (touching ONLY Confidence - never the r461
// counter fields); a >=10pp delta or a completed negative verdict
// releases the holdout. Best effort, never blocks the run.
func (s *trajIntelState) recordHoldoutOutcome(workingDir string, success bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.holdoutThisRun) == 0 {
		return
	}
	keys := s.holdoutThisRun
	s.holdoutThisRun = nil
	if workingDir == "" {
		return
	}
	if s.filePath == "" {
		s.filePath = filepath.Join(workingDir, ".ggcode", "trajectory-learnings.jsonl")
	}
	hp := trajHoldoutPath(workingDir)
	if err := os.MkdirAll(filepath.Dir(hp), 0o755); err != nil {
		return
	}
	unlock, lockErr := lockTrajFile(hp + ".lock")
	if lockErr != nil {
		return
	}
	defer unlock()

	ledger, _ := loadHoldoutLedger(hp)
	var learnings []trajectoryLearning
	if ls, err := s.loadFromFile(); err == nil {
		learnings = ls
	}
	decayed := map[trajKey]bool{}
	var out []trajHoldoutEntry
	for _, e := range ledger {
		k := trajKey{cat: e.Category, typ: e.Type}
		if keys[k] {
			e.HoldoutRuns++
			if success {
				e.HoldoutSuccesses++
			}
		}
		if e.HoldoutRuns >= trajHoldoutMinRuns && e.HoldoutRuns > 0 {
			var inj *trajectoryLearning
			for i := range learnings {
				if trajKeyOf(learnings[i]) == k {
					inj = &learnings[i]
					break
				}
			}
			if inj != nil && inj.InjectedRuns >= trajHoldoutMinInjected {
				delta := float64(inj.AfterSuccess)/float64(inj.InjectedRuns) -
					float64(e.HoldoutSuccesses)/float64(e.HoldoutRuns)
				if delta <= 0 {
					decayed[k] = true // decay, then release
					// #3275: release = suppression window, NOT deletion. The
					// ledger row must persist (with counters) so the next
					// select cannot blindly re-claim the same key by slot.
					e.ReleasedUntil = time.Now().UTC().Add(trajHoldoutReleaseWindow)
					out = append(out, e)
					continue
				}
				if delta >= trajHoldoutReleaseDelta {
					// #3275: same suppression-window release as above.
					e.ReleasedUntil = time.Now().UTC().Add(trajHoldoutReleaseWindow)
					out = append(out, e)
					continue
				}
			}
		}
		out = append(out, e)
	}
	if err := writeHoldoutLedger(hp, out); err != nil {
		return
	}
	if len(decayed) == 0 {
		return
	}
	if err := s.rewriteAllLocked(func(existing []trajectoryLearning, loadErr error) ([]trajectoryLearning, error) {
		if loadErr != nil && !os.IsNotExist(loadErr) {
			return nil, fmt.Errorf("load: %w", loadErr)
		}
		changed := false
		for i := range existing {
			if !decayed[trajKeyOf(existing[i])] {
				continue
			}
			c := existing[i].Confidence - trajConfidenceStep
			if c < trajHoldoutDecayFloor {
				c = trajHoldoutDecayFloor
			}
			existing[i].Confidence = c
			changed = true
		}
		if !changed {
			return nil, nil
		}
		return existing, nil
	}); err != nil {
		debug.Log("traj-holdout", "verdict decay failed: %v", err)
	}
}

// trajHoldoutSnapshot returns the current ledger for display (/traj).
// Read-only; atomic-rename writes make unlocked reads consistent-enough.
func trajHoldoutSnapshot(workingDir string) map[trajKey]trajHoldoutEntry {
	out := map[trajKey]trajHoldoutEntry{}
	entries, err := loadHoldoutLedger(trajHoldoutPath(workingDir))
	if err != nil {
		return out
	}
	for _, e := range entries {
		out[trajKey{cat: e.Category, typ: e.Type}] = e
	}
	return out
}

// trajHoldoutDelta computes Δ (injection-arm rate − holdout-arm rate)
// for a learning paired with its ledger entry. Returns ok=false when
// either arm is unmeasured (no holdout runs, or InjectedRuns below the
// guard).
func trajHoldoutDelta(l trajectoryLearning, e trajHoldoutEntry) (delta float64, ok bool) {
	if e.HoldoutRuns <= 0 || l.InjectedRuns < trajHoldoutMinInjected {
		return 0, false
	}
	return float64(l.AfterSuccess)/float64(l.InjectedRuns) -
		float64(e.HoldoutSuccesses)/float64(e.HoldoutRuns), true
}

// --- ledger file IO (JSONL, atomic tmp+rename, same shape as the store) ---

func loadRawHoldout(path string) ([]trajHoldoutEntry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var out []trajHoldoutEntry
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var e trajHoldoutEntry
		if json.Unmarshal([]byte(line), &e) == nil {
			out = append(out, e)
		}
	}
	return out, nil
}

func writeHoldoutLedger(path string, entries []trajHoldoutEntry) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmpF, err := os.CreateTemp(filepath.Dir(path), ".traj-holdout-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmpF.Name()
	enc := json.NewEncoder(tmpF)
	for _, e := range entries {
		if encErr := enc.Encode(e); encErr != nil {
			tmpF.Close()
			os.Remove(tmpPath)
			return encErr
		}
	}
	if closeErr := tmpF.Close(); closeErr != nil {
		os.Remove(tmpPath)
		return closeErr
	}
	return os.Rename(tmpPath, path)
}

// --- small helpers kept local to avoid touching existing files ---

func containsKey(keys []trajKey, k trajKey) (int, bool) {
	for i, kk := range keys {
		if kk == k {
			return i, true
		}
	}
	return 0, false
}

func sortedKeys(m map[string][]trajKey) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
