package agent

// Weakness Signal Router (sa-112, CoEvolve-inspired).
//
// Frontier basis: CoEvolve "Training LLM Agents via Agent-Data Mutual
// Evolution" (arXiv:2604.15840, ACL 2026) extracts three weakness signal
// classes from agent trajectories - forgetting (corrected behavior
// relapses), boundary (failures concentrated on one interaction boundary),
// rare (long-tail scenarios) - and routes each class to a different
// evolution action. FlowEvo shows the training-free variant: assets (not
// weights) evolve at inference time.
//
// ggcode had every DETECTOR (drift_recurrence, constraint_amnesia,
// repetition_tracker, error_classifier) but each fired only a run-local
// advisory that evaporated at run end; nothing persisted a cross-run
// weakness profile or routed a weakness class to a matching asset action
// (sa-112 double empty-check: WeaknessClass / sa-83's
// suggestSkillRevisionFromFailure both zero hits repo-wide). This file
// adds the missing layer:
//
//	run end -> classify signals (deterministic, zero LLM cost)
//	        -> merge into <workingDir>/.ggcode/weakness_signals.json
//	        -> route mature signals to project memory
//	           forgetting -> "enforce:" rule line   (memory-auto-injection
//                                                already feeds prompts)
//	           boundary   -> "boundary:" skill-candidate line
//                                           (knight WriteStaging upgrade
//                                            left as sa-83 1c follow-up)
//	           rare       -> archive only (case-library write deferred;
//                         a store nobody reads is worse than no store)
//
// All routing lands in project memory via AutoMemory (provenance-tagged,
// atomically written, #1752/#775 cross-process safe), so the user sees
// and edits the evolved assets through the existing memory UX.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/memory"
)

// WeaknessClass is the CoEvolve signal taxonomy.
type WeaknessClass int

const (
	// WeakRare is a first-seen unique failure fingerprint: archived but
	// not routed (long-tail evidence accumulates until it recurs).
	WeakRare WeaknessClass = iota
	// WeakForgetting marks corrected behavior that relapsed - the agent
	// was warned mid-run and still failed, or failed the same file twice.
	WeakForgetting
	// WeakBoundary marks failures concentrated on one interaction
	// boundary, keyed by error-classifier category (edit anchor rules,
	// shell compat, ...).
	WeakBoundary
)

func (c WeaknessClass) String() string {
	switch c {
	case WeakForgetting:
		return "forgetting"
	case WeakBoundary:
		return "boundary"
	default:
		return "rare"
	}
}

// weaknessSignalStore maps fingerprint -> cross-run record. Fingerprint
// forms: "drift-recurrence", "constraint-amnesia", "edit-fail:<file>",
// "errcat:<category>".
type weaknessSignalStore map[string]weaknessRecord

type weaknessRecord struct {
	Class    WeaknessClass `json:"class"`
	Count    int           `json:"count"`
	Evidence string        `json:"evidence,omitempty"`
	LastTS   time.Time     `json:"last_ts"`
	// Routed marks that the mature-signal action already landed in project
	// memory; re-routing on every later run would spam identical lines.
	Routed bool `json:"routed,omitempty"`
}

const (
	// weaknessRouteThreshold is the cross-run count at which a signal is
	// considered mature and routed (matches drift/dial confirm-then-act
	// semantics: one occurrence can be noise, two is a pattern).
	weaknessRouteThreshold = 2
	// weaknessStoreFile lives under the project's .ggcode/ next to memory/.
	weaknessStoreFile = "weakness_signals.json"
	// weaknessMemoryKey is the project-memory key receiving routed lines.
	weaknessMemoryKey = "weakness-signals"
	// weaknessMaxEntries caps the store; oldest LastTS entries evicted on
	// save so a long-lived project cannot grow it unboundedly.
	weaknessMaxEntries = 64
	// weaknessAge is the staleness horizon: a fingerprint unseen for this
	// long stops counting (the weakness was presumably fixed).
	weaknessAge = 30 * 24 * time.Hour
)

// runSignal is one weakness observation collected at run end.
type runSignal struct {
	fingerprint string
	class       WeaknessClass
	evidence    string
}

// collectWeaknessSignals snapshots the run's detector states into signals.
// Runs on the agent loop goroutine at run end, after all tool execution,
// but takes the mutex-guarded snapshots anyway: parallel tool batches and
// the IM bridge can touch the classifiers from other goroutines mid-run.
func (a *Agent) collectWeaknessSignals() []runSignal {
	var out []runSignal
	if a.driftRecurrence != nil && a.driftRecurrence.fired {
		out = append(out, runSignal{"drift-recurrence", WeakForgetting,
			"scope drift recurred after in-run warning"})
	}
	if a.constraintAmnesia != nil && a.constraintAmnesia.warnings > 0 {
		out = append(out, runSignal{"constraint-amnesia", WeakForgetting,
			"user-set constraint violated after being recorded"})
	}
	if a.repetition != nil {
		for file, n := range a.repetition.failedEditSnapshot() {
			fp := "edit-fail:" + file
			switch {
			case n >= 2:
				out = append(out, runSignal{fp, WeakForgetting,
					fmt.Sprintf("%d failed edits to one file in one run", n)})
			case n == 1:
				// Isolated single failure: rare until it repeats.
				out = append(out, runSignal{fp, WeakRare,
					"isolated failed edit"})
			}
		}
	}
	if a.errorClassifier != nil {
		for _, name := range a.errorClassifier.firedCategories() {
			out = append(out, runSignal{"errcat:" + name, WeakBoundary,
				"classified error category fired this run"})
		}
	}
	return out
}

// routeWeaknessSignals is the run-end entry point: collect, merge into the
// persistent store, then route mature signals into project memory.
// Best-effort throughout - a weakness pipeline must never fail a run.
func (a *Agent) routeWeaknessSignals() {
	defer func() {
		if r := recover(); r != nil {
			debug.Log("weakness", "router panicked (recovered): %v", r)
		}
	}()
	wd := a.workingDir
	if wd == "" {
		return
	}
	signals := a.collectWeaknessSignals()
	store := loadWeaknessStore(filepath.Join(wd, ".ggcode", weaknessStoreFile))
	changed := false
	now := time.Now()
	for _, s := range signals {
		rec := store[s.fingerprint]
		if rec.Count == 0 || now.Sub(rec.LastTS) > weaknessAge {
			// First sighting, or stale enough to restart the count:
			// the class is re-seeded by this run's observation.
			rec = weaknessRecord{Class: s.class, Evidence: s.evidence, LastTS: now, Count: 1}
		} else {
			rec.Count++
			rec.LastTS = now
			// Rare upgrades to the class its fingerprint implies once it
			// repeats (a rare failure that recurs is no longer rare).
			if rec.Class == WeakRare {
				switch {
				case strings.HasPrefix(s.fingerprint, "errcat:"):
					rec.Class = WeakBoundary
				default:
					rec.Class = WeakForgetting
				}
			}
		}
		store[s.fingerprint] = rec
		changed = true
	}
	if !changed {
		return
	}
	evictWeaknessStore(store, weaknessMaxEntries)
	if err := saveWeaknessStore(filepath.Join(wd, ".ggcode", weaknessStoreFile), store); err != nil {
		debug.Log("weakness", "store save failed: %v", err)
		return
	}
	routeMatureSignals(wd, store)
}

// routeMatureSignals writes one line per mature-and-unrouted signal into
// project memory (key weakness-signals). Existing lines are deduped by
// fingerprint prefix so re-routing after a manual memory edit still cannot
// duplicate a line.
func routeMatureSignals(workingDir string, store weaknessSignalStore) {
	auto := memory.NewProjectAutoMemory(workingDir)
	if auto == nil {
		return
	}
	var lines []string
	for fp, rec := range store {
		if rec.Count < weaknessRouteThreshold || rec.Routed || rec.Class == WeakRare {
			continue
		}
		switch rec.Class {
		case WeakForgetting:
			lines = append(lines, fmt.Sprintf("enforce: %s (%d runs) - %s; treat as a standing rule",
				fp, rec.Count, rec.Evidence))
		case WeakBoundary:
			lines = append(lines, fmt.Sprintf("boundary: %s (%d runs) - %s; skill-draft candidate (sa-83 1c)",
				fp, rec.Count, rec.Evidence))
		}
		store[fp] = weaknessRecord{Class: rec.Class, Count: rec.Count, Evidence: rec.Evidence, LastTS: rec.LastTS, Routed: true}
	}
	if len(lines) == 0 {
		return
	}
	existing, err := auto.LoadKey(weaknessMemoryKey)
	if err != nil {
		debug.Log("weakness", "memory load failed, skipping route: %v", err)
		return
	}
	for _, l := range lines {
		if strings.Contains(existing, l) {
			continue
		}
		existing = strings.TrimRight(existing, "\n") + "\n" + l
	}
	if err := auto.SaveMemoryWithSource(weaknessMemoryKey, strings.TrimLeft(existing, "\n"), "weakness-router"); err != nil {
		debug.Log("weakness", "memory save failed: %v", err)
		return
	}
	// Persist the Routed flags so the lines are not re-emitted next run.
	_ = saveWeaknessStore(filepath.Join(workingDir, ".ggcode", weaknessStoreFile), store)
	debug.Log("weakness", "routed %d mature signal(s) to project memory", len(lines))
}

// loadWeaknessStore reads the persistent store; missing or corrupt file
// yields an empty store (a weakness profile must never brick a run).
func loadWeaknessStore(path string) weaknessSignalStore {
	store := weaknessSignalStore{}
	b, err := os.ReadFile(path)
	if err != nil {
		return store
	}
	if err := json.Unmarshal(b, &store); err != nil {
		debug.Log("weakness", "corrupt store %s, starting fresh: %v", path, err)
		return weaknessSignalStore{}
	}
	// Age out stale fingerprints at load: unseen-for-30d weaknesses are
	// presumed fixed and stop counting toward maturity.
	now := time.Now()
	for fp, rec := range store {
		if now.Sub(rec.LastTS) > weaknessAge {
			delete(store, fp)
		}
	}
	return store
}

// saveWeaknessStore writes the store atomically (tmp+rename, matching the
// memory package's discipline).
func saveWeaknessStore(path string, store weaknessSignalStore) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(store, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// evictWeaknessStore drops the oldest entries beyond cap.
func evictWeaknessStore(store weaknessSignalStore, cap int) {
	if len(store) <= cap {
		return
	}
	type fpTS struct {
		fp string
		ts time.Time
	}
	all := make([]fpTS, 0, len(store))
	for fp, rec := range store {
		all = append(all, fpTS{fp, rec.LastTS})
	}
	// Simple selection: drop oldest until within cap.
	for len(store) > cap {
		var oldest fpTS
		first := true
		for _, e := range all {
			if _, ok := store[e.fp]; !ok {
				continue
			}
			if first || e.ts.Before(oldest.ts) {
				oldest, first = e, false
			}
		}
		if first {
			return
		}
		delete(store, oldest.fp)
	}
}
