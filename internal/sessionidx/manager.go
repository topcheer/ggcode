package sessionidx

import (
	"os"
	"path/filepath"
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
	stop     chan struct{}
	stopped  sync.WaitGroup
}

type extEntry struct {
	idx     *Index
	idxSize int64
	modTime time.Time
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
			m.self = ix
		}
	}
	m.mu.Unlock()
	if m.self != nil {
		if err := m.self.Refresh(); err != nil {
			debug.Log("sessionidx", "start: self refresh: %v", err)
		}
	}
	m.scanExternal()
	m.stopped.Add(1)
	safego.Go("sessionidx.loop", m.loop)
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
		ix, err := Load(m.dir, id)
		if err != nil || ix == nil {
			continue
		}
		ext[id] = &extEntry{idx: ix, idxSize: info.Size(), modTime: info.ModTime()}
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
		ix, err := Load(m.dir, id)
		if err != nil || ix == nil {
			continue
		}
		m.external[id] = &extEntry{idx: ix, idxSize: st.Size(), modTime: st.ModTime()}
		debug.Log("sessionidx", "refresh: reloaded external idx %s", id)
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
}

// Query searches self + external indexes. terms are ANDed; sessions are
// searched newest-activity first; limit caps total results.
func (m *Manager) Query(terms []string, sessionID string, limit int) []Result {
	if limit <= 0 {
		limit = 20
	}
	var out []Result
	push := func(sessID string, ix *Index) {
		if ix == nil {
			return
		}
		hits := ix.Query(terms, limit)
		for _, h := range hits {
			line, err := ix.ReadLineAt(h.Offset, h.Length)
			if err != nil {
				continue
			}
			out = append(out, Result{SessionID: sessID, Role: h.Role, TsNano: h.TsNano, Line: string(line)})
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
			m.mu.Unlock()
			if ent != nil {
				push(sessionID, ent.idx)
			}
		}
		return out
	}
	// self first (most relevant), then externals
	push(m.liveID, m.Self())
	if len(out) >= limit {
		return out[:limit]
	}
	m.mu.Lock()
	ents := make([]*extEntry, 0, len(m.external))
	for _, e := range m.external {
		ents = append(ents, e)
	}
	m.mu.Unlock()
	for _, e := range ents {
		push(e.idx.Session, e.idx)
		if len(out) >= limit {
			break
		}
	}
	return out
}
