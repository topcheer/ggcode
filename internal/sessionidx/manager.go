package sessionidx

import (
	stddebug "runtime/debug"

	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/safego"
)

// Manager aggregates the indexes of every session file in a sessions
// directory. It OWNS the live-session index (writes it) and treats sibling
// indexes as read-only inputs refreshed on a timer, per the cross-instance
// ownership contract.
type Manager struct {
	dir            string // sessions dir
	liveID         string // this instance's own session id ("" = none)
	selfLimitBytes int64  // skip external idx if SELF idx larger than this (then self-only)
	externalLimit  int64  // skip any external idx larger than this
	refreshEvery   time.Duration

	mu       sync.Mutex
	self     *Index
	external map[string]*extEntry
	// R1 (#5156 review): total-decoded-bytes admission with LRU eviction.
	// externalLimit only caps each file's size; without a total budget a
	// single 894MB idx (and friends) would be decoded into the heap -
	// the exact amplification #5149 exists to remove. totalBudget<=0
	// disables the budget (plain-instance default keeps the historical
	// per-file cap behavior).
	totalBudget   int64
	totalDecoded  int64
	evictedTotal  int64
	evictedCount  int64
	evictionOrder []string // least-recently-used first
	// Cold per-workspace loading (#5149, user-directed): nothing is
	// decoded at rest; the first query naming a workspace activates its
	// indexes, and an idle window unloads them again.
	wsActive   map[string]time.Time // workspace -> last query
	idleUnload time.Duration
	stop       chan struct{}
	stopped    sync.WaitGroup
}

type extEntry struct {
	idx     *Index
	idxSize int64
	modTime time.Time
	decoded bool // lazy: true once the full gob decode has been done
	// degraded: the idx file is corrupt/unreadable - serve this session by
	// scanning its source JSONL (read-only) instead of pretending it is empty.
	degraded bool
}

const (
	DefaultSelfLimitBytes     = 500 << 20 // 500MB
	DefaultExternalLimitBytes = 200 << 20 // 200MB
	DefaultRefreshEvery       = 30 * time.Minute
)

func NewManager(sessionsDir, liveSessionID string) *Manager {
	return &Manager{
		dir:            sessionsDir,
		liveID:         liveSessionID,
		selfLimitBytes: DefaultSelfLimitBytes,
		externalLimit:  DefaultExternalLimitBytes,
		refreshEvery:   DefaultRefreshEvery,
		external:       map[string]*extEntry{},
		wsActive:       map[string]time.Time{},
		idleUnload:     DefaultWsIdleUnload,
		stop:           make(chan struct{}),
	}
}

// SetLiveID binds (or rebinds) this instance's own session id and makes
// its index live (loads + tails it from now on). Safe at any time.
func (m *Manager) SetLiveID(id string) {
	if id == "" {
		return
	}
	m.mu.Lock()
	if m.liveID == id && m.self != nil {
		m.mu.Unlock()
		return
	}
	m.liveID = id
	m.mu.Unlock()
	ix, err := Load(m.dir, id)
	if err != nil {
		debug.Log("sessionidx", "setlive: load %s: %v", id, err)
		return
	}
	if err := ix.Refresh(); err != nil {
		debug.Log("sessionidx", "setlive: refresh %s: %v", id, err)
	}
	m.mu.Lock()
	m.self = ix
	m.mu.Unlock()
}

// Self exposes the live-session index (may be nil when liveID is empty).
func (m *Manager) Self() *Index {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.self
}

// Start builds/refreshes the self index and scans sibling indexes, then
// spawns the background refresher. Safe to call once per process.
func (m *Manager) Start() {
	m.mu.Lock()
	if m.liveID != "" {
		ix, err := Load(m.dir, m.liveID)
		if err != nil {
			debug.Log("sessionidx", "start: self load %s: %v", m.liveID, err)
		} else {
			ix.SetSelfBudget(DefaultSelfBudget())
			m.self = ix
		}
	}
	m.mu.Unlock()
	if m.self != nil {
		if m.self.Capped() {
			// A large persisted idx was skipped (or retroactively dropped):
			// force a GC pass so any transient decode spike returns to the
			// OS now instead of sitting in MADV_FREE lag for minutes.
			stddebug.FreeOSMemory()
		}
		if err := m.self.Refresh(); err != nil {
			debug.Log("sessionidx", "start: self refresh: %v", err)
		}
	}
	m.scanExternal()
	m.stopped.Add(1)
	safego.Go("sessionidx.loop", m.loop)
	// Idle-unload janitor: returns a workspace's heap to zero when nobody
	// asks for it (minute granularity is plenty for a 30min window).
	m.stopped.Add(1)
	safego.Go("sessionidx.wsJanitor", func() {
		defer m.stopped.Done()
		t := time.NewTicker(time.Minute)
		defer t.Stop()
		for {
			select {
			case <-m.stop:
				return
			case <-t.C:
				m.unloadIdleWorkspaces()
			}
		}
	})
}

// loop periodically refreshes: self (cheap tail) and external entries whose
// index file changed on disk.
func (m *Manager) loop() {
	defer m.stopped.Done()
	t := time.NewTicker(m.refreshEvery)
	defer t.Stop()
	for {
		select {
		case <-m.stop:
			return
		case <-t.C:
			if m.self != nil {
				if err := m.self.Refresh(); err != nil {
					debug.Log("sessionidx", "loop: self refresh: %v", err)
				}
			}
			m.refreshExternal()
		}
	}
}

// Stop terminates the background loop.
func (m *Manager) Stop() {
	select {
	case <-m.stop:
		return
	default:
		close(m.stop)
	}
	m.stopped.Wait()
}

// scanExternal loads sibling session indexes (read-only). Threshold rules:
//   - if the SELF index file exceeds selfLimitBytes, load nothing external;
//   - an external idx file larger than externalLimit is skipped.
func (m *Manager) scanExternal() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.self != nil {
		if st, err := os.Stat(m.self.IdxPath); err == nil && st.Size() > m.selfLimitBytes {
			m.external = map[string]*extEntry{}
			debug.Log("sessionidx", "scan: self idx %.1fMB over limit, external skipped", float64(st.Size())/(1<<20))
			return
		}
	}
	entries, err := os.ReadDir(IdxDir(m.dir))
	if err != nil {
		return // no idx dir yet
	}
	ext := map[string]*extEntry{}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".idx") {
			continue
		}
		id := strings.TrimSuffix(name, ".idx")
		if m.liveID != "" && id == m.liveID {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.Size() > m.externalLimit {
			debug.Log("sessionidx", "scan: skip external idx %s (%.1fMB over %.0fMB)", id, float64(info.Size())/(1<<20), float64(m.externalLimit)/(1<<20))
			continue
		}
		// R1: files larger than the total budget can NEVER fit - skip them
		// here (before any decode) so a single 894MB idx is never admitted.
		if m.totalBudget > 0 && info.Size() > m.totalBudget {
			debug.Log("sessionidx", "scan: skip external idx %s (%.1fMB over total budget %.0fMB)", id, float64(info.Size())/(1<<20), float64(m.totalBudget)/(1<<20))
			continue
		}
		// Lazy admission (plain mode too, #5149 scanlazy): record the
		// entry WITHOUT decoding - decode on first query.
		ext[id] = &extEntry{idxSize: info.Size(), modTime: info.ModTime()}
	}
	m.external = ext
}

// refreshExternal reloads external entries whose .idx mtime changed.
func (m *Manager) refreshExternal() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, ent := range m.external {
		st, err := os.Stat(filepath.Join(IdxDir(m.dir), id+".idx"))
		if err != nil {
			continue
		}
		if !st.ModTime().After(ent.modTime) {
			continue
		}
		if st.Size() > m.externalLimit {
			delete(m.external, id)
			continue
		}
		// Changed file: invalidate any decoded copy so the next query
		// re-decodes lazily (plain mode via decodeInto, budgeted under R1).
		ent := m.external[id]
		if ent == nil {
			m.external[id] = &extEntry{idxSize: st.Size(), modTime: st.ModTime()}
			continue
		}
		if m.totalBudget > 0 && ent.decoded {
			m.totalDecoded -= ent.idxSize
		}
		ent.idx = nil
		ent.decoded = false
		ent.modTime = st.ModTime()
		ent.idxSize = st.Size()
		debug.Log("sessionidx", "refresh: invalidated external idx %s (lazy re-decode)", id)
	}
	// pick up newly created sibling indexes too (cheap dir scan)
	if len(m.external) < countIdxFiles(m.dir) {
		m.mu.Unlock()
		m.scanExternal()
		m.mu.Lock()
	}
}

func countIdxFiles(dir string) int {
	entries, err := os.ReadDir(IdxDir(dir))
	if err != nil {
		return 0
	}
	n := 0
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".idx") {
			n++
		}
	}
	return n
}

// SetExternalLimit adjusts the per-file external idx admission cap. The
// shared daemon (sessionidxd) raises this to its machine budget so
// over-cap sibling indexes become searchable; plain instances keep the
// conservative default. Call before Start.
func (m *Manager) SetExternalLimit(limit int64) {
	if limit <= 0 {
		return
	}
	m.mu.Lock()
	m.externalLimit = limit
	m.mu.Unlock()
}

// RefreshExternal re-scans sibling indexes now (size+mtime keyed). Safe to
// call concurrently with the background loop.
func (m *Manager) RefreshExternal() {
	m.refreshExternal()
}

// SetTotalBudget enables the machine-global decoded-bytes admission cap
// with LRU eviction (#5156 R1). The shared daemon sets this to its machine
// budget (default 1GB) so NO single over-cap file is ever decoded into the
// heap unless there is room; when the budget is full the least-recently-used
// decoded external is evicted first. Call before Start.
func (m *Manager) SetTotalBudget(b int64) {
	m.mu.Lock()
	m.totalBudget = b
	m.mu.Unlock()
}

// TotalDecodedBytes reports the current decoded-bytes footprint of loaded
// external indexes (lazy decode happens on first query per session).
func (m *Manager) TotalDecodedBytes() int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.totalDecoded
}

// Evictions reports cumulative evicted bytes and index count (#5156 R1 -
// surfaced at /status so operators can see budget pressure).
func (m *Manager) Evictions() (bytes, count int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.evictedTotal, m.evictedCount
}

// Dir returns the sessions directory this manager aggregates.
func (m *Manager) Dir() string { return m.dir }

// LiveID returns this instance's live session id ("" when none).
func (m *Manager) LiveID() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.liveID
}

// IndexCount reports how many external indexes are currently loaded.
func (m *Manager) IndexCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.external)
}

// Result is one recall hit across sessions.
type Result struct {
	SessionID string
	Role      byte
	TsNano    int64
	Line      string // raw JSONL line (caller extracts snippet)
	// Workspace is the recorded workspace path of the hit's session (""
	// when unattributed). Populated so callers can assert scoping.
	Workspace string
}

// ensureLoaded decodes an external index on first use (#5156 R1 lazy
// admission) under the total budget: evict least-recently-used decoded
// entries until the new one fits, or reject it if it alone exceeds the
// budget. Returns nil when the entry cannot be admitted.
func (m *Manager) ensureLoaded(id string, ent *extEntry) *Index {
	if ent.decoded {
		return ent.idx
	}
	if m.totalBudget <= 0 {
		// Plain-instance mode (no machine budget): decode on FIRST QUERY,
		// never at startup (#5149 scanlazy - scanExternal eagerly decoding
		// every sibling <= externalLimit put ~276MB live on the heap
		// before the first user interaction).
		return m.decodeInto(id, ent)
	}
	// LRU eviction until there is room.
	for m.totalDecoded+ent.idxSize > m.totalBudget && len(m.evictionOrder) > 0 {
		oldest := m.evictionOrder[0]
		m.evictionOrder = m.evictionOrder[1:]
		oe, ok := m.external[oldest]
		if !ok || !oe.decoded {
			continue
		}
		m.totalDecoded -= oe.idxSize
		m.evictedTotal += oe.idxSize
		m.evictedCount++
		oe.idx = nil
		oe.decoded = false
	}
	if m.totalDecoded+ent.idxSize > m.totalBudget {
		return nil // cannot fit even after full eviction - skip this session
	}
	if m.decodeInto(id, ent) == nil {
		return nil
	}
	m.totalDecoded += ent.idxSize
	m.evictionOrder = append(m.evictionOrder, id)
	return ent.idx
}

// decodeInto loads id's index into ent (no budget bookkeeping). Corrupt
// files degrade to source scan (#5156), never silently-empty results.
func (m *Manager) decodeInto(id string, ent *extEntry) *Index {
	ix, err := Load(m.dir, id)
	if err != nil || ix == nil {
		return nil
	}
	// QA (#5156): corrupt sibling idx -> degraded source scan, never an
	// empty result that silently hides the session.
	if isCorruptIdx(ix, ent.idxSize) {
		ent.degraded = true
		ent.decoded = false
		debug.Log("sessionidx", "degraded: corrupt idx for %s - serving from source jsonl", id)
		return nil
	}
	ent.idx = ix
	ent.decoded = true
	return ent.idx
}

// externalSorted returns external entries in the R2 deterministic order:
// newest mtime first, session id as tiebreak. The daemon's ordering IS the
// API once results cross the wire - map iteration order must never leak.
func (m *Manager) externalSorted() []struct {
	id  string
	ent *extEntry
} {
	m.mu.Lock()
	out := make([]struct {
		id  string
		ent *extEntry
	}, 0, len(m.external))
	for id, e := range m.external {
		out = append(out, struct {
			id  string
			ent *extEntry
		}{id, e})
	}
	m.mu.Unlock()
	sort.Slice(out, func(i, j int) bool {
		if out[i].ent.modTime.Equal(out[j].ent.modTime) {
			return out[i].id < out[j].id
		}
		return out[i].ent.modTime.After(out[j].ent.modTime)
	})
	return out
}

// Query searches self + external indexes. terms are ANDed; sessions are
// searched newest-activity first; limit caps total results.
// Query searches self + external indexes across ALL workspaces. Callers
// that must stay inside one workspace use QueryScoped.
func (m *Manager) Query(terms []string, sessionID string, limit int) []Result {
	return m.QueryScoped(terms, sessionID, limit, "")
}

// QueryScoped is Query restricted to sessions recorded under one
// workspace path (#5149). A non-empty workspace drops hits whose session
// is attributed to a different workspace; unattributed sessions
// (pre-workspace-era) are dropped too - "only my workspace" must not
// quietly include the rest of the machine. An empty workspace keeps the
// historic unscoped behavior.
func (m *Manager) QueryScoped(terms []string, sessionID string, limit int, workspace string) []Result {
	if limit <= 0 {
		limit = 20
	}
	var out []Result
	// #5149 workspace scoping: one small index.json read per query,
	// content-cached on size+mtime (LoadWorkspaceMap), so this costs no
	// extra per-session I/O.
	var wsMap map[string]string
	if workspace != "" {
		wsMap = LoadWorkspaceMap(m.dir)
		// Cold load (#5149): first query for this workspace decodes its
		// indexes; every query resets the idle-unload window.
		m.touchWorkspace(workspace)
	}
	allowed := func(sessID string) bool {
		if workspace == "" {
			return true
		}
		return wsMap[sessID] == workspace
	}
	push := func(sessID string, ix *Index) {
		if ix == nil || !allowed(sessID) {
			return
		}
		if ix.Capped() {
			// Self-budget cap: bounded source scan keeps recall correct
			// without the resident gigabytes.
			if len(out) < limit {
				for _, r := range scanSourceForTerms(m.dir, sessID, terms, limit-len(out)) {
					r.Workspace = wsMap[sessID]
					out = append(out, r)
				}
			}
			return
		}
		hits := ix.Query(terms, limit)
		for _, h := range hits {
			line, err := ix.ReadLineAt(h.Offset, h.Length)
			if err != nil {
				continue
			}
			out = append(out, Result{SessionID: sessID, Role: h.Role, TsNano: h.TsNano, Line: string(line), Workspace: wsMap[sessID]})
			if len(out) >= limit {
				return
			}
		}
	}
	if sessionID != "" {
		if sessionID == m.liveID {
			push(sessionID, m.Self())
		} else {
			m.mu.Lock()
			ent := m.external[sessionID]
			// R1: lazy admission - decode under the total budget on
			// first targeted query (same path as the full scan).
			var ix *Index
			var degraded bool
			if ent != nil {
				ix = m.ensureLoaded(sessionID, ent)
				degraded = ent.degraded
			}
			m.mu.Unlock()
			if ix != nil {
				push(sessionID, ix)
			} else if degraded && allowed(sessionID) {
				out = append(out, scanSourceForTerms(m.dir, sessionID, terms, limit)...)
			}
		}
		return out
	}
	// self first (most relevant), then externals in R2 deterministic order
	push(m.liveID, m.Self())
	if len(out) >= limit {
		return out[:limit]
	}
	for _, item := range m.externalSorted() {
		if !allowed(item.id) {
			continue // other workspace: never decode it just to discard hits
		}
		m.mu.Lock()
		ent := m.external[item.id]
		if ent == nil {
			m.mu.Unlock()
			continue
		}
		var ix *Index
		var degraded bool
		if ent != nil {
			ix = m.ensureLoaded(item.id, ent)
			degraded = ent.degraded
		}
		m.mu.Unlock()
		if ix == nil {
			if degraded && len(out) < limit {
				out = append(out, scanSourceForTerms(m.dir, item.id, terms, limit-len(out))...)
			}
			continue // over-budget or unloadable - skip, never crash
		}
		push(item.id, ix)
		if len(out) >= limit {
			break
		}
	}
	return out
}
