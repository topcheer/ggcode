package im

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/cost"
	"github.com/topcheer/ggcode/internal/permission"
)

// AgentSlashOptions wires one-shot, text-output agent commands into the IM
// slash executor. These mirror TUI slash commands that need no interactive
// input (interactive ones like /edit, /copy stay TUI-only by design).
type AgentSlashOptions struct {
	// OnCost handles "/cost" (cross-session disk summary; the daemon has no
	// in-memory TUI session, so disk is the authoritative source).
	OnCost func() (string, error)
	// OnMode handles "/mode [name]": no arg shows the current permission
	// mode; with an arg it switches (ConfigPolicy.SetMode semantics).
	OnMode func(arg string) (string, error)
}

// ExecuteAgentSlashCommand handles query-style agent commands (/cost, /mode).
// Returns (response, handled); unhandled commands fall through to the
// caller's unknown-command path.
func ExecuteAgentSlashCommand(text string, opts AgentSlashOptions) (string, bool) {
	parts := strings.Fields(strings.TrimSpace(text))
	if len(parts) == 0 || !strings.HasPrefix(parts[0], "/") {
		return "", false
	}
	switch strings.ToLower(parts[0]) {
	case "/cost":
		if opts.OnCost == nil {
			return "Cost data not available in this mode.", true
		}
		resp, err := opts.OnCost()
		if err != nil {
			return fmt.Sprintf("Cost query failed: %v", err), true
		}
		return resp, true
	case "/mode":
		if opts.OnMode == nil {
			return "Permission mode not available in this mode.", true
		}
		arg := ""
		if len(parts) > 1 {
			arg = parts[1]
		}
		resp, err := opts.OnMode(arg)
		if err != nil {
			return fmt.Sprintf("Mode switch failed: %v", err), true
		}
		return resp, true
	}
	return "", false
}

// BuildCrossSessionCostSummary renders the cross-session cost summary from
// disk - the same authoritative source as the TUI's /cost all. Package-level
// so both inbound paths (daemon bridge and TUI remote) share one rendering.
func BuildCrossSessionCostSummary() (string, error) {
	dataDir := filepath.Join(config.ConfigDir(), "cost")
	mgr := cost.NewManager(cost.DefaultPricingTable(), dataDir)
	loaded := mgr.LoadAllFromDisk()
	if loaded == 0 {
		return "No cost data found yet. Cost files will appear under:\n" + dataDir, nil
	}
	allCosts := mgr.AllCosts()
	agg := mgr.AggregateAllCosts()

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Cross-Session Cost Summary (%d sessions):\n\n", loaded))
	maxShow := 10
	if len(allCosts) < maxShow {
		maxShow = len(allCosts)
	}
	for i := 0; i < maxShow; i++ {
		sc := allCosts[i]
		costStr := cost.FormatCost(sc.TotalCostUSD)
		if !sc.HasPricing {
			costStr = "(no pricing data)"
		}
		sb.WriteString(fmt.Sprintf("  %s (%s) - %s\n", sc.Model, sc.Provider, costStr))
	}
	if len(allCosts) > maxShow {
		sb.WriteString(fmt.Sprintf("  ... and %d more\n", len(allCosts)-maxShow))
	}
	sb.WriteString("\nAll-time total: " + cost.FormatCost(agg.TotalCostUSD))
	if agg.SessionsWithoutPricing > 0 {
		sb.WriteString(fmt.Sprintf(" (%d sessions without pricing data)", agg.SessionsWithoutPricing))
	}
	return sb.String(), nil
}

// agentCostSummary renders the cross-session cost summary from disk - the
// same authoritative source as the TUI's /cost all (the daemon has no TUI
// session object, and per-session in-memory usage lives in the TUI process).
func (b *DaemonBridge) agentCostSummary() (string, error) {
	return BuildCrossSessionCostSummary()
}

// agentModeQuery shows or switches the agent's permission mode. Switching
// mirrors the TUI /mode semantics: parse, apply via ConfigPolicy.SetMode.
func (b *DaemonBridge) agentModeQuery(arg string) (string, error) {
	b.mu.Lock()
	agentRef := b.agent
	b.mu.Unlock()
	if agentRef == nil {
		return "", fmt.Errorf("no agent attached")
	}
	policy := agentRef.PermissionPolicy()
	if arg == "" {
		return "Current permission mode: " + policy.Mode().String(), nil
	}
	newMode := permission.ParsePermissionMode(arg)
	if cp, ok := policy.(*permission.ConfigPolicy); ok {
		cp.SetMode(newMode)
		return "Permission mode switched to: " + newMode.String(), nil
	}
	return "", fmt.Errorf("mode switching not supported by the active policy (%T)", policy)
}
