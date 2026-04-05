package main

import (
	"context"
	"fmt"
	"os"
	"sync"

	"github.com/spf13/cobra"

	"github.com/topcheer/ggcode/internal/agent"
	"github.com/topcheer/ggcode/internal/checkpoint"
	"github.com/topcheer/ggcode/internal/commands"
	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/cost"
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/memory"
	"github.com/topcheer/ggcode/internal/permission"
	"github.com/topcheer/ggcode/internal/plugin"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/session"
	"github.com/topcheer/ggcode/internal/subagent"
	"github.com/topcheer/ggcode/internal/tool"
	"github.com/topcheer/ggcode/internal/tui"
)

func NewRootCmd() *cobra.Command {
	var cfgFile string
	var resumeID string
	var pipePrompt string
	var allowedTools []string
	var bypassFlag bool
	var outputPath string

	cmd := &cobra.Command{
		Use:          "ggcode",
		Short:        "AI coding assistant",
		Long:         "ggcode is a terminal-based AI coding agent powered by LLMs.",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if cfgFile == "" {
				cfgFile = config.ConfigPath()
			}

			debug.Init()

			cfg, err := config.Load(cfgFile)
			if err != nil {
				return fmt.Errorf("loading config: %w", err)
			}

			// Pipe mode: non-interactive single execution
			if pipePrompt != "" {
				code := RunPipe(cfg, pipePrompt, allowedTools, outputPath)
				if code != 0 {
					os.Exit(code)
				}
				return nil
			}

			return run(cfg, resumeID, bypassFlag)
		},
	}

	cmd.Flags().StringVar(&cfgFile, "config", "", "config file path")
	cmd.Flags().StringVar(&resumeID, "resume", "", "resume a previous session by ID")
	cmd.Flags().StringVarP(&pipePrompt, "prompt", "p", "", "pipe mode: non-interactive execution with a prompt")
	cmd.Flags().StringArrayVar(&allowedTools, "allowedTools", nil, "tools to allow in pipe mode (can be repeated)")
	cmd.Flags().BoolVar(&bypassFlag, "bypass", false, "start in bypass permission mode (auto-approve safe ops, warn on dangerous)")
	cmd.Flags().StringVar(&outputPath, "output", "", "output file path (default: stdout)")

	// Shell completion commands
	completionCmd := &cobra.Command{
		Use:       "completion [shell]",
		Short:     "Generate shell completion script",
		Long:      `Generate shell completion script for bash, zsh, fish, or powershell.`,
		Args:      cobra.ExactArgs(1),
		ValidArgs: []string{"bash", "zsh", "fish", "powershell"},
		RunE: func(cmd *cobra.Command, args []string) error {
			switch args[0] {
			case "bash":
				return cmd.Root().GenBashCompletion(cmd.OutOrStdout())
			case "zsh":
				return cmd.Root().GenZshCompletion(cmd.OutOrStdout())
			case "fish":
				return cmd.Root().GenFishCompletion(cmd.OutOrStdout(), true)
			case "powershell":
				return cmd.Root().GenPowerShellCompletion(cmd.OutOrStdout())
			default:
				return fmt.Errorf("unsupported shell: %s", args[0])
			}
		},
		DisableFlagsInUseLine: true,
	}
	cmd.AddCommand(completionCmd)

	return cmd
}

func run(cfg *config.Config, resumeID string, bypass bool) error {
	// Setup provider
	pc := cfg.GetProviderConfig()
	if pc.APIKey == "" {
		return fmt.Errorf("no API key for provider %q. Set the api_key in config", cfg.Provider)
	}

	prov, err := provider.NewProvider(cfg)
	if err != nil {
		return err
	}

	// Setup permission policy
	allowedDirs := cfg.ExpandAllowedDirs(".")

	// Convert config tool permissions to policy rules
	rules := make(map[string]permission.Decision)
	for tool, perm := range cfg.ToolPerms {
		switch config.ToolPermission(perm) {
		case "allow":
			rules[tool] = permission.Allow
		case "deny":
			rules[tool] = permission.Deny
		default:
			rules[tool] = permission.Ask
		}
	}
	mode := permission.ParsePermissionMode(cfg.DefaultMode)
	if bypass {
		mode = permission.BypassMode
	}
	policy := permission.NewConfigPolicyWithMode(rules, allowedDirs, mode)

	// Setup tools (after policy so sandbox checks can be wired)
	workingDir, _ := os.Getwd()
	registry := tool.NewRegistry()
	if err := tool.RegisterBuiltinTools(registry, policy, workingDir); err != nil {
		return err
	}

	// Load plugins
	pluginMgr := plugin.NewManager()
	pluginMgr.LoadAll(cfg.Plugins)
	if err := pluginMgr.RegisterTools(registry); err != nil {
		return err
	}

	// Setup cost tracker
	pricing := cost.DefaultPricingTable()
	// Allow config to override pricing (future: load from config file)
	costMgr := cost.NewManager(pricing, "")

	autoMem := memory.NewAutoMemory()
	_ = registry.Register(tool.NewSaveMemoryTool(autoMem))

	projectMem, projectFiles, autoContent, autoFiles, customCmds, mcpPlugins, mcpWarnings := loadStartupAssets(workingDir, autoMem, cfg, registry)
	for _, warning := range mcpWarnings {
		fmt.Fprintln(os.Stderr, warning)
	}

	// Detect git status
	gitStatus := detectGitStatus(workingDir)

	// Collect tool names
	tools := registry.List()
	toolNames := make([]string, len(tools))
	for i, t := range tools {
		toolNames[i] = t.Name()
	}

	// Collect custom command names
	customCmdNames := make([]string, len(customCmds))
	i := 0
	for name := range customCmds {
		customCmdNames[i] = name
		i++
	}

	// Build enhanced system prompt with runtime context
	systemPrompt := config.BuildSystemPrompt(cfg.SystemPrompt, workingDir, toolNames, gitStatus, customCmdNames)
	if projectMem != "" {
		systemPrompt += "\n\n## Project Memory (GGCODE.md)\n" + projectMem
	}
	if autoContent != "" {
		systemPrompt += "\n\n## Auto Memory\n" + autoContent
	}

	// Setup sub-agent manager
	subMgr := subagent.NewManager(cfg.SubAgents)

	// Setup agent
	maxIter := cfg.MaxIterations
	if maxIter == 0 {
		maxIter = 50
	}
	ag := agent.NewAgent(prov, registry, systemPrompt, maxIter)
	ag.SetPermissionPolicy(policy)
	ag.SetHookConfig(cfg.Hooks)
	ag.SetWorkingDir(workingDir)
	ag.SetCheckpointManager(checkpoint.NewManager(50))

	// Setup session store
	store, err := session.NewDefaultStore()
	if err != nil {
		return fmt.Errorf("creating session store: %w", err)
	}

	// Build MCP info for TUI
	var mcpInfos []tui.MCPInfo
	for _, mp := range mcpPlugins {
		adapter := mp.Adapter()
		info := tui.MCPInfo{
			Name:      mp.Name(),
			Connected: mp.IsConnected(),
		}
		if adapter != nil {
			info.ToolNames = adapter.ToolNames()
		}
		mcpInfos = append(mcpInfos, info)
	}

	// Start TUI REPL
	repl := tui.NewREPL(ag, policy)
	repl.SetCostManager(costMgr, cfg.Provider, cfg.Model)
	repl.SetConfig(cfg)
	repl.SetSessionStore(store)
	repl.SetMCPServers(mcpInfos)
	repl.SetPluginManager(pluginMgr)
	repl.SetCustomCommands(customCmds)
	repl.SetAutoMemory(autoMem)
	repl.SetProjectMemoryFiles(projectFiles)
	repl.SetAutoMemoryFiles(autoFiles)
	repl.SetSubAgentManager(subMgr, prov, registry)
	if resumeID != "" {
		repl.SetResumeID(resumeID)
	}
	return repl.Run()
}

func init() {
	debug.Init()
}

// detectGitStatus returns a short git status string or "".
func detectGitStatus(workingDir string) string {
	if _, err := os.Stat(workingDir + "/.git"); err != nil {
		return "not a git repository"
	}
	return "in a git repository"
}

func loadStartupAssets(
	workingDir string,
	autoMem *memory.AutoMemory,
	cfg *config.Config,
	registry *tool.Registry,
) (string, []string, string, []string, map[string]*commands.Command, []*plugin.MCPPlugin, []string) {
	var (
		projectMem   string
		projectFiles []string
		autoContent  string
		autoFiles    []string
		customCmds   map[string]*commands.Command
		mcpPlugins   []*plugin.MCPPlugin
		mcpWarnings  []string
	)

	var wg sync.WaitGroup
	wg.Add(4)

	go func() {
		defer wg.Done()
		projectMem, projectFiles, _ = memory.LoadProjectMemory(workingDir)
	}()

	go func() {
		defer wg.Done()
		autoContent, autoFiles, _ = autoMem.LoadAll()
	}()

	go func() {
		defer wg.Done()
		customCmds = commands.NewLoader(workingDir).Load()
	}()

	go func() {
		defer wg.Done()
		mcpPlugins, mcpWarnings = connectMCPServers(context.Background(), cfg.MCPServers, registry)
	}()

	wg.Wait()

	if customCmds == nil {
		customCmds = map[string]*commands.Command{}
	}

	return projectMem, projectFiles, autoContent, autoFiles, customCmds, mcpPlugins, mcpWarnings
}

func connectMCPServers(
	ctx context.Context,
	servers []config.MCPServerConfig,
	registry *tool.Registry,
) ([]*plugin.MCPPlugin, []string) {
	if len(servers) == 0 {
		return nil, nil
	}

	plugins := make([]*plugin.MCPPlugin, len(servers))
	warnings := make([]string, len(servers))

	var wg sync.WaitGroup
	for i, mcpCfg := range servers {
		wg.Add(1)
		go func(i int, mcpCfg config.MCPServerConfig) {
			defer wg.Done()
			p := plugin.NewMCPPlugin(mcpCfg)
			if err := p.RegisterTools(ctx, registry); err != nil {
				warnings[i] = fmt.Sprintf("warning: MCP server %s failed: %v", mcpCfg.Name, err)
				return
			}
			plugins[i] = p
		}(i, mcpCfg)
	}
	wg.Wait()

	connected := make([]*plugin.MCPPlugin, 0, len(plugins))
	nonEmptyWarnings := make([]string, 0, len(warnings))
	for i := range plugins {
		if plugins[i] != nil {
			connected = append(connected, plugins[i])
		}
		if warnings[i] != "" {
			nonEmptyWarnings = append(nonEmptyWarnings, warnings[i])
		}
	}

	return connected, nonEmptyWarnings
}
