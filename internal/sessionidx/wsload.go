package sessionidx

import (
	"time"

	"github.com/topcheer/ggcode/internal/debug"
)

// wsload.go: workspace-granular COLD loading with idle unload (#5149).
//
// Usage of cross-session recall is sparse, so the shared-index daemon must
// not carry anything at rest: it starts with zero decoded indexes, and the
// FIRST query naming a workspace loads that workspace's unlocked indexes.
// A workspace with no request for DefaultWsIdleUnload (30min) is unloaded
// again; any request inside the window resets it.
//
// This composes with the R1 budget: activation decodes under the machine
// budget with LRU eviction, so even a burst across many workspaces stays
// bounded, while the idle janitor returns memory to zero when nothing is
// being asked for.

// DefaultWsIdleUnload is how long an activated workspace stays resident
// without a single query before its indexes are dropped from the heap.
const DefaultWsIdleUnload = 30 * time.Minute

// SetIdleUnload overrides the workspace idle-unload window (tests use a
// short one). Call before Start.
func (m *Manager) SetIdleUnload(d time.Duration) {
	m.mu.Lock()
	m.idleUnload = d
	m.mu.Unlock()
}

// ActiveWorkspaces counts the workspaces currently pinned in the heap
// (cold-load state, surfaced at /status).
func (m *Manager) ActiveWorkspaces() int { return len(m.activeWorkspaces()) }

// activeWorkspaces reports the currently activated workspaces (for /status
// and tests).
func (m *Manager) activeWorkspaces() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]string, 0, len(m.wsActive))
	for ws := range m.wsActive {
		out = append(out, ws)
	}
	return out
}

// touchWorkspace marks a workspace as recently used, activating it on
// first sight. Caller must NOT hold m.mu (activation decodes files).
func (m *Manager) touchWorkspace(workspace string) {
	if workspace == "" {
		return
	}
	m.mu.Lock()
	if m.wsActive == nil {
		m.wsActive = map[string]time.Time{}
	}
	_, existed := m.wsActive[workspace]
	m.wsActive[workspace] = time.Now()
	m.mu.Unlock()
	if !existed {
		m.activateWorkspace(workspace)
	}
}

// activateWorkspace decodes the indexes belonging to one workspace, under
// the machine budget. Already-decoded entries are left alone.
func (m *Manager) activateWorkspace(workspace string) {
	wsMap := LoadWorkspaceMap(m.dir)
	var ids []string
	m.mu.Lock()
	for id, ent := range m.external {
		if ent.decoded || id == m.liveID {
			continue
		}
		if wsMap[id] != workspace {
			continue
		}
		ids = append(ids, id)
	}
	m.mu.Unlock()
	if len(ids) == 0 {
		return
	}

	loaded := 0
	for _, id := range ids {
		m.mu.Lock()
		ent := m.external[id]
		if ent == nil || ent.decoded {
			m.mu.Unlock()
			continue
		}
		ix := m.ensureLoaded(id, ent)
		m.mu.Unlock()
		if ix != nil {
			loaded++
		}
	}
	debug.Log("sessionidx", "ws-activate %s: decoded %d/%d indexes (total %.1fMB)", workspace, loaded, len(ids), float64(m.TotalDecodedBytes())/(1<<20))
}

// unloadIdleWorkspaces drops every decoded index of workspaces whose last
// query is older than the idle window. Called from the background loop.
func (m *Manager) unloadIdleWorkspaces() {
	if m.idleUnload <= 0 {
		return
	}
	now := time.Now()

	m.mu.Lock()
	var expired []string
	for ws, last := range m.wsActive {
		if now.Sub(last) >= m.idleUnload {
			expired = append(expired, ws)
		}
	}
	m.mu.Unlock()
	if len(expired) == 0 {
		return
	}
	wsMap := LoadWorkspaceMap(m.dir)
	for _, ws := range expired {
		m.mu.Lock()
		freed := int64(0)
		count := 0
		for id, ent := range m.external {
			if !ent.decoded || wsMap[id] != ws {
				continue
			}
			m.totalDecoded -= ent.idxSize
			freed += ent.idxSize
			count++
			ent.idx = nil
			ent.decoded = false
		}
		// Drop them from the LRU order too.
		if len(m.evictionOrder) > 0 && count > 0 {
			keep := m.evictionOrder[:0]
			for _, id := range m.evictionOrder {
				ent := m.external[id]
				if ent != nil && ent.decoded {
					keep = append(keep, id)
				}
			}
			m.evictionOrder = keep
		}
		delete(m.wsActive, ws)
		m.mu.Unlock()
		debug.Log("sessionidx", "ws-unload %s: dropped %d indexes (%.1fMB) after %.0fmin idle", ws, count, float64(freed)/(1<<20), m.idleUnload.Minutes())
	}
}
