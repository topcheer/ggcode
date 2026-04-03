package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/spf13/cobra"

	"github.com/topcheer/ggcode/internal/agent"
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/checkpoint"
	"github.com/topcheer/ggcode/internal/commands"
	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/cost"
	"github.com/topcheer/ggcode/internal/memory"
	"github.com/topcheer/ggcode/internal/permission"
	"github.com/topcheer/ggcode/internal/plugin"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/session"
	"github.com/topcheer/ggcode/internal/tool"
	"github.com/topcheer/ggcode/internal/tui"
	"github.com/topcheer/ggcode/internal/subagent"
)

func NewRootCmd() *cobra.Command {
	var cfgFile string
	var resumeID string
	var pipePrompt string
	var allowedTools []string
	var outputPath string

	cmd := &cobra.Command{
		Use:   "ggcode",
		Short: "AI coding assistant",
		Long:  "ggcode is a terminal-based AI coding agent powered by LLMs.",
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

			return run(cfg, resumeID)
		},
	}

	cmd.Flags().StringVar(&cfgFile, "config", "", "config file path")
	cmd.Flags().StringVar(&resumeID, "resume", "", "resume a previous session by ID")
	cmd.Flags().StringVarP(&pipePrompt, "prompt", "p", "", "pipe mode: non-interactive execution with a prompt")
	cmd.Flags().StringArrayVar(&allowedTools, "allowedTools", nil, "tools to allow in pipe mode (can be repeated)")
	cmd.Flags().StringVar(&outputPath, "output", "", "output file path (default: stdout)")

	// Shell completion commands
	completionCmd := &cobra.Command{
		Use:   "completion [shell]",
		Short: "Generate shell completion script",
		Long:  `Generate shell completion script for bash, zsh, fish, or powershell.`,
		Args:  cobra.ExactArgs(1),
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

func run(cfg *config.Config, resumeID string) error {
	// Setup provider
	pc := cfg.GetProviderConfig()
	if pc.APIKey == "" {
		return fmt.Errorf("no API key for provider %q. Set the api_key in config", cfg.Provider)
	}

	prov, err := provider.NewProvider(cfg)
	if err != nil {
		return err
	}

	// Setup tools
	registry := tool.NewRegistry()
	if err := tool.RegisterBuiltinTools(registry); err != nil {
		return err
	}

	// Load plugins
	pluginMgr := plugin.NewManager()
	pluginMgr.LoadAll(cfg.Plugins)

	// Connect MCP servers and register their tools
	var mcpPlugins []*plugin.MCPPlugin
	for _, mcpCfg := range cfg.MCPServers {
		p := plugin.NewMCPPlugin(mcpCfg)
		if err := p.RegisterTools(context.Background(), registry); err != nil {
			fmt.Fprintf(os.Stderr, "warning: MCP server %s failed: %v\n", mcpCfg.Name, err)
			continue
		}
		mcpPlugins = append(mcpPlugins, p)
	}
	pluginMgr.RegisterTools(registry)

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
policy := permission.NewConfigPolicyWithMode(rules, allowedDirs, mode)

	// Setup cost tracker
	pricing := cost.DefaultPricingTable()
	// Allow config to override pricing (future: load from config file)
	costMgr := cost.NewManager(pricing, "")

	// Load project memory (GGCODE.md)
	workingDir, _ := os.Getwd()
	projectMem, projectFiles, _ := memory.LoadProjectMemory(workingDir)

	// Load auto memory
	autoMem := memory.NewAutoMemory()
	autoContent, autoFiles, _ := autoMem.LoadAll()
	_ = registry.Register(tool.NewSaveMemoryTool(autoMem))

	// Build enhanced system prompt
	systemPrompt := cfg.SystemPrompt
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

	// Load custom slash commands
	cmdLoader := commands.NewLoader(workingDir)
	customCmds := cmdLoader.Load()

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
	log.SetFlags(0)
	log.SetOutput(log.Writer())
}
