package agentruntime

import (
	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/permission"
)

func InteractivePermissionMode(cfg *config.Config, bypass bool) permission.PermissionMode {
	return InteractivePermissionModeWithDefault(cfg, bypass, "")
}

func InteractivePermissionModeWithDefault(cfg *config.Config, bypass bool, defaultMode string) permission.PermissionMode {
	modeStr := defaultMode
	if cfg != nil && cfg.DefaultMode != "" {
		modeStr = cfg.DefaultMode
	}
	mode := permission.ParsePermissionMode(modeStr)
	if bypass {
		mode = permission.BypassMode
	}
	return mode
}

func BuildInteractivePermissionPolicy(cfg *config.Config, workingDir string, bypass bool) permission.PermissionPolicy {
	if cfg == nil {
		return nil
	}
	allowedDirs := cfg.ExpandAllowedDirs(workingDir)
	rules := make(map[string]permission.Decision)
	for toolName, perm := range cfg.ToolPerms {
		switch config.ToolPermission(perm) {
		case "allow":
			rules[toolName] = permission.Allow
		case "deny":
			rules[toolName] = permission.Deny
		default:
			rules[toolName] = permission.Ask
		}
	}
	mode := InteractivePermissionMode(cfg, bypass)
	return permission.NewConfigPolicyWithMode(rules, allowedDirs, mode)
}
