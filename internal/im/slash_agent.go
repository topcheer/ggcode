package im

import (
	"context"
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/agent"
	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/permission"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/usage"
)

// BuildCrossSessionCostSummary is retired (#2312 ruling B): the .cost.json
// store it loaded has no live writer - the IM fallback rendered empty or
// stale legacy data. The first-class usage/balance surface (#2150, in
// flight) will serve cross-session totals from live data.
func BuildCrossSessionCostSummary() (string, error) {
	return "Cross-session cost totals are being rebuilt on the live usage pipeline (#2150). Session /cost is available.", nil
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

	cfg := config.LoadInstanceConfig(b.workingDir)
	if cfg == nil || cfg.Vendor == "" {
		return "usage: no active vendor", nil
	}
	baseURL, apiKey := "", ""
	if v, ok := cfg.Vendors[cfg.Vendor]; ok {
		if ep, ok := v.Endpoints[cfg.Endpoint]; ok {
			baseURL, apiKey = ep.BaseURL, ep.APIKey
		}
	}
	if apiKey == "" {
		return fmt.Sprintf("usage: %s (no API key resolved for the active endpoint)", cfg.Vendor), nil
	}

	probeID := imUsageService().Resolve(baseURL)
	if probeID == "" {
		return fmt.Sprintf("usage: %s (no usage probe for this endpoint)", cfg.Vendor), nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	info, err := imUsageService().Get(ctx, probeID, baseURL, apiKey)
	return usage.RenderText(probeID, info, err), nil
}

// imUsageServiceOnce guards the process-wide probe service for the IM
// /usage path (usage.DefaultService builds a fresh Service per call; a
// singleton preserves its cache across commands).
var imUsageServiceOnce sync.Once
var imUsageServiceInst *usage.Service

func imUsageService() *usage.Service {
	imUsageServiceOnce.Do(func() {
		imUsageServiceInst = usage.DefaultService()
	})
	return imUsageServiceInst
}

func (b *DaemonBridge) CurrentMode() string {
	a := b.liveAgent()
	if a == nil {
		return "(no agent attached)"
	}
	return a.PermissionPolicy().Mode().String()
}

func (b *DaemonBridge) SwitchMode(name string) error {

	if !permission.IsValidPermissionMode(name) {
		return fmt.Errorf("unknown permission mode %q (valid: supervised, bypass; legacy aliases auto/autopilot normalize)", name)
	}
	newMode := permission.ParsePermissionMode(name)
	if (newMode == permission.BypassMode || newMode == permission.AutopilotMode) &&
		!b.remoteDangerousAllowed() {
		return fmt.Errorf("switching to %s over IM requires the im.remote_dangerous_commands opt-in (config yaml) - refusing zero-confirmation privilege escalation (#2185/#2205)", newMode)
	}
	a := b.liveAgent()
	if a == nil {
		return fmt.Errorf("no agent attached")
	}
	policy := a.PermissionPolicy()
	cp, ok := policy.(*permission.ConfigPolicy)
	if !ok {
		return fmt.Errorf("mode switching not supported by the active policy (%T)", policy)
	}
	cp.SetMode(newMode)
	return nil
}

// remoteDangerousAllowed reports whether the #2185 im.remote_dangerous_commands
// opt-in is set for this bridge's workspace (fail closed: nil config denies).
func (b *DaemonBridge) remoteDangerousAllowed() bool {
	if b.remoteDangerousOverride != nil {
		return *b.remoteDangerousOverride
	}
	cfg := config.LoadInstanceConfig(b.workingDir)
	return cfg != nil && cfg.IM.RemoteDangerousCommands
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

			r := []rune(desc)
			if len(r) > 117 {
				desc = string(r[:117]) + "..."
			}
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

// sanitizeGitDiffArgs filters remote /diff arguments down to a safe
// subset (#1752 case 1): IM text arrives via strings.Fields and was passed
// through verbatim - `--output=~/.bashrc` made git WRITE the diff to any
// file the process can touch, --ext-diff/-O widened the surface. No shell
// is involved, but git's own options are attacker-reachable. Allowlist:
// a few display flags, then anything after a `--` separator (paths).
func SanitizeGitDiffArgs(args []string) []string {
	allowed := map[string]bool{
		"--cached": true, "--stat": true, "--numstat": true,
		"--shortstat": true, "--name-only": true, "--name-status": true,
	}
	var out []string
	pastSeparator := false
	for _, a := range args {
		if pastSeparator {
			out = append(out, a)
			continue
		}
		if a == "--" {
			pastSeparator = true
			out = append(out, a)
			continue
		}
		if allowed[a] {
			out = append(out, a)
			continue
		}

		if !strings.HasPrefix(a, "-") {
			out = append(out, a)
		}
	}
	return out
}

func (b *DaemonBridge) GitDiff(args []string) (string, error) {
	b.mu.Lock()
	dir := b.workingDir
	b.mu.Unlock()
	gitArgs := append([]string{"diff"}, SanitizeGitDiffArgs(args)...)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", gitArgs...)
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.CombinedOutput()
	if err != nil {

		first := strings.SplitN(strings.TrimSpace(string(out)), "\n", 2)[0]
		if first != "" {
			return "", fmt.Errorf("git diff: %v (%s)", err, first)
		}
		return "", fmt.Errorf("git diff: %v", err)
	}
	trimmed := strings.TrimRight(string(out), "\n")
	if trimmed == "" {

		return "No changes.", nil
	}
	return trimmed, nil
}

// Compile-time interface conformance for the path-A deps implementation.
var _ SlashDeps = (*DaemonBridge)(nil)

// keep provider import for token usage parity with the TUI path (usage
// rendering on the TUI side references provider.TokenUsage).
var _ = provider.TokenUsage{}
