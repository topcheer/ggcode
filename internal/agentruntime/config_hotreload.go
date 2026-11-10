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

type ConfigHotReload struct {
	configPath  string
	externalDir string
	access      *configAccess
	interval    time.Duration

	baselines map[string]fileBaseline

	mu      sync.Mutex
	polling bool
}

type fileBaseline struct {
	exists bool
	hash   string
}

func NewConfigHotReload(configPath string, access *configAccess) *ConfigHotReload {
	return &ConfigHotReload{
		configPath:  configPath,
		externalDir: config.ConfigDir(),
		access:      access,
		interval:    2 * time.Second,
		baselines:   make(map[string]fileBaseline),
	}
}

func (w *ConfigHotReload) watchedFiles() []string {
	return []string{
		w.configPath,
		filepath.Join(w.externalDir, "vendors.yaml"),
	}
}

func (w *ConfigHotReload) Start(ctx context.Context) {

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

		debug.Log("config-hotreload", "reload skipped (invalid yaml): %v", err)
		return
	}

	w.applyFreshConfig(fresh)
}

func (w *ConfigHotReload) applyFreshConfig(fresh *config.Config) {
	a := w.access
	if a == nil || a.cfg == nil {
		return
	}
	a.cfgMu.Lock()
	old := a.cfg

	if len(fresh.Vendors) > 0 {
		old.Vendors = fresh.Vendors
	}

	old.Fallback = fresh.Fallback

	old.Fallbacks = fresh.Fallbacks

	old.Gates = fresh.Gates

	old.KnightConfig = fresh.KnightConfig
	old.MaxIterations = fresh.MaxIterations
	old.SessionTokenBudget = fresh.SessionTokenBudget
	old.SessionCostLimitUSD = fresh.SessionCostLimitUSD
	old.ToolCallBudget = fresh.ToolCallBudget

	fallbackConfigured := old.Fallback.IsConfigured()
	vendorCount := len(old.Vendors)
	a.cfgMu.Unlock()

	debug.Log("config-hotreload", "config refreshed: vendors=%d fallback=%v",
		vendorCount, fallbackConfigured)

	if a.agentInst != nil {
		ApplySessionTokenBudget(a.agentInst, old)
		ApplySessionCostLimit(a.agentInst, old)
		ApplySessionTimeBudget(a.agentInst, old)
		ApplyToolCallBudget(a.agentInst, old)
		ApplySessionTimeout(a.agentInst, old, a.agentInst.IsAutopilotMode())
	}
}

func snapshotFile(path string) fileBaseline {
	data, err := os.ReadFile(path)
	if err != nil {
		return fileBaseline{exists: false}
	}
	sum := sha256.Sum256(data)
	return fileBaseline{exists: true, hash: hex.EncodeToString(sum[:])}
}
