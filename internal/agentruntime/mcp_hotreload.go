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
	"github.com/topcheer/ggcode/internal/mcp"
	"github.com/topcheer/ggcode/internal/plugin"
	"github.com/topcheer/ggcode/internal/safego"
)

type MCPHotReload struct {
	mcpPath    string
	configDir  string
	workingDir string
	manager    *plugin.MCPManager
	watched    map[string]*watchState
	interval   time.Duration
}

type watchState struct {
	exists bool
	mtime  time.Time
	hash   string
}

func NewMCPHotReload(configDir, workingDir string, mgr *plugin.MCPManager) *MCPHotReload {
	return &MCPHotReload{
		mcpPath:    config.MCPServersPath(configDir),
		configDir:  configDir,
		workingDir: workingDir,
		manager:    mgr,
		watched:    make(map[string]*watchState),
		interval:   2 * time.Second,
	}
}

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

func (w *MCPHotReload) Start(ctx context.Context) {

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

	changed := false
	for _, p := range w.watchedPaths() {
		if w.pathChanged(p) {
			changed = true
		}
	}
	if !changed {
		return
	}

	time.Sleep(500 * time.Millisecond)

	debug.Log("mcp-hotreload", "change detected, reloading MCP servers")

	servers := w.resolveScopeMCPServers()
	deleted := config.LoadMCPDeleted(w.configDir)
	merged, mergeWarnings := mcp.MergeStartupServersWithDeleted(w.workingDir, servers, deleted)

	merged, gateWarnings := applyProjectMCPGate(merged, w.workingDir)
	for _, warning := range append(mergeWarnings, gateWarnings...) {
		debug.Log("mcp", "%s", warning)
	}
	w.manager.Reload(ctx, merged)
}

func (w *MCPHotReload) watchedPaths() []string {
	paths := []string{w.mcpPath}
	for _, p := range w.scopeWatchPaths() {
		if !slices.Contains(paths, p) {
			paths = append(paths, p)
		}
	}

	if grantsPath := mcp.ProjectGrantsPath(w.workingDir); grantsPath != "" && !slices.Contains(paths, grantsPath) {
		paths = append(paths, grantsPath)
	}
	return paths
}

func (w *MCPHotReload) pathChanged(path string) bool {
	st, seeded := w.watched[path]
	info, err := os.Stat(path)
	if err != nil {
		if seeded {
			st.exists = false
		}
		return false
	}
	if !seeded {

		return w.seedState(path)
	}
	if !st.exists {

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
		st.mtime = info.ModTime()
		return false
	}
	st.mtime = info.ModTime()
	st.hash = h
	return true
}

func (w *MCPHotReload) seedState(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		w.watched[path] = &watchState{}
		return false
	}
	w.watched[path] = &watchState{exists: true, mtime: info.ModTime(), hash: hashFile(path)}
	return true
}

func hashFile(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
