package im

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/topcheer/ggcode/internal/agent"
	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/cost"
	"github.com/topcheer/ggcode/internal/permission"
	"github.com/topcheer/ggcode/internal/provider"
)

// Path-A (daemon bridge) implementation of SlashDeps. The agent and disk
// stores are the data sources; the TUI-attached path supplies its own live
// implementation from the model state.

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

// buildDiskUsageSummary aggregates token counts across all persisted
// sessions - the /usage view when no live session object is at hand.
func buildDiskUsageSummary() (string, error) {
	dataDir := filepath.Join(config.ConfigDir(), "cost")
	mgr := cost.NewManager(cost.DefaultPricingTable(), dataDir)
	loaded := mgr.LoadAllFromDisk()
	if loaded == 0 {
		return "No usage data found yet.", nil
	}
	agg := mgr.AggregateAllCosts()
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Token Usage (%d sessions, all-time):\n\n", loaded))
	sb.WriteString(fmt.Sprintf("  Input tokens:  %s\n", humanCount(agg.InputTokens)))
	sb.WriteString(fmt.Sprintf("  Output tokens: %s\n", humanCount(agg.OutputTokens)))
	if agg.CacheReadTokens > 0 {
		sb.WriteString(fmt.Sprintf("  Cache read:    %s\n", humanCount(agg.CacheReadTokens)))
	}
	if agg.CacheWriteTokens > 0 {
		sb.WriteString(fmt.Sprintf("  Cache write:   %s\n", humanCount(agg.CacheWriteTokens)))
	}
	total := agg.InputTokens + agg.OutputTokens + agg.CacheReadTokens + agg.CacheWriteTokens
	sb.WriteString(fmt.Sprintf("\n  Total: %s", humanCount(total)))
	return sb.String(), nil
}

func humanCount(n int64) string {
	switch {
	case n >= 1_000_000_000:
		return fmt.Sprintf("%.1fB", float64(n)/1_000_000_000)
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	case n >= 1_000:
		return fmt.Sprintf("%.1fK", float64(n)/1_000)
	default:
		return fmt.Sprintf("%d", n)
	}
}

func (b *DaemonBridge) liveAgent() *agent.Agent {
	b.mu.Lock()
	a := b.agent
	b.mu.Unlock()
	return a
}

func (b *DaemonBridge) SessionCostSummary() (string, error) {
	return BuildCrossSessionCostSummary()
}

func (b *DaemonBridge) SessionUsageSummary() (string, error) {
	return buildDiskUsageSummary()
}

func (b *DaemonBridge) CurrentMode() string {
	a := b.liveAgent()
	if a == nil {
		return "(no agent attached)"
	}
	return a.PermissionPolicy().Mode().String()
}

func (b *DaemonBridge) SwitchMode(name string) error {
	a := b.liveAgent()
	if a == nil {
		return fmt.Errorf("no agent attached")
	}
	policy := a.PermissionPolicy()
	cp, ok := policy.(*permission.ConfigPolicy)
	if !ok {
		return fmt.Errorf("mode switching not supported by the active policy (%T)", policy)
	}
	cp.SetMode(permission.ParsePermissionMode(name))
	return nil
}

func (b *DaemonBridge) ToolList() (string, error) {
	a := b.liveAgent()
	if a == nil {
		return "No agent attached.", nil
	}
	reg := a.ToolRegistry()
	if reg == nil {
		return "Tool registry not available.", nil
	}
	tools := reg.List()
	if len(tools) == 0 {
		return "No tools registered.", nil
	}
	sort.Slice(tools, func(i, j int) bool { return tools[i].Name() < tools[j].Name() })
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Available tools (%d):\n\n", len(tools)))
	for _, t := range tools {
		desc := t.Description()
		if len(desc) > 120 {
			desc = desc[:117] + "..."
		}
		sb.WriteString(fmt.Sprintf("  %-20s %s\n", t.Name(), desc))
	}
	return sb.String(), nil
}

func (b *DaemonBridge) ModifiedFiles() (string, error) {
	a := b.liveAgent()
	if a == nil {
		return "No agent attached.", nil
	}
	cpMgr := a.CheckpointManager()
	if cpMgr == nil {
		return "Checkpoints not available.", nil
	}
	files := cpMgr.ModifiedFiles()
	if len(files) == 0 {
		return "No files modified yet this session.", nil
	}
	totalEdits := 0
	for _, f := range files {
		totalEdits += f.Edits
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Modified files (%d, %d edits):\n\n", len(files), totalEdits))
	for _, f := range files {
		flag := ""
		if f.IsNew {
			flag = " (new)"
		}
		sb.WriteString(fmt.Sprintf("  %s - %d edits%s\n", f.Path, f.Edits, flag))
	}
	return sb.String(), nil
}

func (b *DaemonBridge) GitDiff(args []string) (string, error) {
	b.mu.Lock()
	dir := b.workingDir
	b.mu.Unlock()
	gitArgs := append([]string{"diff"}, args...)
	cmd := exec.Command("git", gitArgs...)
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.CombinedOutput()
	if err != nil && len(out) == 0 {
		return "", fmt.Errorf("git diff: %v", err)
	}
	return strings.TrimRight(string(out), "\n"), nil
}

// Compile-time interface conformance for the path-A deps implementation.
var _ SlashDeps = (*DaemonBridge)(nil)

// keep provider import for token usage parity with the TUI path (usage
// rendering on the TUI side references provider.TokenUsage).
var _ = provider.TokenUsage{}
