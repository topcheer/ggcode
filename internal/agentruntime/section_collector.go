package agentruntime

import (
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
)

// SectionCollector runs a background goroutine that periodically collects
// I/O-heavy system-prompt sections (git status, project overview, toolchain,
// package symbols, project commands) and caches the results in memory.
//
// When the system prompt is rebuilt (e.g. on user message submit), it reads
// these pre-computed values instantly — zero I/O on the UI thread.
//
// Design:
//   - Start launches a goroutine that refreshes every refreshInterval.
//   - The first refresh runs synchronously in Start so values are available
//     immediately for the initial prompt build.
//   - RefreshNow triggers an immediate refresh (useful after file saves).
//   - If a refresh is slow, the previous cached value remains in use —
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
}

// globalSectionCollector is the default collector for the interactive REPL.
// Sub-agent and teammate prompts also read from it.
var globalSectionCollector *SectionCollector

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
// It is safe to call multiple times — subsequent calls are no-ops if the
// working directory matches.
func InitGlobalSectionCollector(workingDir string) {
	if globalSectionCollector != nil && globalSectionCollector.working == workingDir {
		return
	}
	// Stop any previous collector.
	if globalSectionCollector != nil {
		globalSectionCollector.Stop()
	}
	sc := &SectionCollector{
		working: workingDir,
		stop:    make(chan struct{}),
		done:    make(chan struct{}),
	}
	// Synchronous first refresh so values are ready for the initial prompt,
	// but bounded: on slow filesystems (NFS with large directories) the scans
	// can take minutes, which would delay the TUI indefinitely. Past the
	// budget, startup proceeds with empty sections and the refresh goroutine
	// keeps filling the cache in the background.
	refreshDone := make(chan struct{})
	go func() {
		sc.refresh()
		close(refreshDone)
	}()
	select {
	case <-refreshDone:
	case <-time.After(firstRefreshBudget):
		debug.Log("agentruntime", "section collector first refresh exceeded %s (slow filesystem?); starting with empty sections", firstRefreshBudget)
	}
	sc.Start()
	globalSectionCollector = sc
}

// StopGlobalSectionCollector stops the background goroutine.
func StopGlobalSectionCollector() {
	if globalSectionCollector != nil {
		globalSectionCollector.Stop()
		globalSectionCollector = nil
	}
}

// Start launches the background refresh loop. The first refresh must have
// been called by the caller (InitGlobalSectionCollector does this).
func (sc *SectionCollector) Start() {
	go sc.loop()
}

// Stop signals the background loop to exit and waits for it.
func (sc *SectionCollector) Stop() {
	close(sc.stop)
	<-sc.done
}

// loop runs the periodic refresh until Stop is called.
// When consecutive refreshes produce no changes, it backs off to
// sectionIdleInterval to reduce idle-time I/O.
func (sc *SectionCollector) loop() {
	defer close(sc.done)
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
			if sc.idle {
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
	go sc.refresh()
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

	// Sections are independent — collect them concurrently so total refresh
	// time is the slowest section (MAX) rather than the sum. On slow
	// filesystems each scan is dominated by readdir/stat round-trips.
	var (
		overview, modified, commands, toolchain, symbols, deps, recentCommits string
		wg                                                                    sync.WaitGroup
	)
	wg.Add(7)
	go func() { defer wg.Done(); overview = projectOverviewSection(sc.working) }()
	go func() { defer wg.Done(); modified = computeModifiedFilesSection(sc.working) }()
	go func() { defer wg.Done(); commands = projectCommandsSection(sc.working) }()
	go func() { defer wg.Done(); toolchain = toolchainSection(sc.working) }()
	go func() {
		defer wg.Done()
		symbols = buildGoPackageSymbolsSection(sc.working)
		symbols += buildTSSymbolsSection(sc.working)
		symbols += buildPythonSymbolsSection(sc.working)
	}()
	go func() { defer wg.Done(); deps = buildPackageDepsSection(sc.working) }()
	go func() { defer wg.Done(); recentCommits = computeRecentCommitsSection(sc.working) }()

	// The background loop and Stop() must never deadlock on a hung section:
	// the goroutines write shared locals and exit on their own. But refresh()
	// itself must not block forever waiting for them — wait with a generous
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
	if globalSectionCollector == nil {
		return sectionSnapshot{}, false
	}
	return globalSectionCollector.Snapshot(), true
}
