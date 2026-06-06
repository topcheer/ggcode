package agentruntime

import (
	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/permission"
)

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
	mode := permission.ParsePermissionMode(cfg.DefaultMode)
	if bypass {
		mode = permission.BypassMode
	}
	return permission.NewConfigPolicyWithMode(rules, allowedDirs, mode)
}
