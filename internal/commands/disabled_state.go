package commands

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"

	"github.com/topcheer/ggcode/internal/config"
)

var (
	disabledMu      sync.RWMutex
	disabledCache   map[string]bool
	disabledCacheOK bool
)

func disabledStatePath() (string, error) {
	home := config.HomeDir()
	return filepath.Join(home, ".ggcode", "disabled_skills.json"), nil
}

func loadDisabledSet() map[string]bool {
	disabledMu.RLock()
	if disabledCacheOK {
		c := disabledCache
		disabledMu.RUnlock()
		return c
	}
	disabledMu.RUnlock()

	disabledMu.Lock()
	defer disabledMu.Unlock()
	return loadDisabledSetLocked()
}

func loadDisabledSetLocked() map[string]bool {
	if disabledCacheOK {
		return disabledCache
	}

	path, err := disabledStatePath()
	if err != nil {
		disabledCache = map[string]bool{}
		disabledCacheOK = true
		return disabledCache
	}
	data, err := os.ReadFile(path)
	if err != nil {
		disabledCache = map[string]bool{}
		disabledCacheOK = true
		return disabledCache
	}
	var names []string
	if err := json.Unmarshal(data, &names); err != nil {
		disabledCache = map[string]bool{}
		disabledCacheOK = true
		return disabledCache
	}
	m := make(map[string]bool, len(names))
	for _, n := range names {
		m[normalizeSkillName(n)] = true
	}
	disabledCache = m
	disabledCacheOK = true
	return m
}

func saveDisabledSet(disabled map[string]bool) error {
	path, err := disabledStatePath()
	if err != nil {
		return err
	}
	names := make([]string, 0, len(disabled))
	for n, v := range disabled {
		if v {
			names = append(names, n)
		}
	}
	data, err := json.MarshalIndent(names, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func ApplyDisabledState(cmds map[string]*Command) {
	disabled := loadDisabledSet()
	for name, cmd := range cmds {
		if cmd == nil {
			continue
		}
		if cmd.IsBuiltin() {
			cmd.Enabled = true
			continue
		}
		if disabled[name] {
			cmd.Enabled = false
		}
	}
}

func PersistEnabledState(name string, enabled bool) {
	disabledMu.Lock()
	defer disabledMu.Unlock()
	cached := disabledCache
	if !disabledCacheOK {
		cached = loadDisabledSetLocked()
	}
	disabled := make(map[string]bool, len(cached)+1)
	for k, v := range cached {
		disabled[k] = v
	}
	if enabled {
		delete(disabled, normalizeSkillName(name))
	} else {
		disabled[normalizeSkillName(name)] = true
	}
	_ = saveDisabledSet(disabled)

	disabledCache = disabled
	disabledCacheOK = true
}

func InvalidateDisabledCache() {
	disabledMu.Lock()
	disabledCacheOK = false
	disabledMu.Unlock()
}
