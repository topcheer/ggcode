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
)

// SectionCollector holds cached prompt sections refreshed by a background goroutine.
type SectionCollector struct {
	mu      sync.RWMutex
	working string

	overview  string
	modified  string
	commands  string
	toolchain string
	symbols   string

	stop chan struct{}
	done chan struct{}
}

// globalSectionCollector is the default collector for the interactive REPL.
// Sub-agent and teammate prompts also read from it.
var globalSectionCollector *SectionCollector

// sectionSnapshot is an immutable point-in-time copy of all cached sections.
type sectionSnapshot struct {
	Overview  string
	Modified  string
	Commands  string
	Toolchain string
	Symbols   string
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
	// Synchronous first refresh so values are ready for the initial prompt.
	sc.refresh()
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
func (sc *SectionCollector) loop() {
	defer close(sc.done)
	ticker := time.NewTicker(sectionRefreshInterval)
	defer ticker.Stop()
	for {
		select {
		case <-sc.stop:
			return
		case <-ticker.C:
			sc.refresh()
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
		Overview:  sc.overview,
		Modified:  sc.modified,
		Commands:  sc.commands,
		Toolchain: sc.toolchain,
		Symbols:   sc.symbols,
	}
}

// refresh reads all I/O-heavy sections and updates the cache.
// Each section function already has its own internal timeout, so they
// complete or time out independently.
func (sc *SectionCollector) refresh() {
	start := time.Now()

	overview := projectOverviewSection(sc.working)
	modified := computeModifiedFilesSection(sc.working)
	commands := projectCommandsSection(sc.working)
	toolchain := toolchainSection(sc.working)
	symbols := buildGoPackageSymbolsSection(sc.working)

	sc.mu.Lock()
	sc.overview = overview
	sc.modified = modified
	sc.commands = commands
	sc.toolchain = toolchain
	sc.symbols = symbols
	sc.mu.Unlock()

	debug.Log("agentruntime", "section collector refreshed in %s", time.Since(start).Round(time.Millisecond))
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
