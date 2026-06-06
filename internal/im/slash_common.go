package im

import (
	"fmt"
	"strings"
)

type CommonIMSlashOptions struct {
	HelpExtraLines []string
}

type CommonIMSlashResult struct {
	Handled         bool
	Response        string
	MuteSelfAdapter string
}

func ExecuteCommonIMSlashCommand(manager *Manager, selfAdapter, text string, opts CommonIMSlashOptions) CommonIMSlashResult {
	parts := strings.Fields(strings.TrimSpace(text))
	if len(parts) == 0 || !strings.HasPrefix(parts[0], "/") {
		return CommonIMSlashResult{}
	}

	switch strings.ToLower(parts[0]) {
	case "/listim":
		return CommonIMSlashResult{Handled: true, Response: FormatIMAdapterList(manager)}
	case "/muteim":
		return CommonIMSlashResult{Handled: true, Response: executeMuteIMCommand(manager, selfAdapter, parts)}
	case "/muteall":
		return CommonIMSlashResult{Handled: true, Response: executeMuteAllCommand(manager, selfAdapter)}
	case "/muteself":
		resp, adapter := executeMuteSelfCommand(manager, selfAdapter)
		return CommonIMSlashResult{Handled: true, Response: resp, MuteSelfAdapter: adapter}
	case "/help":
		return CommonIMSlashResult{Handled: true, Response: CommonIMHelpText(opts.HelpExtraLines...)}
	default:
		return CommonIMSlashResult{}
	}
}

func FormatIMAdapterList(manager *Manager) string {
	if manager == nil {
		return "IM manager not available"
	}
	snapshot := manager.Snapshot()
	if len(snapshot.Adapters) == 0 {
		return "📭 No IM adapters configured."
	}

	var sb strings.Builder
	sb.WriteString("📬 IM Adapters:\n")
	for _, a := range snapshot.Adapters {
		status := "✅ online"
		if !a.Healthy {
			status = "❌ " + a.Status
		}
		bound := ""
		for _, binding := range snapshot.CurrentBindings {
			if binding.Adapter == a.Name {
				if binding.Muted {
					bound = " 🔇 muted"
				} else {
					bound = " 📡 active"
				}
				break
			}
		}
		sb.WriteString(fmt.Sprintf("  • %s [%s]%s %s\n", a.Name, a.Platform, bound, status))
	}
	return sb.String()
}

func CommonIMHelpText(extraLines ...string) string {
	lines := []string{
		"Available commands:",
		"/listim - List IM adapters and their status",
		"/muteim <name> - Mute a specific adapter",
		"/muteall - Mute all adapters except the one you're using",
		"/muteself - Mute THIS adapter (⚠️ you'll stop receiving replies; use /restart from another adapter to recover)",
	}
	lines = append(lines, extraLines...)
	lines = append(lines, "/help - Show this help")
	return strings.Join(lines, "\n")
}

func DefaultMuteSelfWarning(adapter string) string {
	if strings.TrimSpace(adapter) == "" {
		return "🔇 Muting this adapter. You will stop receiving replies.\n💡 Use /restart from another adapter to recover."
	}
	return fmt.Sprintf("🔇 Muting adapter %s. You will stop receiving replies.\n💡 Use /restart from another adapter to recover.", adapter)
}

func executeMuteIMCommand(manager *Manager, selfAdapter string, parts []string) string {
	if manager == nil {
		return "IM manager not available"
	}
	if len(parts) < 2 {
		return "Usage: /muteim <adapter_name>\nUse /listim to see adapter names."
	}
	name := parts[1]
	if name == selfAdapter {
		return "⚠️ Cannot mute yourself. Use /muteself instead."
	}
	if err := manager.MuteBinding(name); err != nil {
		return fmt.Sprintf("❌ Failed to mute %s: %v", name, err)
	}
	return fmt.Sprintf("🔇 Muted adapter: %s", name)
}

func executeMuteAllCommand(manager *Manager, selfAdapter string) string {
	if manager == nil {
		return "IM manager not available"
	}
	count, err := manager.MuteAllExcept(selfAdapter)
	if err != nil {
		return fmt.Sprintf("❌ Failed to mute adapters: %v", err)
	}
	if selfAdapter != "" {
		return fmt.Sprintf("🔇 Muted %d adapter(s), keeping %s active", count, selfAdapter)
	}
	return fmt.Sprintf("🔇 Muted %d adapter(s)", count)
}

func executeMuteSelfCommand(manager *Manager, selfAdapter string) (string, string) {
	if manager == nil {
		return "IM manager not available", ""
	}
	if selfAdapter == "" {
		return "Cannot determine your adapter.", ""
	}
	return DefaultMuteSelfWarning(selfAdapter), selfAdapter
}
