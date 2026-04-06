package main

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/topcheer/ggcode/internal/agent"
	"github.com/topcheer/ggcode/internal/commands"
	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/image"
	"github.com/topcheer/ggcode/internal/mcp"
	"github.com/topcheer/ggcode/internal/memory"
	"github.com/topcheer/ggcode/internal/permission"
	"github.com/topcheer/ggcode/internal/plugin"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/subagent"
	"github.com/topcheer/ggcode/internal/tool"
)

// RunPipe executes the agent in non-interactive pipe mode.
// Returns the exit code (0=success, 1=failure).
func RunPipe(cfg *config.Config, prompt string, allowedTools []string, outputPath string) int {
	resolved, err := cfg.ResolveActiveEndpoint()
	if err != nil {
		fmt.Fprintf(os.Stderr, "resolving endpoint: %v\n", err)
		return 1
	}
	if resolved.APIKey == "" {
		fmt.Fprintf(os.Stderr, "missing api key for vendor %s endpoint %s\n", resolved.VendorID, resolved.EndpointID)
		return 1
	}

	// Setup provider
	prov, err := provider.NewProvider(resolved)
	if err != nil {
		fmt.Fprintf(os.Stderr, "creating provider: %v\n", err)
		return 1
	}

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

	// Setup tools (after policy so sandbox checks can be wired)
	workingDir, _ := os.Getwd()
	registry := tool.NewRegistry()
	if err := tool.RegisterBuiltinTools(registry, policy, workingDir); err != nil {
		fmt.Fprintf(os.Stderr, "registering tools: %v\n", err)
		return 1
	}
	mergedMCPServers, _ := mcp.MergeStartupServers(workingDir, cfg.MCPServers)
	mcpMgr := plugin.NewMCPManager(mergedMCPServers, registry)
	_ = registry.Register(tool.ListMCPCapabilitiesTool{Runtime: mcpMgr})
	_ = registry.Register(tool.GetMCPPromptTool{Runtime: mcpMgr})
	_ = registry.Register(tool.ReadMCPResourceTool{Runtime: mcpMgr})

	// Load plugins
	pluginMgr := plugin.NewManager()
	pluginMgr.LoadAll(cfg.Plugins)
	for _, warning := range mcpMgr.ConnectAll(context.Background()) {
		fmt.Fprintln(os.Stderr, warning)
	}
	pluginMgr.RegisterTools(registry)

	// Load project memory documents.
	projectMem, _, _ := memory.LoadProjectMemory(workingDir)

	// Load auto memory
	autoMem := memory.NewAutoMemory()
	autoContent, _, _ := autoMem.LoadAll()
	_ = registry.Register(tool.NewSaveMemoryTool(autoMem))
	commandMgr := commands.NewManager(workingDir)
	skillAgentFactory := func(prov provider.Provider, tools interface{}, systemPrompt string, maxTurns int) subagent.AgentRunner {
		return agent.NewAgent(prov, tools.(*tool.Registry), systemPrompt, maxTurns)
	}
	_ = registry.Register(tool.SkillTool{
		Skills:       commandMgr,
		Runtime:      mcpMgr,
		Provider:     prov,
		Tools:        registry,
		AgentFactory: skillAgentFactory,
	})

	// Build enhanced system prompt
	gitStatus := detectGitStatus(workingDir)
	userSlashCmds := commandMgr.UserSlashCommands()
	customCmdNames := make([]string, 0, len(userSlashCmds))
	for name := range userSlashCmds {
		customCmdNames = append(customCmdNames, name)
	}
	systemPrompt := config.BuildSystemPrompt(cfg.SystemPrompt, workingDir, registryToolNames(registry), gitStatus, customCmdNames)
	if skillsPrompt := buildSkillsSystemPrompt(append(commandMgr.List(), buildMCPSkillCommands(mcpMgr.SnapshotMCP())...)); skillsPrompt != "" {
		systemPrompt += "\n\n## Skills\n" + skillsPrompt
	}
	if projectMem != "" {
		systemPrompt += "\n\n## Project Memory\n" + projectMem
	}
	if autoContent != "" {
		systemPrompt += "\n\n## Auto Memory\n" + autoContent
	}

	// Setup agent
	maxIter := cfg.MaxIterations
	ag := agent.NewAgent(prov, registry, systemPrompt, maxIter)
	if resolved.ContextWindow > 0 {
		ag.ContextManager().SetMaxTokens(resolved.ContextWindow)
	}
	ag.SetPermissionPolicy(policy)

	// Read stdin if available
	stdinData, err := readStdin()
	if err != nil {
		fmt.Fprintf(os.Stderr, "reading stdin: %v\n", err)
		return 1
	}

	// Compose the full prompt (may include image from stdin)
	fullPrompt, imageBlocks := buildPipePrompt(prompt, stdinData)

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
	var agentErr error
	if imageBlocks != nil {
		agentErr = ag.RunStreamWithContent(ctx, imageBlocks, func(event provider.StreamEvent) {
			switch event.Type {
			case provider.StreamEventText:
				fmt.Fprint(w, event.Text)
			case provider.StreamEventError:
				fmt.Fprintf(os.Stderr, "error: %v\n", event.Error)
				hasError = true
			}
		})
	} else {
		agentErr = ag.RunStream(ctx, fullPrompt, func(event provider.StreamEvent) {
			switch event.Type {
			case provider.StreamEventText:
				fmt.Fprint(w, event.Text)
			case provider.StreamEventError:
				fmt.Fprintf(os.Stderr, "error: %v\n", event.Error)
				hasError = true
			}
		})
	}

	if agentErr != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", agentErr)
		return 1
	}
	if hasError {
		return 1
	}
	return 0
}

// readStdin reads all data from stdin if it's a pipe, otherwise returns "".
func readStdin() ([]byte, error) {
	stat, _ := os.Stdin.Stat()
	if (stat.Mode() & os.ModeCharDevice) != 0 {
		return nil, nil
	}
	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		return nil, err
	}
	return data, nil
}

// registryToolNames extracts tool names from the registry.
func registryToolNames(r *tool.Registry) []string {
	tools := r.List()
	names := make([]string, len(tools))
	for i, t := range tools {
		names[i] = t.Name()
	}
	return names
}

// buildPipePrompt builds the prompt with optional image from stdin.
func buildPipePrompt(prompt string, stdinData []byte) (string, []provider.ContentBlock) {
	if stdinData == nil {
		return prompt, nil
	}

	// Check if stdin is an image
	mime := image.DetectMIME(stdinData)
	if mime != "" {
		img, err := image.Decode(stdinData)
		if err == nil {
			placeholder := image.Placeholder("stdin", img)
			fmt.Fprintf(os.Stderr, "Detected image from stdin: %s\n", placeholder)
			blocks := []provider.ContentBlock{
				provider.TextBlock(prompt),
				provider.ImageBlock(img.MIME, image.EncodeBase64(img)),
			}
			return "", blocks
		}
	}

	// Plain text
	return string(stdinData) + "\n\n" + prompt, nil
}
