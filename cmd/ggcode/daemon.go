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
	"golang.org/x/term"

	"github.com/topcheer/ggcode/internal/agent"
	"github.com/topcheer/ggcode/internal/commands"
	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/daemon"
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
	var bypassFlag, followFlag, backgroundFlag bool

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

			// If --__daemonized, skip fork logic — we ARE the daemonized child
			if daemonized, _ := cmd.Flags().GetBool("__daemonized"); daemonized {
				return runDaemon(cfg, resolvedCfg, bypassFlag, true)
			}

			// If --background, fork and exit parent
			if backgroundFlag {
				return startBackgroundDaemon(cfg, resolvedCfg, bypassFlag)
			}

			// Normal foreground start
			return runDaemon(cfg, resolvedCfg, bypassFlag, followFlag)
		},
	}

	cmd.Flags().BoolVar(&bypassFlag, "bypass", false, "start in bypass permission mode (auto-approve safe ops)")
	cmd.Flags().BoolVar(&followFlag, "follow", false, "auto-enable follow mode")
	cmd.Flags().BoolVarP(&backgroundFlag, "background", "b", false, "start in background")
	cmd.Flags().Bool("__daemonized", false, "internal: already daemonized")
	_ = cmd.Flags().MarkHidden("__daemonized")
	cmd.MarkFlagsMutuallyExclusive("follow", "background")
	return cmd
}

// startBackgroundDaemon forks the process into background and exits the parent.
func startBackgroundDaemon(cfg *config.Config, cfgFile string, bypass bool) error {
	workingDir, _ := os.Getwd()
	lang := daemonLang(cfg)

	pid, err := daemon.ForkIntoBackground(cfgFile, workingDir, "", "--bypass="+fmt.Sprintf("%v", bypass))
	if err != nil {
		return fmt.Errorf("starting background daemon: %w", err)
	}
	if lang == "zh-CN" {
		fmt.Fprintf(os.Stderr, "ggcode daemon 已在后台启动 (PID: %d)\n", pid)
		fmt.Fprintf(os.Stderr, "工作目录: %s\n", workingDir)
	} else {
		fmt.Fprintf(os.Stderr, "ggcode daemon started in background (PID: %d)\n", pid)
		fmt.Fprintf(os.Stderr, "Working directory: %s\n", workingDir)
	}
	return nil
}

func runDaemon(cfg *config.Config, cfgFile string, bypass bool, followActive bool) error {
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
		if daemonLang(cfg) == "zh-CN" {
			return fmt.Errorf("当前工作目录没有配对的 IM 渠道。\n请先在 TUI 模式下通过 /qq、/tg 等命令配对 IM 渠道，然后再使用 daemon 模式。")
		}
		return fmt.Errorf("No IM channel paired with this workspace.\nPair an IM channel via /qq, /tg etc in TUI mode first, then use daemon mode.")
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

	// --- Follow mode setup ---
	toolLang := im.ToolLangEn
	if lang == "zh-CN" {
		toolLang = im.ToolLangZhCN
	}
	followLang := daemon.LangEn
	if lang == "zh-CN" {
		followLang = daemon.LangZhCN
	}
	toolFormatter := func(toolName, rawArgs string) string {
		pres := im.DescribeTool(toolLang, toolName, rawArgs)
		activity := pres.Activity
		if activity == "" {
			activity = im.FormatToolInline(pres.DisplayName, pres.Detail)
		}
		if activity == "" {
			return ""
		}
		return im.LocalizeIMProgress(toolLang, activity)
	}
	followDisplay := daemon.NewTerminalFollowDisplay(os.Stderr, followLang, toolFormatter)
	if followActive {
		bridge.SetFollowSink(followDisplay)
	}

	// Check if stdin is a terminal (for keyboard handling)
	isTerminal := term.IsTerminal(int(os.Stdin.Fd()))

	// Startup message (before raw mode — normal \n is fine)
	if lang == "zh-CN" {
		fmt.Fprintf(os.Stderr, "ggcode daemon 已启动 (session: %s)\n", ses.ID)
		fmt.Fprintf(os.Stderr, "工作目录: %s\n", workingDir)
		if isTerminal {
			fmt.Fprintf(os.Stderr, "按 x 退出, d 后台运行, f 切换 follow 模式\n")
		} else {
			fmt.Fprintf(os.Stderr, "按 x 退出\n")
		}
	} else {
		fmt.Fprintf(os.Stderr, "ggcode daemon started (session: %s)\n", ses.ID)
		fmt.Fprintf(os.Stderr, "Working directory: %s\n", workingDir)
		if isTerminal {
			fmt.Fprintf(os.Stderr, "Press x to exit, d for background, f to toggle follow\n")
		} else {
			fmt.Fprintf(os.Stderr, "Press x to exit\n")
		}
	}

	// Signal handling
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	// Keyboard goroutine (only if stdin is a terminal)
	kbCh := make(chan byte, 8)
	var restoreTerminal func()
	if isTerminal {
		restoreTerminal = readKeyboard(kbCh)
	}

	// Register PID file for cleanup
	defer daemon.CleanupDaemon(workingDir)

	// Main loop
loop:
	for {
		select {
		case sig := <-sigCh:
			_ = sig
			break loop
		case b, ok := <-kbCh:
			if !ok {
				break loop
			}
			switch b {
			case 'x': // exit
				break loop
			case 'd': // detach to background
				detachToBackground(lang, cfgFile, workingDir, ses.ID)
				break loop
			case 'f': // toggle follow mode
				followActive = !followActive
				if followActive {
					bridge.SetFollowSink(followDisplay)
					if lang == "zh-CN" {
						fmt.Fprint(os.Stderr, "follow 模式已开启\r\n")
					} else {
						fmt.Fprint(os.Stderr, "follow mode enabled\r\n")
					}
				} else {
					bridge.SetFollowSink(nil)
					if lang == "zh-CN" {
						fmt.Fprint(os.Stderr, "follow 模式已关闭\r\n")
					} else {
						fmt.Fprint(os.Stderr, "follow mode disabled\r\n")
					}
				}
			}
		}
	}

	// Restore terminal before printing further output
	if restoreTerminal != nil {
		restoreTerminal()
	}

	if lang == "zh-CN" {
		fmt.Fprintln(os.Stderr, "\n正在关闭...")
	} else {
		fmt.Fprintln(os.Stderr, "\nShutting down...")
	}

	// Save session on exit
	ses.Messages = ag.Messages()
	_ = store.Save(ses)

	if lang == "zh-CN" {
		fmt.Fprintln(os.Stderr, "ggcode daemon 已停止")
	} else {
		fmt.Fprintln(os.Stderr, "ggcode daemon stopped")
	}
	return nil
}

// readKeyboard reads raw keystrokes from stdin and sends them to the channel.
// Returns a function that restores the terminal to its original state.
func readKeyboard(ch chan<- byte) func() {
	oldState, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		return func() {}
	}

	go func() {
		defer close(ch)
		buf := make([]byte, 1)
		for {
			n, err := os.Stdin.Read(buf)
			if err != nil || n == 0 {
				return
			}
			ch <- buf[0]
		}
	}()

	return func() {
		term.Restore(int(os.Stdin.Fd()), oldState)
	}
}

// detachToBackground forks the daemon into background mode.
func detachToBackground(lang, cfgFile, workingDir, sessionID string) {
	pid, err := daemon.ForkIntoBackground(cfgFile, workingDir, sessionID)
	if err != nil {
		if lang == "zh-CN" {
			fmt.Fprintf(os.Stderr, "后台启动失败: %v\n", err)
		} else {
			fmt.Fprintf(os.Stderr, "Failed to start background: %v\n", err)
		}
		return
	}
	if lang == "zh-CN" {
		fmt.Fprintf(os.Stderr, "已切换到后台 (PID: %d)\n", pid)
	} else {
		fmt.Fprintf(os.Stderr, "Switched to background (PID: %d)\n", pid)
	}
}

// daemonLang returns the language string from config.
func daemonLang(cfg *config.Config) string {
	if cfg.Language == "zh-CN" || cfg.Language == "zh" {
		return "zh-CN"
	}
	return "en"
}
