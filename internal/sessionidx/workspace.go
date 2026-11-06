package sessionidx

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// workspace.go: workspace attribution for cross-session recall (#5149).
//
// Sessions live in ONE machine-global directory (~/.ggcode/sessions), so
// without attribution a recall from workspace A leaks results from
// workspace B - and the tool's own description already promises "this
// workspace". The authoritative mapping is the sessions index
// (<sessionsDir>/index.json), whose entries carry a workspace path. The
// .idx files themselves carry no workspace (and must not: they are
// written by the owning instance only), so scoping is resolved from the
// index at query time - one small file read, cached, refreshed on the
// same cadence as the index scan.

type workspaceEntry struct {
	ID        string `json:"id"`
	Workspace string `json:"workspace"`
}

type workspaceIndex struct {
	Entries   []workspaceEntry `json:"entries"`
	Workspace []workspaceEntry `json:"sessions"` // tolerate alt key
}

var (
	wsCacheMu sync.RWMutex
	wsCache   map[string]wsCacheEntry
)

type wsCacheEntry struct {
	entries map[string]string
	modTime time.Time
	size    int64
}

// LoadWorkspaceMap returns sessionID -> workspace path for one sessions
// dir, refreshed when index.json changes (size+mtime keyed). A missing or
// unreadable index yields an empty map (callers then treat every session
// as unattributable rather than failing recall).
func LoadWorkspaceMap(sessionsDir string) map[string]string {
	path := filepath.Join(sessionsDir, "index.json")
	st, err := os.Stat(path)
	if err != nil {
		return map[string]string{}
	}
	wsCacheMu.RLock()
	c, ok := wsCache[path]
	wsCacheMu.RUnlock()
	if ok && c.size == st.Size() && c.modTime.Equal(st.ModTime()) {
		return c.entries
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		return map[string]string{}
	}
	var idx workspaceIndex
	if err := json.Unmarshal(raw, &idx); err != nil {
		return map[string]string{}
	}
	all := append(idx.Entries, idx.Workspace...)
	m := make(map[string]string, len(all))
	for _, e := range all {
		if e.ID != "" && e.Workspace != "" {
			m[e.ID] = e.Workspace
		}
	}
	wsCacheMu.Lock()
	if wsCache == nil {
		wsCache = map[string]wsCacheEntry{}
	}
	wsCache[path] = wsCacheEntry{entries: m, modTime: st.ModTime(), size: st.Size()}
	wsCacheMu.Unlock()
	return m
}

// WorkspaceOf returns the recorded workspace for a session id, or "" when
// the sessions index does not attribute it (pre-workspace-era sessions).
func WorkspaceOf(sessionsDir, sessionID string) string {
	return LoadWorkspaceMap(sessionsDir)[sessionID]
}
