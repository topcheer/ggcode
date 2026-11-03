package agent

import (
	"encoding/json"
	"fmt"
	"math"
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

type WeaknessClass int

const (
	WeakRare WeaknessClass = iota
	WeakForgetting
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

type weaknessSignalStore map[string]weaknessRecord

type weaknessRecord struct {
	Class    WeaknessClass `json:"class"`
	Count    int           `json:"count"`
	Evidence string        `json:"evidence,omitempty"`
	LastTS   time.Time     `json:"last_ts"`
	Routed   bool          `json:"routed,omitempty"`
}

const (
	weaknessRouteThreshold = 2
	weaknessStoreFile      = "weakness_signals.json"
	weaknessMemoryKey      = "weakness-signals"
	weaknessMaxEntries     = 64
	weaknessAge            = 30 * 24 * time.Hour
	weaknessDecayHalfLife  = 15 * 24 * time.Hour
)

type runSignal struct {
	fingerprint string
	class       WeaknessClass
	evidence    string
}

var testFailLineRE = regexp.MustCompile(`^--- FAIL: (\S+)`)

var testPkgFailRE = regexp.MustCompile("^FAIL\t(\\S+)")

type testFailCollector struct {
	mu       sync.Mutex
	counts   map[string]int
	degraded map[string]bool
}

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

func (c *testFailCollector) record(cmd, output string) {

	if !goTestInvokedAsTokens(cmd) {
		return
	}
	if !strings.Contains(output, "--- FAIL:") {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	var pending []string
	seen := map[string]bool{}

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
			continue
		}
		seen[key] = true
		pending = append(pending, m[1])
	}
	flush("")
}

func (c *testFailCollector) snapshot() map[string]int {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make(map[string]int, len(c.counts))
	for k, v := range c.counts {
		out[k] = v
	}
	return out
}

func (c *testFailCollector) degradedSnapshot() map[string]bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make(map[string]bool, len(c.degraded))
	for k, v := range c.degraded {
		out[k] = v
	}
	return out
}

func (c *testFailCollector) reset() {
	c.mu.Lock()
	c.counts = map[string]int{}
	c.mu.Unlock()
}

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

	if a.testFails != nil {
		degraded := a.testFails.degradedSnapshot()
		for name, n := range a.testFails.snapshot() {
			fp := "test-fail:" + name

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

		if rec.Count == 0 {

			rec = weaknessRecord{Class: s.class, Evidence: s.evidence, LastTS: now, Count: 1}
		} else {
			rec.Count++
			rec.LastTS = now

			if rec.Class == WeakRare {
				switch {
				case s.class == WeakForgetting:
					rec.Class = WeakForgetting
				case strings.HasPrefix(s.fingerprint, "errcat:"):
					rec.Class = WeakBoundary
				case strings.HasPrefix(s.fingerprint, "test-fail:"):

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

func routeMatureSignals(workingDir string, store weaknessSignalStore) {
	unlock, lockErr := util.FileLock(filepath.Join(workingDir, ".ggcode", weaknessStoreFile) + ".lock")
	if lockErr != nil {
		debug.Log("weakness", "store lock unavailable, proceeding unlocked: %v", lockErr)
	} else {
		defer unlock()
	}
	routeMatureSignalsLocked(workingDir, store)
}

func weaknessDecayFactor(lastTS, now time.Time) float64 {
	if lastTS.IsZero() || !lastTS.Before(now) {
		return 1
	}
	age := now.Sub(lastTS)
	if age < time.Hour {
		return 1
	}
	if age >= weaknessAge {
		return 0
	}
	return math.Exp(-float64(age) / float64(weaknessDecayHalfLife))
}

func routeMatureSignalsLocked(workingDir string, store weaknessSignalStore) {

	auto := memory.NewProjectAutoMemory(workingDir)
	if auto == nil {
		return
	}
	var lines []string
	now := time.Now()
	for fp, rec := range store {

		if float64(rec.Count)*weaknessDecayFactor(rec.LastTS, now) < weaknessRouteThreshold || rec.Routed || rec.Class == WeakRare {
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

	_ = saveWeaknessStore(filepath.Join(workingDir, ".ggcode", weaknessStoreFile), store)
	debug.Log("weakness", "routed %d mature signal(s) to project memory", len(lines))
}

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

	now := time.Now()
	for fp, rec := range store {
		if now.Sub(rec.LastTS) > weaknessAge {
			delete(store, fp)
		}
	}
	return store
}

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
