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
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/memory"
	"github.com/topcheer/ggcode/internal/util"
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

// testFailLineRE matches `go test` verbose failure lines. One line per failed
// test (subtests carry a slash - kept verbatim, they are distinct tests).
var testFailLineRE = regexp.MustCompile(`^--- FAIL: (\S+)`)

// testPkgFailRE matches `go test` per-package failure summary lines
// (`FAIL	pkg/path	0.42s`). #3781: the package path is the only reliable
// package context for the `--- FAIL` lines that preceded it in the same
// package block, so fingerprints become pkg-qualified and same-named tests
// in different packages no longer collide into one count.
var testPkgFailRE = regexp.MustCompile("^FAIL\t(\\S+)")

// testFailCollector accumulates per-run go-test failure counts keyed by
// test name (r17: test signals drive evolution, arXiv 2608.03392 signals
// dimension - test failures are the strongest code-specific failure
// semantic, yet were absent from the fingerprint set).
type testFailCollector struct {
	mu     sync.Mutex
	counts map[string]int
	// degraded marks fingerprints whose package context could not be
	// resolved (no `FAIL\t<pkg>` summary in the output): they fall back to
	// the pre-#3781 bare-test-name key and are flagged as such (#3781).
	degraded map[string]bool
}

// goTestInvokedAsTokens reports whether the command invokes go test as
// adjacent whitespace-separated tokens ("... go test ..."). Quoted
// embeddings (awk '/go test/{...}', rg 'go test') glue the words into a
// single non-matching token, so log-reading commands are rejected (#3863 A).
func goTestInvokedAsTokens(cmd string) bool {
	fields := strings.Fields(cmd)
	for i := 0; i+1 < len(fields); i++ {
		if fields[i] == "go" && fields[i+1] == "test" {
			return true
		}
	}
	return false
}

func newTestFailCollector() *testFailCollector {
	return &testFailCollector{counts: map[string]int{}, degraded: map[string]bool{}}
}

// record parses run_command output for go-test failure lines. Called for
// every run_command result regardless of IsError: agents often suffix
// `|| true` and the tool then reports success while tests failed.
func (c *testFailCollector) record(cmd, output string) {
	// #3781 fix 2: only go-test runs count. A bare tool-name filter let
	// `cat`ing a log or `grep`ing a build artifact feed fake failure lines
	// into the weakness store.
	// #3863 A: a substring test still let read-log commands EMBED the words
	// in a quoted pattern (`awk '/go test/{f=1} f' ci.log`, `rg 'go test'`)
	// pass the gate and pipe archived FAIL lines in. Require adjacent
	// whitespace-separated TOKENS exactly "go" "test" - quoting the words
	// into a regex/argument glues them into one non-matching token.
	if !goTestInvokedAsTokens(cmd) {
		return // cat/grep/awk/rg over stale logs never counts (#3781, #3863)
	}
	if !strings.Contains(output, "--- FAIL:") {
		return // fast path: package-level failures without -v carry no test names
	}
	// #3781 fix 1: resolve package context for each `--- FAIL` line. go test
	// emits a package block (test lines) followed by its `FAIL\tpkg`
	// summary, so pending failures attach to the next package summary line.
	// No package info (e.g. -run output stripped through a pipe) falls back
	// to the bare test name - the pre-#3781 key, a degraded fingerprint.
	c.mu.Lock()
	defer c.mu.Unlock()
	var pending []string
	seen := map[string]bool{}
	// #3863 C: remember the last resolved package; a truncated tail that
	// lost its own `FAIL\t<pkg>` summary falls back to it instead of
	// splitting the same failing test into pkg-qualified and bare keys
	// (diluted counts + duplicate enforce lines).
	lastPkg := ""
	flush := func(pkg string) {
		if pkg == "" {
			pkg = lastPkg
		}
		for _, name := range pending {
			key := name
			if pkg != "" {
				key = pkg + "." + name
			}
			c.counts[key]++
		}
		if pkg == "" {
			for _, name := range pending {
				c.degraded[name] = true
			}
		} else {
			lastPkg = pkg
		}
		pending = nil
		// A new package block starts: the same test name may legitimately
		// fail in it too (#3781 cross-package collision).
		seen = map[string]bool{}
	}
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if m := testPkgFailRE.FindStringSubmatch(line); m != nil {
			flush(m[1])
			continue
		}
		m := testFailLineRE.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		key := m[1]
		if seen[key] {
			continue // one sighting per test per output (dedup reruns inside)
		}
		seen[key] = true
		pending = append(pending, m[1])
	}
	flush("") // trailing failures with no package summary: degraded fallback
}

// snapshot returns the per-test counts for run-end signal collection.
func (c *testFailCollector) snapshot() map[string]int {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make(map[string]int, len(c.counts))
	for k, v := range c.counts {
		out[k] = v
	}
	return out
}

// degradedSnapshot returns the keys collected without package context.
func (c *testFailCollector) degradedSnapshot() map[string]bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make(map[string]bool, len(c.degraded))
	for k, v := range c.degraded {
		out[k] = v
	}
	return out
}

// reset clears per-run counts (called after routeWeaknessSignals snapshots).
func (c *testFailCollector) reset() {
	c.mu.Lock()
	c.counts = map[string]int{}
	c.mu.Unlock()
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
	// r17: go-test failure signals. Two sightings in ONE run (the agent
	// reran and the test still fails) is WeakForgetting - it was told. One
	// sighting starts WeakRare; cross-run recurrence matures through the
	// store's Count++ path like every other fingerprint.
	if a.testFails != nil {
		degraded := a.testFails.degradedSnapshot()
		for name, n := range a.testFails.snapshot() {
			fp := "test-fail:" + name
			// #3781 degraded fallback: package context unavailable, the
			// fingerprint is the pre-#3781 bare test name.
			suffix := ""
			if degraded[name] {
				suffix = " [pkg context unavailable: degraded fingerprint]"
			}
			if n >= 2 {
				out = append(out, runSignal{fp, WeakForgetting,
					fmt.Sprintf("go test failure rerun in one run (%d sightings)%s", n, suffix)})
			} else {
				out = append(out, runSignal{fp, WeakRare,
					"go test failure observed" + suffix})
			}
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
	// #3925: hold the cross-process flock across the WHOLE mutation -
	// load, Count++, and save. #3837 B placed the lock in
	// routeMatureSignals only, so the main load-modify-save stage ran
	// unlocked and two processes sharing a workspace still lost one side's
	// increments to last-writer-wins (the comment overpromised). The lock
	// is non-blocking-with-timeout; on failure we proceed unlocked
	// (same availability-over-mutexality tradeoff as #1752/#775).
	unlock, lockErr := util.FileLock(filepath.Join(wd, ".ggcode", weaknessStoreFile) + ".lock")
	if lockErr != nil {
		debug.Log("weakness", "store lock unavailable, proceeding unlocked: %v", lockErr)
	} else {
		defer unlock()
	}
	store := loadWeaknessStore(filepath.Join(wd, ".ggcode", weaknessStoreFile))
	changed := false
	now := time.Now()
	for _, s := range signals {
		rec := store[s.fingerprint]
		// #3863 D: the old `now.Sub(rec.LastTS) > weaknessAge` restart arm
		// here was dead code - loadWeaknessStore already deletes entries
		// older than weaknessAge, so a surviving rec is never stale.
		if rec.Count == 0 {
			// First sighting: the class is seeded by this run's observation.
			rec = weaknessRecord{Class: s.class, Evidence: s.evidence, LastTS: now, Count: 1}
		} else {
			rec.Count++
			rec.LastTS = now
			// Rare upgrades to the class its fingerprint implies once it
			// repeats (a rare failure that recurs is no longer rare).
			// #3863 B: an IN-RUN signal already carries the strong class
			// when the agent reran and still failed (n>=2 emits
			// WeakForgetting directly) - adopt it. But a cross-run
			// recurrence of a bare test-fail sighting must NOT upgrade:
			// environment flakes have no agent-actionable semantics and a
			// standing "enforce" rule for them is pure prompt noise.
			if rec.Class == WeakRare {
				switch {
				case s.class == WeakForgetting:
					rec.Class = WeakForgetting
				case strings.HasPrefix(s.fingerprint, "errcat:"):
					rec.Class = WeakBoundary
				case strings.HasPrefix(s.fingerprint, "test-fail:"):
					// stay Rare (#3863 B)
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
	// #3781 fix 3: reset the per-run collector BEFORE persisting. Collection
	// and persistence are decoupled: a failed save (read-only dir, disk full)
	// must not leave the run's counts in place to be double-counted next run.
	if a.testFails != nil {
		a.testFails.reset()
	}
	evictWeaknessStore(store, weaknessMaxEntries)
	if err := saveWeaknessStore(filepath.Join(wd, ".ggcode", weaknessStoreFile), store); err != nil {
		debug.Log("weakness", "store save failed: %v", err)
		return
	}
	routeMatureSignalsLocked(wd, store)
}

// routeMatureSignals is the standalone entry (tests, future callers): it
// acquires the store flock itself, then runs the locked body. Production
// flow calls routeMatureSignalsLocked directly - routeWeaknessSignals
// already holds the lock (#3925), and the non-blocking flock on a second
// fd of the same process would time out against itself.
func routeMatureSignals(workingDir string, store weaknessSignalStore) {
	unlock, lockErr := util.FileLock(filepath.Join(workingDir, ".ggcode", weaknessStoreFile) + ".lock")
	if lockErr != nil {
		debug.Log("weakness", "store lock unavailable, proceeding unlocked: %v", lockErr)
	} else {
		defer unlock()
	}
	routeMatureSignalsLocked(workingDir, store)
}

// routeMatureSignalsLocked is the flock-held body: it writes one line per
// mature-and-unrouted signal into project memory (key weakness-signals).
// Existing lines are deduped by fingerprint prefix so re-routing after a
// manual memory edit still cannot duplicate a line. Caller holds the store
// flock (#3925).
func routeMatureSignalsLocked(workingDir string, store weaknessSignalStore) {
	// #3837 B heritage note: the lock used to live HERE, covering only the
	// memory-routing tail while the caller's load/Count++/save ran unlocked.
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
		// #3837 A: dedupe by FINGERPRINT PREFIX, not the whole line - the
		// routed line embeds the live Count, so a later run with a different
		// Count (memory save failure, eviction+recurrence, manual edit) never
		// re-matched the old line and rules accumulated. A newer count
		// REPLACES the stale line instead of appending a duplicate.
		fp := routedFingerprint(l)
		if fp != "" {
			existing = replaceRoutedLine(existing, fp, l)
		} else {
			if strings.Contains(existing, l) {
				continue
			}
			existing = strings.TrimRight(existing, "\n") + "\n" + l
		}
	}
	if err := auto.SaveMemoryWithSource(weaknessMemoryKey, strings.TrimLeft(existing, "\n"), "weakness-router"); err != nil {
		debug.Log("weakness", "memory save failed: %v", err)
		return
	}
	// Persist the Routed flags so the lines are not re-emitted next run.
	_ = saveWeaknessStore(filepath.Join(workingDir, ".ggcode", weaknessStoreFile), store)
	debug.Log("weakness", "routed %d mature signal(s) to project memory", len(lines))
}

// routedFingerprint extracts the fingerprint token from a routed line
// ("enforce: <fp> (N runs) - ..." / "boundary: <fp> (N runs) - ...").
// Empty when the line does not match the routed shapes.
func routedFingerprint(line string) string {
	rest, ok := strings.CutPrefix(line, "enforce: ")
	if !ok {
		rest, ok = strings.CutPrefix(line, "boundary: ")
		if !ok {
			return ""
		}
	}
	if idx := strings.Index(rest, " ("); idx > 0 {
		return rest[:idx]
	}
	return ""
}

// replaceRoutedLine swaps any existing routed line carrying the same
// fingerprint for the new line (in place), or appends when none exists.
func replaceRoutedLine(existing, fp, newLine string) string {
	marker := " " + fp + " ("
	lines := strings.Split(existing, "\n")
	for i, ln := range lines {
		if strings.Contains(ln, marker) && (strings.HasPrefix(ln, "enforce: ") || strings.HasPrefix(ln, "boundary: ")) {
			lines[i] = newLine
			return strings.Join(lines, "\n")
		}
	}
	return strings.TrimRight(existing, "\n") + "\n" + newLine
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
