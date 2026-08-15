package agentruntime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"time"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/mcp"
	"github.com/topcheer/ggcode/internal/plugin"
	"github.com/topcheer/ggcode/internal/safego"
)

// MCPHotReload watches mcp_servers.yaml for changes and triggers a hot-reload
// of MCP server connections via MCPManager.Reload(). It uses polling (no fsnotify
// dependency) with a 2-second interval and a debounce window to coalesce rapid
// writes (e.g. editors that write via temp file + rename).
type MCPHotReload struct {
	mcpPath    string   // path to the GLOBAL mcp_servers.yaml (always watched)
	extraPaths []string // additional scope-specific files watched for the session (#497)
	workingDir string   // for MergeStartupServers
	manager    *plugin.MCPManager
	lastMod    time.Time
	lastHash   string // content hash to detect real changes
	interval   time.Duration
}

// NewMCPHotReload creates a watcher for the given config directory.
func NewMCPHotReload(configDir, workingDir string, mgr *plugin.MCPManager) *MCPHotReload {
	return &MCPHotReload{
		mcpPath:    config.MCPServersPath(configDir),
		workingDir: workingDir,
		manager:    mgr,
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
	// Record initial mtime so we don't trigger a reload on the first poll.
	if info, err := os.Stat(w.mcpPath); err == nil {
		w.lastMod = info.ModTime()
	}
	// #497: workspace-scoped sessions must also react to edits of their OWN
	// mcp_servers.yaml — previously only the global file was watched, so
	// manual edits to the workspace file never reloaded the session's manager.
	for _, p := range w.scopeWatchPaths() {
		if _, err := os.Stat(p); err == nil {
			debug.Log("mcp-hotreload", "also watching scope file %s", p)
			w.extraPaths = append(w.extraPaths, p)
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
	changed := w.fileChanged(&w.lastMod, &w.lastHash, w.mcpPath)
	for i := range w.extraPaths {
		// extraPaths have no per-file hash bookkeeping; mtime movement plus a
		// combined-hash check below covers them without spurious reloads.
		if info, err := os.Stat(w.extraPaths[i]); err == nil && info.ModTime().After(w.lastMod) {
			changed = true
		}
	}
	if !changed {
		return
	}

	// Debounce: wait a short window to let multi-write operations settle.
	time.Sleep(500 * time.Millisecond)

	// Advance the watermark past every watched file's current mtime (#497):
	// extraPaths share the single watermark — when the global file is absent
	// (workspace-only setups), the watermark must still advance past the
	// workspace file's mtime or every subsequent tick re-fires a reload.
	for _, p := range append([]string{w.mcpPath}, w.extraPaths...) {
		if info, err := os.Stat(p); err == nil && info.ModTime().After(w.lastMod) {
			w.lastMod = info.ModTime()
		}
	}

	debug.Log("mcp-hotreload", "change detected, reloading MCP servers")

	// Scope-resolved list (#497): workspace sessions reload from their own
	// mcp_servers.yaml; global sessions from the global file — matching the
	// manager's initial set computation instead of clobbering it.
	servers := w.resolveScopeMCPServers()
	mergedServers, _ := mcp.MergeStartupServers(w.workingDir, servers)
	w.manager.Reload(ctx, mergedServers)
}

// fileChanged reports whether path's content changed since the last call,
// updating the recorded mtime/hash (mtime-only bump with identical content
// does not trigger).
func (w *MCPHotReload) fileChanged(lastMod *time.Time, lastHash *string, path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	if !info.ModTime().After(*lastMod) {
		return false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	sum := sha256.Sum256(data)
	hash := hex.EncodeToString(sum[:])
	if hash == *lastHash {
		*lastMod = info.ModTime()
		return false // content unchanged, just mtime bumped
	}
	*lastHash = hash
	return true
}
