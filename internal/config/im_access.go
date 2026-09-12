package config

// im_access.go — locked read accessors for the IM adapter map (#2152).
//
// #1367/#1794 migrated all *writes* to c.IM.Adapters onto the Update loop
// (configMutationMsg), but the *reads* stayed behind in TUI Cmd goroutines:
// a bind/create Cmd that lives for seconds (waitForXxxAdapterHealthy polls
// up to 10s) races an Update-loop "d" toggle (`SetIMAdapterEnabled` map
// write) — Go fatal "concurrent map read and map write", process death.
// The same applies to `im.StartNamedAdapter(ctx, m.config.IM, ...)` which
// hands the live map (by struct copy) to a long-lived adapter.
//
// The runtime *writers* in config_save.go take imAdaptersMu for their
// read-modify-write sections; the accessors below take RLock. Load-time
// normalization (normalizeIMPlatforms / expandEnv) is single-threaded and
// intentionally unguarded.

// GetIMAdapter returns a deep copy of the named adapter's config. Safe to
// call from any goroutine; mutating the copy never touches live config.
func (c *Config) GetIMAdapter(name string) (IMAdapterConfig, bool) {
	if c == nil {
		return IMAdapterConfig{}, false
	}
	c.imAdaptersMu.RLock()
	defer c.imAdaptersMu.RUnlock()
	adapter, ok := c.IM.Adapters[name]
	if !ok {
		return IMAdapterConfig{}, false
	}
	return cloneIMAdapterConfig(adapter), true
}

// IMAdapterEnabled is the hot-path convenience accessor for render code
// (`Disabled: !m.config.IMAdapterEnabled(name)`).
func (c *Config) IMAdapterEnabled(name string) bool {
	adapter, ok := c.GetIMAdapter(name)
	return ok && adapter.Enabled
}

// IMSnapshot returns a deep copy of the whole IMConfig. The returned value
// owns its Adapters map: safe to hand to long-lived consumers such as
// im.StartNamedAdapter — later config writes never leak into them.
func (c *Config) IMSnapshot() IMConfig {
	if c == nil {
		return IMConfig{}
	}
	c.imAdaptersMu.RLock()
	defer c.imAdaptersMu.RUnlock()
	snap := c.IM // struct copy; Adapters still aliased below
	if c.IM.Adapters != nil {
		snap.Adapters = make(map[string]IMAdapterConfig, len(c.IM.Adapters))
		for name, adapter := range c.IM.Adapters {
			snap.Adapters[name] = cloneIMAdapterConfig(adapter)
		}
	}
	return snap
}

// cloneIMAdapterConfig deep-copies every reference field so callers can
// never race live config through a handed-out copy.
func cloneIMAdapterConfig(a IMAdapterConfig) IMAdapterConfig {
	a.Args = append([]string(nil), a.Args...)
	a.AllowFrom = append([]string(nil), a.AllowFrom...)
	if a.Env != nil {
		a.Env = copyStringMap(a.Env)
	}
	if a.Extra != nil {
		extra := make(map[string]interface{}, len(a.Extra))
		for k, v := range a.Extra {
			extra[k] = v
		}
		a.Extra = extra
	}
	if a.Targets != nil {
		targets := make([]IMTargetConfig, len(a.Targets))
		copy(targets, a.Targets)
		for i := range targets {
			if targets[i].Metadata != nil {
				targets[i].Metadata = copyStringMap(targets[i].Metadata)
			}
		}
		a.Targets = targets
	}
	return a
}

func copyStringMap(src map[string]string) map[string]string {
	dst := make(map[string]string, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}
