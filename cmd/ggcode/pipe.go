package main

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/topcheer/ggcode/internal/agent"
	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/cost"
	"github.com/topcheer/ggcode/internal/permission"
	"github.com/topcheer/ggcode/internal/plugin"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/tool"
)

// RunPipe executes the agent in non-interactive pipe mode.
// Returns the exit code (0=success, 1=failure).
func RunPipe(cfg *config.Config, prompt string, allowedTools []string, outputPath string) int {
	// Setup provider
	prov, err := provider.NewProvider(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "creating provider: %v\n", err)
		return 1
	}

	// Setup tools
	registry := tool.NewRegistry()
	if err := tool.RegisterBuiltinTools(registry); err != nil {
		fmt.Fprintf(os.Stderr, "registering tools: %v\n", err)
		return 1
	}

	// Load plugins
	pluginMgr := plugin.NewManager()
	pluginMgr.LoadAll(cfg.Plugins)
	for _, mcpCfg := range cfg.MCPServers {
		p := plugin.NewMCPPlugin(mcpCfg)
		if err := p.RegisterTools(context.Background(), registry); err != nil {
			fmt.Fprintf(os.Stderr, "warning: MCP server %s failed: %v\n", mcpCfg.Name, err)
		}
	}
	pluginMgr.RegisterTools(registry)

	// Setup permission: auto mode for pipe (no interactive prompts)
	allowedDirs := cfg.ExpandAllowedDirs(".")
	rules := make(map[string]permission.Decision)
	for name, perm := range cfg.ToolPerms {
		switch config.ToolPermission(perm) {
		case "allow":
			rules[name] = permission.Allow
		case "deny":
			rules[name] = permission.Deny
		}
	}
	policy := permission.NewConfigPolicyWithMode(rules, allowedDirs, permission.AutoMode)

	// Apply allowedTools filter
	if len(allowedTools) > 0 {
		for _, t := range allowedTools {
			policy.SetOverride(t, permission.Allow)
		}
	}

	// Setup agent
	maxIter := cfg.MaxIterations
	if maxIter == 0 {
		maxIter = 50
	}
	ag := agent.NewAgent(prov, registry, cfg.SystemPrompt, maxIter)
	ag.SetPermissionPolicy(policy)

	// Setup cost tracking
	pricing := cost.DefaultPricingTable()
	costMgr := cost.NewManager(pricing, "")
	ag.SetUsageHandler(func(usage provider.TokenUsage) {
		tracker := costMgr.GetOrCreateTracker("pipe", cfg.Provider, cfg.Model)
		tracker.Record(cost.TokenUsage{
			InputTokens:  usage.InputTokens,
			OutputTokens: usage.OutputTokens,
		})
	})

	// Read stdin if available
	stdinData, err := readStdin()
	if err != nil {
		fmt.Fprintf(os.Stderr, "reading stdin: %v\n", err)
		return 1
	}

	// Compose the full prompt
	fullPrompt := prompt
	if stdinData != "" {
		fullPrompt = stdinData + "\n\n" + prompt
	}

	// Output destination
	var w io.Writer = os.Stdout
	if outputPath != "" {
		f, err := os.Create(outputPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "creating output file: %v\n", err)
			return 1
		}
		defer f.Close()
		w = f
	}

	// Run agent non-interactively
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var hasError bool
	err = ag.RunStream(ctx, fullPrompt, func(event provider.StreamEvent) {
		switch event.Type {
		case provider.StreamEventText:
			fmt.Fprint(w, event.Text)
		case provider.StreamEventError:
			fmt.Fprintf(os.Stderr, "error: %v\n", event.Error)
			hasError = true
		}
	})

	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	if hasError {
		return 1
	}
	return 0
}

// readStdin reads all data from stdin if it's a pipe, otherwise returns "".
func readStdin() (string, error) {
	stat, _ := os.Stdin.Stat()
	if (stat.Mode() & os.ModeCharDevice) != 0 {
		return "", nil
	}
	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		return "", err
	}
	return string(data), nil
}
