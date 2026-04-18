package commands

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

var (
	disabledMu      sync.RWMutex
	disabledCache   map[string]bool
	disabledCacheOK bool
)

func disabledStatePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
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

	// Double-check after acquiring write lock
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

// ApplyDisabledState sets the Enabled field on each command based on persisted state.
// New skills (not in the persisted set) default to enabled.
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
		// else: defaults to whatever the loader set (true for new skills)
	}
}

// PersistEnabledState saves the enabled/disabled state for a single skill.
func PersistEnabledState(name string, enabled bool) {
	disabled := loadDisabledSet()
	if enabled {
		delete(disabled, normalizeSkillName(name))
	} else {
		disabled[normalizeSkillName(name)] = true
	}
	_ = saveDisabledSet(disabled)

	// Update cache
	disabledMu.Lock()
	disabledCache = disabled
	disabledMu.Unlock()
}

// InvalidateDisabledCache clears the in-memory cache so next load reads from disk.
func InvalidateDisabledCache() {
	disabledMu.Lock()
	disabledCacheOK = false
	disabledMu.Unlock()
}
