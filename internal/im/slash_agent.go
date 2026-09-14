package im

import (
	"fmt"
	"os/exec"
	"sort"
	"strings"

	"github.com/topcheer/ggcode/internal/agent"
	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/permission"
	"github.com/topcheer/ggcode/internal/provider"
)

// Path-A (daemon bridge) implementation of SlashDeps. The agent and disk
// stores are the data sources; the TUI-attached path supplies its own live
// implementation from the model state.

// BuildCrossSessionCostSummary is retired (#2312 ruling B): the .cost.json
// store it loaded has no live writer - the IM fallback rendered empty or
// stale legacy data. The first-class usage/balance surface (#2150, in
// flight) will serve cross-session totals from live data.
func BuildCrossSessionCostSummary() (string, error) {
	return "Cross-session cost totals are being rebuilt on the live usage pipeline (#2150). Session /cost is available.", nil
}

// buildDiskUsageSummary is retired with the same ruling (#2312 B): it
// aggregated the same writer-less .cost.json store. See
// BuildCrossSessionCostSummary above.
func buildDiskUsageSummary() (string, error) {
	return "All-time token totals are being rebuilt on the live usage pipeline (#2150).", nil
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
	// #2205: mirror the TUI-side gate (#2185/PR #2203) on the daemon
	// bridge - the second consumer of IM-remote mode switching. Escalation
	// to bypass/autopilot requires the im.remote_dangerous_commands opt-in;
	// downgrade/level switches and plan/auto stay ungated. Fail closed on
	// missing config. Unknown mode names error instead of silently
	// falling back to supervised.
	if !permission.IsValidPermissionMode(name) {
		return fmt.Errorf("unknown permission mode %q (valid: supervised, plan, auto, bypass, autopilot)", name)
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
		// Positional tokens (refs, paths - no leading dash) pass; every
		// other `-`-prefixed token is dropped silently, so the resulting
		// command is always a plain read-only diff.
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
	cmd := exec.Command("git", gitArgs...)
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		// #1560-B: CombinedOutput merges stderr - "fatal: bad revision"
		// plus the full usage text used to be handed back as the DIFF
		// BODY when the exit was non-zero with non-empty output (the TUI
		// path fixed this in #909; the IM path was the un-synced parity
		// copy). Any error now surfaces as an error, carrying the first
		// line of output for diagnosis.
		first := strings.SplitN(strings.TrimSpace(string(out)), "\n", 2)[0]
		if first != "" {
			return "", fmt.Errorf("git diff: %v (%s)", err, first)
		}
		return "", fmt.Errorf("git diff: %v", err)
	}
	trimmed := strings.TrimRight(string(out), "\n")
	if trimmed == "" {
		// Clean tree: git diff exits 0 with empty output. IM users would
		// otherwise receive an empty message. Mirrors the TUI path's fix.
		return "No changes.", nil
	}
	return trimmed, nil
}

// Compile-time interface conformance for the path-A deps implementation.
var _ SlashDeps = (*DaemonBridge)(nil)

// keep provider import for token usage parity with the TUI path (usage
// rendering on the TUI side references provider.TokenUsage).
var _ = provider.TokenUsage{}
