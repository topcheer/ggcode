package agentruntime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/plugin"
	"github.com/topcheer/ggcode/internal/safego"
)

// MCPHotReload watches mcp_servers.yaml for changes and triggers a hot-reload
// of MCP server connections via MCPManager.Reload(). It uses polling (no fsnotify
// dependency) with a 2-second interval and a debounce window to coalesce rapid
// writes (e.g. editors that write via temp file + rename).
type MCPHotReload struct {
	mcpPath    string // path to the GLOBAL mcp_servers.yaml (always watched)
	workingDir string // for MergeStartupServers
	manager    *plugin.MCPManager
	// watched holds per-path watch state (#521): every watched file is
	// judged against ITS OWN (mtime, sha256) baseline. The previous single
	// global mtime watermark let a newer global file permanently mask
	// workspace mcp_servers.yaml edits, and scope files had no content-hash
	// debounce at all.
	watched  map[string]*watchState
	interval time.Duration
}

// watchState records the last-observed state of one watched file (#521).
type watchState struct {
	exists bool
	mtime  time.Time
	hash   string // sha256 of content at last observation
}

// NewMCPHotReload creates a watcher for the given config directory.
func NewMCPHotReload(configDir, workingDir string, mgr *plugin.MCPManager) *MCPHotReload {
	return &MCPHotReload{
		mcpPath:    config.MCPServersPath(configDir),
		workingDir: workingDir,
		manager:    mgr,
		watched:    make(map[string]*watchState),
		interval:   2 * time.Second,
	}
}

// resolveScopeMCPServers computes the effective server list for the session's
// scope (#497): when the working dir has its own ggcode.yaml (workspace
// scope), the session's manager runs the WORKSPACE mcp_servers.yaml — the
// same file resolution wailskit's LoadConfigForWorkspace performs. The old
// code always fed the GLOBAL file's list into Reload, so a global-file edit
// kicked the workspace session's workspace-only servers out as "removed"
// (name-diff, no scope awareness) and silently injected global-only servers
// into the workspace session. Global scope (no workspace yaml) keeps reading
// the global file, matching the pre-#497 behavior for unaffected users.
func (w *MCPHotReload) resolveScopeMCPServers() []config.MCPServerConfig {
	if w.workingDir != "" {
		for _, rel := range []string{"mcp_servers.yaml", filepath.Join(".ggcode", "mcp_servers.yaml")} {
			wsPath := filepath.Join(w.workingDir, rel)
			if _, err := os.Stat(wsPath); err == nil {
				return config.LoadMCPServersPublic(wsPath)
			}
		}
	}
	return config.LoadMCPServersPublic(w.mcpPath)
}

// Start launches the watcher goroutine. It returns immediately.
// The watcher runs until ctx is cancelled.
func (w *MCPHotReload) Start(ctx context.Context) {
	// #521: seed per-path (mtime, sha256) baselines for every watched file
	// so the first poll does not fire, and so each file's later edits are
	// compared against its own state instead of a single global watermark.
	// Files that do not exist yet are recorded as absent, so their later
	// APPEARANCE is treated as a change.
	// #497: workspace-scoped sessions must also react to edits of their OWN
	// mcp_servers.yaml — previously only the global file was watched, so
	// manual edits to the workspace file never reloaded the session's manager.
	for _, p := range w.watchedPaths() {
		if w.seedState(p) {
			debug.Log("mcp-hotreload", "watching %s (baseline seeded)", p)
		}
	}

	safego.Go("mcp.hotreload", func() {
		debug.Log("mcp-hotreload", "watching %s (interval=%v)", w.mcpPath, w.interval)
		ticker := time.NewTicker(w.interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				debug.Log("mcp-hotreload", "watcher stopped")
				return
			case <-ticker.C:
				w.checkAndReload(ctx)
			}
		}
	})
}

// scopeWatchPaths returns the scope-specific files to watch in addition to
// the global file: for a workspace-scoped session, its workspace
// mcp_servers.yaml (#497).
func (w *MCPHotReload) scopeWatchPaths() []string {
	if w.workingDir == "" {
		return nil
	}
	var paths []string
	for _, rel := range []string{"mcp_servers.yaml", filepath.Join(".ggcode", "mcp_servers.yaml")} {
		p := filepath.Join(w.workingDir, rel)
		if _, err := os.Stat(p); err == nil {
			paths = append(paths, p)
		}
	}
	return paths
}

func (w *MCPHotReload) checkAndReload(ctx context.Context) {
	// #497: watch both the global file and the session's scope file. Whichever
	// changed, the reload input must be the SCOPE-RESOLVED list, not blindly
	// the global file's contents.
	// #521: every watched path carries its own (mtime, sha256) state — a
	// newer global file can no longer mask an older-timestamped workspace
	// edit, and mtime-only bumps (touch / editor rewrite) are debounced by
	// content hash on ALL watched paths, not just the global one.
	changed := false
	for _, p := range w.watchedPaths() {
		if w.pathChanged(p) {
			changed = true
		}
	}
	if !changed {
		return
	}

	// Debounce: wait a short window to let multi-write operations settle.
	time.Sleep(500 * time.Millisecond)

	debug.Log("mcp-hotreload", "change detected, reloading MCP servers")

	// Scope-resolved list (#497): workspace sessions reload from their own
	// mcp_servers.yaml; global sessions from the global file — matching the
	// manager's initial set computation instead of clobbering it.
	//
	// NOTE: deliberately NO MergeStartupServers here. The startup merge
	// (interactive_core.go) is a one-time migration of Claude sources
	// (.mcp.json / ~/.claude.json). Re-running it on every reload turned
	// those sources into "forced-present" entries: deleting a server that
	// also exists in a Claude source was resurrected on the next poll
	// (mergeServers only dedupes entries still present in the ggcode list;\t// a deleted name is re-added from the source). Deletion — via config
	// tool or manual file edit — must take effect; .mcp.json additions
	// still land on next startup's migration.
	servers := w.resolveScopeMCPServers()
	w.manager.Reload(ctx, servers)
}

// watchedPaths returns every path currently under watch: the global file
// plus the session's scope files (#497). Scope files are resolved live so a
// workspace mcp_servers.yaml created after Start is picked up as well.
func (w *MCPHotReload) watchedPaths() []string {
	paths := []string{w.mcpPath}
	for _, p := range w.scopeWatchPaths() {
		if !slices.Contains(paths, p) {
			paths = append(paths, p)
		}
	}
	return paths
}

// pathChanged reports whether path's content changed since the last
// observation, updating the recorded per-path state. An mtime bump with
// identical content does not count (#521 Bug A: hash debounce now applies
// to every watched path, not just the global file).
func (w *MCPHotReload) pathChanged(path string) bool {
	st, seeded := w.watched[path]
	info, err := os.Stat(path)
	if err != nil {
		if seeded {
			st.exists = false // disappeared; reappearance will be a change
		}
		return false
	}
	if !seeded {
		// Never seen before: either Start hasn't run (direct invocation) or
		// the file appeared after Start seeded the then-existing files —
		// treat the appearance as a change so fresh scope configs hot-reload.
		return w.seedState(path)
	}
	if !st.exists {
		// Appeared since the last poll.
		st.exists = true
		st.mtime = info.ModTime()
		st.hash = hashFile(path)
		return true
	}
	if !info.ModTime().After(st.mtime) {
		return false
	}
	h := hashFile(path)
	if h != "" && h == st.hash {
		st.mtime = info.ModTime() // content unchanged: just an mtime bump
		return false
	}
	st.mtime = info.ModTime()
	st.hash = h
	return true
}

// seedState records the current (mtime, hash) of path as its baseline and
// reports whether the file exists (#521).
func (w *MCPHotReload) seedState(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		w.watched[path] = &watchState{}
		return false
	}
	w.watched[path] = &watchState{exists: true, mtime: info.ModTime(), hash: hashFile(path)}
	return true
}

// hashFile returns the hex sha256 of path's content, or "" if unreadable.
func hashFile(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
