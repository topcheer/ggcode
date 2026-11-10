package agentruntime

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/safego"
)

const (
	sectionRefreshInterval = 10 * time.Second

	sectionIdleInterval = 30 * time.Second

	firstRefreshBudget = 800 * time.Millisecond
)

type SectionCollector struct {
	mu      sync.RWMutex
	working string

	overview  string
	modified  string
	commands  string
	toolchain string
	symbols   string
	repoHead  string
	deps      string

	lastSnapshot string
	idle         bool

	stop chan struct{}
	done chan struct{}

	loopStarted atomic.Bool

	stopped sync.Once
}

var (
	globalCollectorMu sync.Mutex

	globalSectionCollector *SectionCollector
)

func newSectionCollector(workingDir string) *SectionCollector {
	return &SectionCollector{
		working: workingDir,
		stop:    make(chan struct{}),
		done:    make(chan struct{}),
	}
}

type sectionSnapshot struct {
	Overview  string
	Modified  string
	Commands  string
	Toolchain string
	Symbols   string
	RepoHead  string
	Deps      string
}

func InitGlobalSectionCollector(workingDir string) {

	globalCollectorMu.Lock()
	if cur := globalSectionCollector; cur != nil && cur.working == workingDir {
		globalCollectorMu.Unlock()
		return
	}
	globalCollectorMu.Unlock()

	sc := newSectionCollector(workingDir)

	refreshDone := make(chan struct{})
	go func() {

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

	globalCollectorMu.Lock()
	old := globalSectionCollector
	globalSectionCollector = sc
	globalCollectorMu.Unlock()
	if old != nil {
		old.Stop()
	}
}

func StopGlobalSectionCollector() {

	globalCollectorMu.Lock()
	cur := globalSectionCollector
	globalSectionCollector = nil
	globalCollectorMu.Unlock()
	if cur != nil {
		cur.Stop()
	}
}

func (sc *SectionCollector) Start() {
	if !sc.loopStarted.CompareAndSwap(false, true) {
		return
	}
	go safego.Run("agentruntime.sectionCollector.loop", sc.loop)
}

func (sc *SectionCollector) Stop() {
	sc.stopped.Do(func() { close(sc.stop) })
	if sc.loopStarted.Load() {
		<-sc.done
	}
}

func (sc *SectionCollector) loop() {
	defer close(sc.done)

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

func (sc *SectionCollector) Snapshot() sectionSnapshot {
	sc.mu.RLock()
	defer sc.mu.RUnlock()
	return sectionSnapshot{
		Overview:  sc.overview,
		Modified:  sc.modified,
		Commands:  sc.commands,
		Toolchain: sc.toolchain,
		Symbols:   sc.symbols,
		RepoHead:  sc.repoHead,
		Deps:      sc.deps,
	}
}

func (sc *SectionCollector) refresh() {
	start := time.Now()

	var (
		overview, modified, commands, toolchain, symbols, deps, repoHead string
		wg                                                               sync.WaitGroup
	)
	wg.Add(7)

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
	go safego.Run("section.repoHead", func() { defer wg.Done(); repoHead = computeRepoHeadSection(sc.working) })

	done := make(chan struct{})
	go safego.Run("section.waitAndClose", func() { wg.Wait(); close(done) })
	select {
	case <-done:
		sc.store(overview, modified, commands, toolchain, symbols, deps, repoHead, start)
	case <-time.After(10 * time.Minute):
		debug.Log("agentruntime", "section collector refresh hung >10m; skipping update")
	}
}

func (sc *SectionCollector) store(overview, modified, commands, toolchain, symbols, deps, repoHead string, start time.Time) {
	sc.mu.Lock()
	sc.overview = overview
	sc.modified = modified
	sc.commands = commands
	sc.toolchain = toolchain
	sc.symbols = symbols
	sc.deps = deps
	sc.repoHead = repoHead

	currentSig := overview + "\x00" + modified + "\x00" + commands + "\x00" +
		toolchain + "\x00" + symbols + "\x00" + deps + "\x00" + repoHead

	if currentSig == sc.lastSnapshot {
		sc.idle = true
	} else {
		sc.idle = false
		sc.lastSnapshot = currentSig
		debug.Log("agentruntime", "section collector refreshed in %s", time.Since(start).Round(time.Millisecond))
	}
	sc.mu.Unlock()
}

func GlobalSectionSnapshot() (sectionSnapshot, bool) {

	globalCollectorMu.Lock()
	sc := globalSectionCollector
	globalCollectorMu.Unlock()
	if sc == nil {
		return sectionSnapshot{}, false
	}
	return sc.Snapshot(), true
}
