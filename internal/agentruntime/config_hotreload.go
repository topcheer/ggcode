package agentruntime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/safego"
)

// ConfigHotReload watches ggcode.yaml (+ vendors.yaml) for content
// changes and refreshes the in-memory Config so edits made in one ggcode
// instance (or by hand) propagate to every instance on the machine.
//
// Scope is deliberately field-conservative (#763): config is a deep snapshot
// captured by the agent build chain, so a reload must not disturb a running
// agent loop. The watcher therefore:
//   - NEVER touches the provider mid-run. Provider/fallback changes are
//     "next-turn effective": the new config is swapped into the ConfigAccess
//     (whose reloadProvider already exists for /model switches), and the
//     provider rebuild happens between turns via the same channel a manual
//     switch uses. An idle agent gets the new provider immediately; a busy
//     one finishes its turn on the old provider first.
//   - Refreshes vendor definitions (endpoints, models lists, API keys) on
//     the existing Config object via field-level merge, so name lookups
//     (ResolveEndpoint, EndpointNames, /model lists) see new entries without
//     any structural swap.
//   - Ignores unknown/invalid files: a broken YAML keeps the last good
//     config (fail-open to the snapshot, never crash on bad edit).
//
// Detection: polling (2s, same pattern as MCPHotReload - no fsnotify
// dependency) comparing sha256 of each watched file against its own
// baseline, with mtime prefilter to keep idle cost near zero.

// HotReloadFieldPolicy classifies which top-level config fields may be
// refreshed in place on a live Config. Everything not listed here keeps the
// startup snapshot value (restart to change) - conservative by default.
type HotReloadFieldPolicy struct{}

// ConfigHotReload is the watcher for ggcode.yaml + external config files.
type ConfigHotReload struct {
	configPath  string // ggcode.yaml (or workspace equivalent)
	externalDir string // config dir holding vendors.yaml / im.yaml
	access      *configAccess
	interval    time.Duration

	baselines map[string]fileBaseline

	// polling (guarded by mu) is true while pollOnce runs. It lets tests
	// observe that the poll loop has quiesced after ctx cancel, so temp-dir
	// cleanup does not race an in-flight config.Load rewriting the file.
	mu      sync.Mutex
	polling bool
}

type fileBaseline struct {
	exists bool
	hash   string
}

// NewConfigHotReload creates the watcher. configPath is the ggcode.yaml the
// session loaded; externalDir is config.ConfigDir().
func NewConfigHotReload(configPath string, access *configAccess) *ConfigHotReload {
	return &ConfigHotReload{
		configPath:  configPath,
		externalDir: config.ConfigDir(),
		access:      access,
		interval:    2 * time.Second,
		baselines:   make(map[string]fileBaseline),
	}
}

// watchedFiles returns the files whose edits this watcher reacts to.
// #876: im.yaml is deliberately NOT watched — applyFreshConfig cannot apply
// IM fields (the IM manager holds the startup snapshot and a live reconnect
// is out of scope for the safe field-level refresh). Watching it anyway made
// every edit log "config refreshed" while nothing changed for IM.
func (w *ConfigHotReload) watchedFiles() []string {
	return []string{
		w.configPath,
		filepath.Join(w.externalDir, "vendors.yaml"),
	}
}

// Start launches the polling goroutine. Returns immediately.
func (w *ConfigHotReload) Start(ctx context.Context) {
	// Seed baselines so the first poll does not fire on existing content.
	for _, p := range w.watchedFiles() {
		w.baselines[p] = snapshotFile(p)
	}
	safego.Go("agentruntime.configHotReload", func() {
		ticker := time.NewTicker(w.interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				w.pollOnce()
			}
		}
	})
}

func (w *ConfigHotReload) pollOnce() {
	w.mu.Lock()
	w.polling = true
	w.mu.Unlock()
	defer func() {
		w.mu.Lock()
		w.polling = false
		w.mu.Unlock()
	}()

	changed := false
	for _, p := range w.watchedFiles() {
		cur := snapshotFile(p)
		base, ok := w.baselines[p]
		if !ok || cur.exists != base.exists || cur.hash != base.hash {
			w.baselines[p] = cur
			if cur.exists {
				changed = true
			}
		}
	}
	if !changed {
		return
	}

	fresh, err := config.Load(w.configPath)
	if err != nil {
		// Broken edit: keep the last good snapshot. The user's editor will
		// show the YAML error on their side; we log it once per change.
		debug.Log("config-hotreload", "reload skipped (invalid yaml): %v", err)
		return
	}

	w.applyFreshConfig(fresh)
}

// applyFreshConfig merges freshly-loaded config into the live session.
func (w *ConfigHotReload) applyFreshConfig(fresh *config.Config) {
	a := w.access
	if a == nil || a.cfg == nil {
		return
	}
	a.cfgMu.Lock()
	old := a.cfg

	// --- Field-level refresh (safe on live objects) ---
	// Vendor definitions: new endpoints/models/keys become visible to
	// lookups immediately. Provider selection itself (Vendor/Endpoint/Model)
	// is session-scoped (#541): keep the session's selection.
	if len(fresh.Vendors) > 0 {
		old.Vendors = fresh.Vendors
	}

	// Fallback: consumed only when a provider is (re)built - next turn
	// effective by construction, no mid-run impact.
	old.Fallback = fresh.Fallback
	// Fallbacks chain (#1482): FallbackChain consumes BOTH the legacy single
	// entry and the modern array. Refreshing only the legacy field left the
	// array frozen at the startup snapshot while the "config refreshed"
	// log implied success - failover-chain edits silently needed a restart.
	old.Fallbacks = fresh.Fallbacks

	// Knight budgets and iteration caps: consumed per-turn by Apply* calls;
	// next-turn effective.
	old.KnightConfig = fresh.KnightConfig
	old.MaxIterations = fresh.MaxIterations
	old.SessionTokenBudget = fresh.SessionTokenBudget
	old.ToolCallBudget = fresh.ToolCallBudget
	a.cfgMu.Unlock()

	debug.Log("config-hotreload", "config refreshed: vendors=%d fallback=%v",
		len(old.Vendors), old.Fallback.IsConfigured())

	// Re-apply turn-scoped budgets so the next turn picks them up.
	if a.agentInst != nil {
		ApplySessionTokenBudget(a.agentInst, old)
		ApplyToolCallBudget(a.agentInst, old)
		ApplySessionTimeout(a.agentInst, old, false)
	}
}

// snapshotFile hashes a file's content (mtime prefilter is implicit: we
// hash every poll; files are small - a few hundred KB worst case for
// vendors.yaml with long model lists, ~microseconds of sha256).
func snapshotFile(path string) fileBaseline {
	data, err := os.ReadFile(path)
	if err != nil {
		return fileBaseline{exists: false}
	}
	sum := sha256.Sum256(data)
	return fileBaseline{exists: true, hash: hex.EncodeToString(sum[:])}
}
