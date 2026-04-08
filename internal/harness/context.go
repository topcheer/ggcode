package harness

import (
	"fmt"
	"path/filepath"
	"strings"
)

func ResolveContext(cfg *Config, raw string) (*ContextConfig, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	if cfg == nil {
		return nil, fmt.Errorf("no harness config loaded for context %q", raw)
	}
	cleanRaw := filepath.Clean(raw)
	for _, contextCfg := range cfg.Contexts {
		if strings.EqualFold(contextCfg.Name, raw) || filepath.Clean(contextCfg.Path) == cleanRaw {
			match := contextCfg
			return &match, nil
		}
	}
	return nil, fmt.Errorf("unknown harness context %q", raw)
}

func ResolveTaskContext(cfg *Config, task *Task) *ContextConfig {
	if cfg == nil || task == nil {
		return nil
	}
	match, err := ResolveContext(cfg, firstNonEmptyText(task.ContextPath, task.ContextName))
	if err != nil {
		return nil
	}
	return match
}
