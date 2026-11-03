package config

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

func (c *Config) IMAdapterEnabled(name string) bool {
	adapter, ok := c.GetIMAdapter(name)
	return ok && adapter.Enabled
}

func (c *Config) IMSnapshot() IMConfig {
	if c == nil {
		return IMConfig{}
	}
	c.imAdaptersMu.RLock()
	defer c.imAdaptersMu.RUnlock()
	snap := c.IM
	if c.IM.Adapters != nil {
		snap.Adapters = make(map[string]IMAdapterConfig, len(c.IM.Adapters))
		for name, adapter := range c.IM.Adapters {
			snap.Adapters[name] = cloneIMAdapterConfig(adapter)
		}
	}
	return snap
}

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
