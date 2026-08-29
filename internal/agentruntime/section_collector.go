package agentruntime

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/safego"
)

// SectionCollector runs a background goroutine that periodically collects
// I/O-heavy system-prompt sections (git status, project overview, toolchain,
// package symbols, project commands) and caches the results in memory.
//
// When the system prompt is rebuilt (e.g. on user message submit), it reads
// these pre-computed values instantly - zero I/O on the UI thread.
//
// Design:
//   - Start launches a goroutine that refreshes every refreshInterval.
//   - The first refresh runs synchronously in Start so values are available
//     immediately for the initial prompt build.
//   - RefreshNow triggers an immediate refresh (useful after file saves).
//   - If a refresh is slow, the previous cached value remains in use -
//     prompt construction is never blocked.

const (
	// sectionRefreshInterval controls how often the background goroutine
	// re-reads git status, project files, etc. Short enough to stay fresh,
	// long enough to avoid wasting CPU on busy repos.
	sectionRefreshInterval = 10 * time.Second

	// sectionIdleInterval is used when the last refresh produced no changes.
	// This reduces idle-time I/O (git status, git log, etc.) by 3x.
	sectionIdleInterval = 30 * time.Second

	// firstRefreshBudget bounds how long startup waits for the synchronous
	// first refresh on slow filesystems (e.g. NFS mounts with many entries).
	// Individual section scans (project overview, symbol maps) perform one
	// readdir/stat network round-trip per entry and can take minutes there.
	// Past the budget the TUI starts with empty sections; the background
	// refresh loop fills the cache shortly after.
	firstRefreshBudget = 5 * time.Second
)

// SectionCollector holds cached prompt sections refreshed by a background goroutine.
type SectionCollector struct {
	mu      sync.RWMutex
	working string

	overview      string
	modified      string
	commands      string
	toolchain     string
	symbols       string
	recentCommits string
	deps          string

	// idle tracking: if snapshot unchanged, use longer interval
	lastSnapshot string
	idle         bool

	stop chan struct{}
	done chan struct{}

	// loopStarted is flipped exactly once by Start; Stop consults it so a
	// collector whose loop never ran does not wait forever on done (#1154).
	loopStarted atomic.Bool

	// stopped makes Stop idempotent: concurrent callers each run through
	// their own close attempt, but only the first one closes the channel.
	// Before this guard, two goroutines calling Stop on the same instance
	// panicked on "close of closed channel" (#1154, desktop multi-ChatBridge).
	stopped sync.Once
}

// globalSectionCollector is the default collector for the interactive REPL.
// Sub-agent and teammate prompts also read from it.
// Access is serialized by globalCollectorMu (#1154): Init runs on the TUI main
// goroutine, StopGlobalSectionCollector fires from session teardown, and
// GlobalSectionSnapshot is read from background prompt builders.
var (
	globalCollectorMu sync.Mutex

	globalSectionCollector *SectionCollector
)

// newSectionCollector builds a collector with its lifecycle channels ready.
func newSectionCollector(workingDir string) *SectionCollector {
	return &SectionCollector{
		working: workingDir,
		stop:    make(chan struct{}),
		done:    make(chan struct{}),
	}
}

// sectionSnapshot is an immutable point-in-time copy of all cached sections.
type sectionSnapshot struct {
	Overview      string
	Modified      string
	Commands      string
	Toolchain     string
	Symbols       string
	RecentCommits string
	Deps          string
}

// InitGlobalSectionCollector creates the global collector for workingDir,
// performs one synchronous refresh, then starts the background refresh loop.
// It is safe to call multiple times - subsequent calls are no-ops if the
// working directory matches - and safe for concurrent callers (#1154).
func InitGlobalSectionCollector(workingDir string) {
	// Fast path: an initialized collector already serves this directory.
	globalCollectorMu.Lock()
	if cur := globalSectionCollector; cur != nil && cur.working == workingDir {
		globalCollectorMu.Unlock()
		return
	}
	globalCollectorMu.Unlock()

	sc := newSectionCollector(workingDir)
	// Synchronous first refresh so values are ready for the initial prompt,
	// but bounded: on slow filesystems (NFS with large directories) the scans
	// can take minutes, which would delay the TUI indefinitely. Past the
	// budget, startup proceeds with empty sections and the refresh goroutine
	// keeps filling the cache in the background.
	refreshDone := make(chan struct{})
	go func() {
		// LIFO: Recover (registered last) runs first on panic, so the close
		// below still executes and the startup select never blocks past the
		// budget on a panicking refresh.
		defer close(refreshDone)
		defer safego.Recover("agentruntime.sectionCollector.firstRefresh")
		sc.refresh()
	}()
	select {
	case <-refreshDone:
	case <-time.After(firstRefreshBudget):
		debug.Log("agentruntime", "section collector first refresh exceeded %s (slow filesystem?); starting with empty sections", firstRefreshBudget)
	}
	sc.Start()

	// Swap under lock (#1154), capturing the instance being replaced in the
	// same critical section so a concurrent Stop/Snapshot observes either the
	// old or the new collector - never a torn state. The displaced instance is
	// shut down after installation so snapshot readers always see a live
	// collector; lingering Stop work happens outside the lock.
	globalCollectorMu.Lock()
	old := globalSectionCollector
	globalSectionCollector = sc
	globalCollectorMu.Unlock()
	if old != nil {
		old.Stop()
	}
}

// StopGlobalSectionCollector stops the background goroutine.
func StopGlobalSectionCollector() {
	// Detach first so readers converge on "not initialized", then stop the
	// detached instance outside the lock (#1154). Stop itself is idempotent,
	// so overlap with a racing Init that re-installs an instance cannot
	// double-close anything.
	globalCollectorMu.Lock()
	cur := globalSectionCollector
	globalSectionCollector = nil
	globalCollectorMu.Unlock()
	if cur != nil {
		cur.Stop()
	}
}

// Start launches the background refresh loop. The first refresh must have
// been called by the caller (InitGlobalSectionCollector does this).
// Repeat calls are no-ops (#1154): exactly one loop goroutine may exist,
// otherwise two loops would race to close done.
func (sc *SectionCollector) Start() {
	if !sc.loopStarted.CompareAndSwap(false, true) {
		return
	}
	go sc.loop()
}

// Stop signals the background loop to exit and waits for it. Concurrent or
// repeated calls are safe (#1154): the close happens exactly once, and waiting
// on done is safe because a channel close releases every waiter.
func (sc *SectionCollector) Stop() {
	sc.stopped.Do(func() { close(sc.stop) })
	if sc.loopStarted.Load() {
		<-sc.done
	}
}

// loop runs the periodic refresh until Stop is called.
// When consecutive refreshes produce no changes, it backs off to
// sectionIdleInterval to reduce idle-time I/O.
func (sc *SectionCollector) loop() {
	defer close(sc.done)
	// Registered after the close defer, so on a panic Recover runs first
	// (LIFO) and sc.done is still closed - Stop() cannot deadlock on a
	// crashed loop.
	defer safego.Recover("agentruntime.sectionCollector.loop")
	interval := sectionRefreshInterval
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-sc.stop:
			return
		case <-ticker.C:
			sc.refresh()
			newInterval := sectionRefreshInterval
			sc.mu.RLock()
			idle := sc.idle
			sc.mu.RUnlock()
			if idle {
				newInterval = sectionIdleInterval
			}
			if newInterval != interval {
				ticker.Reset(newInterval)
				interval = newInterval
			}
		}
	}
}

// RefreshNow triggers an immediate refresh in a non-blocking goroutine.
// Call this after file edits or git operations to get fresh data without
// waiting for the next tick.
func (sc *SectionCollector) RefreshNow() {
	go safego.Run("agentruntime.sectionCollector.refreshNow", sc.refresh)
}

// Snapshot returns a point-in-time copy of all cached sections.
func (sc *SectionCollector) Snapshot() sectionSnapshot {
	sc.mu.RLock()
	defer sc.mu.RUnlock()
	return sectionSnapshot{
		Overview:      sc.overview,
		Modified:      sc.modified,
		Commands:      sc.commands,
		Toolchain:     sc.toolchain,
		Symbols:       sc.symbols,
		RecentCommits: sc.recentCommits,
		Deps:          sc.deps,
	}
}

// refresh reads all I/O-heavy sections and updates the cache.
// Each section function already has its own internal timeout, so they
// complete or time out independently.
func (sc *SectionCollector) refresh() {
	start := time.Now()

	// Sections are independent - collect them concurrently so total refresh
	// time is the slowest section (MAX) rather than the sum. On slow
	// filesystems each scan is dominated by readdir/stat round-trips.
	var (
		overview, modified, commands, toolchain, symbols, deps, recentCommits string
		wg                                                                    sync.WaitGroup
	)
	wg.Add(7)
	// Each section goroutine runs under safego.Run: a panic in one section's
	// parsing (go.mod / git output / directory walk over untrusted trees)
	// must degrade that section, not kill the process. wg.Done still fires
	// (deferred inside fn) so refresh's bounded wait is unaffected.
	go safego.Run("section.overview", func() { defer wg.Done(); overview = projectOverviewSection(sc.working) })
	go safego.Run("section.modified", func() { defer wg.Done(); modified = computeModifiedFilesSection(sc.working) })
	go safego.Run("section.commands", func() { defer wg.Done(); commands = projectCommandsSection(sc.working) })
	go safego.Run("section.toolchain", func() { defer wg.Done(); toolchain = toolchainSection(sc.working) })
	go safego.Run("section.symbols", func() {
		defer wg.Done()
		symbols = buildGoPackageSymbolsSection(sc.working)
		symbols += buildTSSymbolsSection(sc.working)
		symbols += buildPythonSymbolsSection(sc.working)
	})
	go safego.Run("section.deps", func() { defer wg.Done(); deps = buildPackageDepsSection(sc.working) })
	go safego.Run("section.recentCommits", func() { defer wg.Done(); recentCommits = computeRecentCommitsSection(sc.working) })

	// The background loop and Stop() must never deadlock on a hung section:
	// the goroutines write shared locals and exit on their own. But refresh()
	// itself must not block forever waiting for them - wait with a generous
	// cap so the loop keeps its cadence even on pathological mounts.
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
		sc.store(overview, modified, commands, toolchain, symbols, deps, recentCommits, start)
	case <-time.After(10 * time.Minute):
		debug.Log("agentruntime", "section collector refresh hung >10m; skipping update")
	}
}

// store writes the freshly collected sections into the cache and updates
// idle tracking. Called with all section values ready.
func (sc *SectionCollector) store(overview, modified, commands, toolchain, symbols, deps, recentCommits string, start time.Time) {
	sc.mu.Lock()
	sc.overview = overview
	sc.modified = modified
	sc.commands = commands
	sc.toolchain = toolchain
	sc.symbols = symbols
	sc.deps = deps
	sc.recentCommits = recentCommits

	// Check if anything actually changed
	currentSig := overview + "\x00" + modified + "\x00" + commands + "\x00" +
		toolchain + "\x00" + symbols + "\x00" + deps + "\x00" + recentCommits

	if currentSig == sc.lastSnapshot {
		sc.idle = true
	} else {
		sc.idle = false
		sc.lastSnapshot = currentSig
		debug.Log("agentruntime", "section collector refreshed in %s", time.Since(start).Round(time.Millisecond))
	}
	sc.mu.Unlock()
}

// GlobalSectionSnapshot returns the cached sections from the global collector.
// If no collector has been initialized (e.g. in tests or pipe mode), it
// returns zero values and the caller falls back to direct computation.
func GlobalSectionSnapshot() (sectionSnapshot, bool) {
	// Copy the pointer under lock (#1154); the per-instance state read below
	// is separately guarded by the collector's own RWMutex, so holding only
	// the global mutex here avoids serializing readers behind slow snapshots.
	globalCollectorMu.Lock()
	sc := globalSectionCollector
	globalCollectorMu.Unlock()
	if sc == nil {
		return sectionSnapshot{}, false
	}
	return sc.Snapshot(), true
}
