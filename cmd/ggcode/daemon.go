package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/topcheer/ggcode/internal/agent"
	"github.com/topcheer/ggcode/internal/commands"
	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/im"
	"github.com/topcheer/ggcode/internal/mcp"
	"github.com/topcheer/ggcode/internal/memory"
	"github.com/topcheer/ggcode/internal/permission"
	"github.com/topcheer/ggcode/internal/plugin"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/session"
	"github.com/topcheer/ggcode/internal/subagent"
	"github.com/topcheer/ggcode/internal/tool"
)

func newDaemonCmd(cfgFile *string) *cobra.Command {
	var bypassFlag bool

	cmd := &cobra.Command{
		Use:   "daemon",
		Short: "Run ggcode in daemon mode, controlled via IM",
		Long:  "Run ggcode as a headless daemon. Messages from paired IM channels are forwarded to the agent, and responses are sent back via IM. Requires at least one IM channel bound to the current workspace.",
		RunE: func(cmd *cobra.Command, args []string) error {
			resolvedCfg := *cfgFile
			if resolvedCfg == "" {
				r, err := resolveConfigFilePath()
				if err != nil {
					return fmt.Errorf("resolving config path: %w", err)
				}
				resolvedCfg = r
			}

			debug.Init()
			cfg, err := config.Load(resolvedCfg)
			if err != nil {
				return fmt.Errorf("loading config: %w", err)
			}
			if _, _, err := mcp.PersistUserClaudeServers(cfg); err != nil {
				return fmt.Errorf("persisting Claude MCP servers: %w", err)
			}
			return runDaemon(cfg, resolvedCfg, bypassFlag)
		},
	}

	cmd.Flags().BoolVar(&bypassFlag, "bypass", false, "start in bypass permission mode (auto-approve safe ops)")
	return cmd
}

func runDaemon(cfg *config.Config, cfgFile string, bypass bool) error {
	// --- Steps 1-8: same as run() in root.go ---

	resolved, err := cfg.ResolveActiveEndpoint()
	if err != nil {
		return err
	}
	if resolved.APIKey == "" {
		return fmt.Errorf("no API key for vendor %q endpoint %q", resolved.VendorID, resolved.EndpointID)
	}

	// Apply impersonation
	if imp := cfg.Impersonation; imp.Preset != "" {
		var preset *provider.ImpersonationPreset
		if imp.Preset != "none" {
			preset = provider.FindPresetByID(imp.Preset)
		}
		customHeaders := make(map[string]string, len(imp.CustomHeaders))
		for k, v := range imp.CustomHeaders {
			customHeaders[k] = v
		}
		provider.SetActiveImpersonation(preset, imp.CustomVersion, customHeaders)
	}

	prov, err := provider.NewProvider(resolved)
	if err != nil {
		return err
	}

	// Permission policy
	workingDir, _ := os.Getwd()
	allowedDirs := cfg.ExpandAllowedDirs(".")
	rules := make(map[string]permission.Decision)
	for t, perm := range cfg.ToolPerms {
		switch config.ToolPermission(perm) {
		case "allow":
			rules[t] = permission.Allow
		case "deny":
			rules[t] = permission.Deny
		default:
			rules[t] = permission.Ask
		}
	}
	mode := permission.ParsePermissionMode(cfg.DefaultMode)
	if bypass {
		mode = permission.BypassMode
	}
	policy := permission.NewConfigPolicyWithMode(rules, allowedDirs, mode)

	// Tools
	registry := tool.NewRegistry()
	if err := tool.RegisterBuiltinTools(registry, policy, workingDir); err != nil {
		return err
	}
	mergedMCPServers, _ := mcp.MergeStartupServers(workingDir, cfg.MCPServers)
	mcpMgr := plugin.NewMCPManager(mergedMCPServers, registry)
	_ = registry.Register(tool.ListMCPCapabilitiesTool{Runtime: mcpMgr})
	_ = registry.Register(tool.GetMCPPromptTool{Runtime: mcpMgr})
	_ = registry.Register(tool.ReadMCPResourceTool{Runtime: mcpMgr})

	pluginMgr := plugin.NewManager()
	pluginMgr.LoadAll(cfg.Plugins)
	if err := pluginMgr.RegisterTools(registry); err != nil {
		return err
	}

	autoMem := memory.NewAutoMemory()
	_ = registry.Register(tool.NewSaveMemoryTool(autoMem))

	autoContent, _, commandMgr := loadInteractiveStartupAssets(workingDir, autoMem)
	commandMgr.SetExtraProviders(func() []*commands.Command {
		return buildMCPSkillCommands(mcpMgr.SnapshotMCP())
	})
	projectMemoryLoader := func() (string, []string, error) {
		return memory.LoadProjectMemory(workingDir)
	}
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

	// System prompt
	gitStatus := detectGitStatus(workingDir)
	tools := registry.List()
	toolNames := make([]string, len(tools))
	for i, t := range tools {
		toolNames[i] = t.Name()
	}
	userSlashCmds := commandMgr.UserSlashCommands()
	customCmdNames := make([]string, 0, len(userSlashCmds))
	for name := range userSlashCmds {
		customCmdNames = append(customCmdNames, name)
	}
	systemPrompt := config.BuildSystemPrompt(cfg.SystemPrompt, workingDir, cfg.Language, toolNames, gitStatus, customCmdNames)
	if skillsPrompt := buildSkillsSystemPrompt(commandMgr.List()); skillsPrompt != "" {
		systemPrompt += "\n\n## Skills\n" + skillsPrompt
	}
	if mode == permission.AutopilotMode {
		systemPrompt += "\n\n## Autopilot\nDo not stop to ask the user for preferences or confirmation if a reasonable default exists. Choose the safest reversible assumption, explain it briefly if useful, and keep going until there is no meaningful work left. If progress is blocked on a user action, environment step, or missing external information that you cannot safely do yourself, call `ask_user` promptly instead of reporting that you are blocked and waiting. If you can perform the next step yourself with the available tools, do it instead of asking."
	}
	if autoContent != "" {
		systemPrompt += "\n\n## Auto Memory\n" + autoContent
	}

	// Agent
	ag := agent.NewAgent(prov, registry, systemPrompt, cfg.MaxIterations)
	if resolved.ContextWindow > 0 {
		ag.ContextManager().SetMaxTokens(resolved.ContextWindow)
	}
	if resolved.MaxTokens > 0 {
		ag.ContextManager().SetOutputReserve(resolved.MaxTokens)
	}
	ag.SetPermissionPolicy(policy)
	ag.SetWorkingDir(workingDir)
	ag.SetSupportsVision(resolved.SupportsVision)

	// Approval handler: auto-approve in daemon mode
	ag.SetApprovalHandler(func(toolName string, input string) permission.Decision {
		switch mode {
		case permission.BypassMode:
			return permission.Allow
		case permission.AutoMode:
			return permission.Allow
		default:
			return permission.Allow
		}
	})

	// --- Steps 9+: IM & Daemon setup ---

	// Session store
	store, err := session.NewDefaultStore()
	if err != nil {
		return fmt.Errorf("creating session store: %w", err)
	}

	// IM Manager
	imMgr := im.NewManager()
	bindingsPath, err := im.DefaultBindingsPath()
	if err != nil {
		return fmt.Errorf("resolving IM bindings path: %w", err)
	}
	bindingStore, err := im.NewJSONFileBindingStore(bindingsPath)
	if err != nil {
		return fmt.Errorf("creating IM binding store: %w", err)
	}
	if err := imMgr.SetBindingStore(bindingStore); err != nil {
		return fmt.Errorf("loading IM bindings: %w", err)
	}

	// Check for existing bindings for this workspace
	bindings, err := bindingStore.ListByWorkspace(workingDir)
	if err != nil {
		return fmt.Errorf("checking IM bindings: %w", err)
	}
	hasActiveBinding := false
	for _, b := range bindings {
		if strings.TrimSpace(b.ChannelID) != "" {
			hasActiveBinding = true
			break
		}
	}
	if !hasActiveBinding {
		return fmt.Errorf("当前工作目录没有配对的 IM 渠道。\n请先在 TUI 模式下通过 /qq、/tg 等命令配对 IM 渠道，然后再使用 daemon 模式。")
	}

	pairingPath, err := im.DefaultPairingStatePath()
	if err != nil {
		return fmt.Errorf("resolving IM pairing state path: %w", err)
	}
	pairingStore, err := im.NewJSONFilePairingStore(pairingPath)
	if err != nil {
		return fmt.Errorf("creating IM pairing store: %w", err)
	}
	if err := imMgr.SetPairingStore(pairingStore); err != nil {
		return fmt.Errorf("loading IM pairing state: %w", err)
	}

	// Bind session
	imMgr.BindSession(im.SessionBinding{Workspace: workingDir})

	// Determine language
	lang := "en"
	if cfg.Language == "zh-CN" || cfg.Language == "zh" {
		lang = "zh-CN"
	}

	// Create session
	vendor := cfg.Vendor
	endpoint := cfg.Endpoint
	modelName := cfg.Model
	ses := session.NewSession(vendor, endpoint, modelName)
	if err := store.Save(ses); err != nil {
		return fmt.Errorf("creating session: %w", err)
	}

	// Create emitter and daemon bridge
	emitter := im.NewIMEmitter(imMgr, lang)
	bridge := im.NewDaemonBridge(imMgr, ag, emitter, store, ses)

	// Wire ask_user handler
	if tl, ok := registry.Get("ask_user"); ok {
		if askTool, ok := tl.(*tool.AskUserTool); ok {
			askTool.SetHandler(bridge.HandleAskUser)
		}
	}

	// Sub-agent manager
	_ = subagent.NewManager(cfg.SubAgents)

	// Set bridge on manager
	imMgr.SetBridge(bridge)

	// Start adapters
	if cfg.IM.Enabled {
		controller, err := im.StartCurrentBindingAdapter(context.Background(), cfg.IM, imMgr)
		if err != nil {
			return fmt.Errorf("starting IM adapter: %w", err)
		}
		defer controller.Stop()
	}

	// Start MCP connections
	for _, warning := range mcpMgr.ConnectAll(context.Background()) {
		fmt.Fprintln(os.Stderr, warning)
	}
	mcpMgr.StartBackground(context.Background())
	defer mcpMgr.Close()

	// Load project memory synchronously (daemon mode has no TUI event loop)
	if projectMemoryLoader != nil {
		content, _, err := projectMemoryLoader()
		if err == nil && content != "" {
			ag.AddMessage(provider.Message{
				Role:    "system",
				Content: []provider.ContentBlock{{Type: "text", Text: "## Project Memory\n" + content}},
			})
		}
	}

	// Start command watcher
	if commandMgr != nil {
		stop := make(chan struct{})
		defer close(stop)
		go func() {
			ticker := time.NewTicker(2 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-ticker.C:
					commandMgr.Reload()
				case <-stop:
					return
				}
			}
		}()
	}

	// Wait for shutdown signal
	fmt.Fprintf(os.Stderr, "ggcode daemon 已启动 (session: %s)\n", ses.ID)
	fmt.Fprintf(os.Stderr, "工作目录: %s\n", workingDir)
	fmt.Fprintf(os.Stderr, "按 Ctrl+C 退出\n")

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	fmt.Fprintln(os.Stderr, "\n正在关闭...")

	// Save session on exit
	ses.Messages = ag.Messages()
	_ = store.Save(ses)

	fmt.Fprintln(os.Stderr, "ggcode daemon 已停止")
	return nil
}
