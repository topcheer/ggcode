package agentruntime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
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
	mcpPath    string // path to mcp_servers.yaml
	workingDir string // for MergeStartupServers
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

// Start launches the watcher goroutine. It returns immediately.
// The watcher runs until ctx is cancelled.
func (w *MCPHotReload) Start(ctx context.Context) {
	// Record initial mtime so we don't trigger a reload on the first poll.
	if info, err := os.Stat(w.mcpPath); err == nil {
		w.lastMod = info.ModTime()
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

func (w *MCPHotReload) checkAndReload(ctx context.Context) {
	info, err := os.Stat(w.mcpPath)
	if err != nil {
		return
	}
	if !info.ModTime().After(w.lastMod) {
		return
	}

	// Read content hash to avoid spurious reloads when mtime changes
	// but the file content is identical (common with some editors/tools).
	data, err := os.ReadFile(w.mcpPath)
	if err != nil {
		return
	}
	sum := sha256.Sum256(data)
	hash := hex.EncodeToString(sum[:])
	if hash == w.lastHash {
		w.lastMod = info.ModTime()
		return // content unchanged, just mtime bumped
	}

	// Debounce: wait a short window to let multi-write operations settle.
	time.Sleep(500 * time.Millisecond)

	info, err = os.Stat(w.mcpPath)
	if err != nil {
		return
	}
	w.lastMod = info.ModTime()
	w.lastHash = hash

	debug.Log("mcp-hotreload", "change detected, reloading MCP servers")

	servers := config.LoadMCPServersPublic(w.mcpPath)
	mergedServers, _ := mcp.MergeStartupServers(w.workingDir, servers)
	w.manager.Reload(ctx, mergedServers)
}
