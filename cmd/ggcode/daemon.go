package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strconv"
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
	"github.com/topcheer/ggcode/internal/knight"
	"github.com/topcheer/ggcode/internal/mcp"
	"github.com/topcheer/ggcode/internal/memory"
	"github.com/topcheer/ggcode/internal/permission"
	"github.com/topcheer/ggcode/internal/plugin"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/restart"
	"github.com/topcheer/ggcode/internal/safego"
	"github.com/topcheer/ggcode/internal/session"
	"github.com/topcheer/ggcode/internal/subagent"
	"github.com/topcheer/ggcode/internal/tool"
	"github.com/topcheer/ggcode/internal/webui"
)

func newDaemonCmd(cfgFile *string) *cobra.Command {
	var bypassFlag, followFlag, backgroundFlag bool
	var resumeID string

	cmd := &cobra.Command{
		Use:   "daemon",
		Short: "Run ggcode in daemon mode, controlled via IM",
		Long:  "Run ggcode as a headless daemon. Messages from paired IM channels are forwarded to the agent, and responses are sent back via IM. Requires at least one IM channel bound to the current workspace.",
		RunE: func(cmd *cobra.Command, args []string) error {
			// Handle --resume-picker: set resumeID to trigger interactive picker
			if picker, _ := cmd.Flags().GetBool("resume-picker"); picker {
				resumeID = "picker"
			}

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
				return runDaemon(cfg, resolvedCfg, bypassFlag, followFlag, resumeID, true)
			}

			// If --background, fork and exit parent
			if backgroundFlag {
				return startBackgroundDaemon(cfg, resolvedCfg, bypassFlag, resumeID)
			}

			// Normal foreground start
			return runDaemon(cfg, resolvedCfg, bypassFlag, followFlag, resumeID, false)
		},
	}

	cmd.Flags().BoolVar(&bypassFlag, "bypass", false, "start in bypass permission mode (auto-approve safe ops)")
	cmd.Flags().BoolVar(&followFlag, "follow", false, "auto-enable follow mode")
	cmd.Flags().BoolVarP(&backgroundFlag, "background", "b", false, "start in background")
	cmd.Flags().StringVar(&resumeID, "resume", "", "resume a previous session by ID; use --resume-picker for interactive selection")
	cmd.Flags().Bool("resume-picker", false, "interactively select a session to resume")
	cmd.Flags().Bool("__daemonized", false, "internal: already daemonized")
	_ = cmd.Flags().MarkHidden("__daemonized")
	cmd.MarkFlagsMutuallyExclusive("follow", "background")
	cmd.MarkFlagsMutuallyExclusive("resume", "resume-picker")
	return cmd
}

// startBackgroundDaemon forks the process into background and exits the parent.
func startBackgroundDaemon(cfg *config.Config, cfgFile string, bypass bool, resumeID string) error {
	workingDir, _ := os.Getwd()
	lang := daemon.ResolveLang(cfg.Language)

	extraArgs := []string{"--bypass=" + fmt.Sprintf("%v", bypass)}
	if resumeID != "" {
		extraArgs = append(extraArgs, "--resume="+resumeID)
	}
	pid, err := daemon.ForkIntoBackground(cfgFile, workingDir, "", extraArgs...)
	if err != nil {
		return fmt.Errorf("starting background daemon: %w", err)
	}
	fmt.Fprintf(os.Stderr, "%s\n", daemon.Tr(lang, "daemon.started_bg", pid))
	fmt.Fprintf(os.Stderr, "%s\n", daemon.Tr(lang, "daemon.workdir", workingDir))
	return nil
}

func runDaemon(cfg *config.Config, cfgFile string, bypass bool, followActive bool, resumeID string, _ bool) error {
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
	_, knightProv, err := resolveKnightProvider(cfg, resolved, prov)
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
	var ag *agent.Agent
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
		a := agent.NewAgent(prov, tools.(*tool.Registry), systemPrompt, maxTurns)
		a.SetWorkingDir(ag.WorkingDir())
		return a
	}
	// Knight agent (created later but referenced via closure)
	var knightAgent *knight.Knight

	// Knight uses a different factory signature — it doesn't need provider/tools
	// passed each time because it creates its own agent for analysis tasks.
	knightFactory := func(systemPrompt string, maxTurns int, onUsage func(provider.TokenUsage)) (knight.AgentRunner, error) {
		a := agent.NewAgent(knightProv, registry, systemPrompt, maxTurns)
		if ag != nil {
			a.SetWorkingDir(ag.WorkingDir())
		}
		if onUsage != nil {
			a.SetUsageHandler(onUsage)
		}
		return a, nil
	}

	_ = registry.Register(tool.SkillTool{
		Skills:       commandMgr,
		Runtime:      mcpMgr,
		Provider:     prov,
		Tools:        registry,
		AgentFactory: skillAgentFactory,
		OnSkillUsed: func(ref string) {
			if knightAgent != nil {
				knightAgent.RecordSkillUse(ref)
			}
		},
		OnSkillCompleted: func(event tool.SkillExecutionEvent) {
			if knightAgent == nil {
				return
			}
			if event.Err != nil || event.Result.IsError {
				knightAgent.RecordSkillEffectiveness(event.Ref, 1)
				return
			}
			if event.Mode == tool.SkillExecutionModeFork {
				knightAgent.RecordSkillEffectiveness(event.Ref, 4)
				return
			}
			knightAgent.RecordSkillEffectiveness(event.Ref, 3)
		},
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
	ag = agent.NewAgent(prov, registry, systemPrompt, cfg.MaxIterations)
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
	ag.SetApprovalHandler(func(_ context.Context, toolName string, input string) permission.Decision {
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
		return fmt.Errorf("%s", daemon.Tr(daemon.ResolveLang(cfg.Language), "daemon.no_binding"))
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
	lang := daemon.ResolveLang(cfg.Language)

	// Create or resume session
	var ses *session.Session
	if resumeID == "-" || resumeID == "picker" {
		// Interactive session selection
		resumeID = pickSessionInteractive(store, lang)
	}
	if resumeID != "" {
		existing, err := store.Load(resumeID)
		if err != nil {
			return fmt.Errorf("loading session %s: %w", resumeID, err)
		}
		ses = existing
		// Restore messages to agent
		for _, msg := range ses.Messages {
			ag.AddMessage(msg)
		}
	} else {
		vendor := cfg.Vendor
		endpoint := cfg.Endpoint
		modelName := cfg.Model
		ses = session.NewSession(vendor, endpoint, modelName)
	}
	if err := store.Save(ses); err != nil {
		return fmt.Errorf("saving session: %w", err)
	}

	// Create emitter and daemon bridge
	emitter := im.NewIMEmitter(imMgr, string(lang), workingDir)
	if cfg.IM.OutputMode != "" {
		emitter.SetOutputMode(cfg.IM.OutputMode)
	}
	bridge := im.NewDaemonBridge(imMgr, ag, emitter, store, ses)

	// Wire checkpoint handler — persist compacted state after summarize
	ag.SetCheckpointHandler(func(messages []provider.Message, tokenCount int) {
		if err := store.AppendCheckpoint(ses, messages, tokenCount); err != nil {
			debug.Log("daemon", "checkpoint save failed: %v", err)
		} else {
			debug.Log("daemon", "checkpoint saved: %d messages, %d tokens", len(messages), tokenCount)
		}
	})

	// Wire ask_user handler
	if tl, ok := registry.Get("ask_user"); ok {
		if askTool, ok := tl.(*tool.AskUserTool); ok {
			askTool.SetHandler(bridge.HandleAskUser)
		}
	}

	// Sub-agent manager
	subMgr := subagent.NewManager(cfg.SubAgents)
	defer subMgr.Shutdown()

	// Create follow display (always, so the pairing watcher can use it)
	toolLang := im.ToolLangEn
	if lang == daemon.LangZhCN {
		toolLang = im.ToolLangZhCN
	}
	toolPresenter := &daemonToolPresenter{lang: toolLang}
	followDisplay := daemon.NewTerminalFollowDisplay(os.Stderr, lang, workingDir, toolPresenter)

	// Set bridge on manager
	imMgr.SetBridge(bridge)

	// Track previous pairing state so we can notify follow display.
	// When follow mode is off, pairing code is printed to stderr directly
	// so the user can still see it even without follow mode.
	var prevPairingChallenge *im.PairingChallenge
	imMgr.SetOnUpdate(func(snap im.StatusSnapshot) {
		current := snap.PendingPairing
		wasPending := prevPairingChallenge != nil
		isPending := current != nil

		if isPending && !wasPending {
			// New pairing challenge appeared — always show it
			platformName := daemon.PlatformDisplayName(string(current.Platform))
			kind := string(current.Kind)
			followDisplay.OnPairingChallenge(platformName, current.ChannelID, current.Code, kind)
		} else if !isPending && wasPending {
			// Pairing resolved (accepted or rejected)
			if followActive {
				followDisplay.OnPairingResolved()
			}
		}

		if current != nil {
			cp := *current
			prevPairingChallenge = &cp
		} else {
			prevPairingChallenge = nil
		}
	})

	// Start adapters
	if cfg.IM.Enabled {
		controller, err := im.StartCurrentBindingAdapter(context.Background(), cfg.IM, imMgr)
		if err != nil {
			return fmt.Errorf("starting IM adapter: %w", err)
		}
		defer controller.Stop()
	}

	// Register this instance for multi-instance detection.
	// If another instance is already running in the same workspace,
	// auto-mute all IM channels so only the primary instance responds.
	_, others, err := imMgr.RegisterInstance(workingDir)
	if err != nil {
		debug.Log("daemon", "instance detect register failed: %v", err)
	} else if len(others) > 0 {
		primary := others[0]
		fmt.Fprintf(os.Stderr, "🔇 Auto-muted IM channels — primary instance (PID %d, started %s)\n",
			primary.PID, primary.StartedAt.Format("15:04"))
	}
	defer imMgr.UnregisterInstance()

	// Start MCP connections
	for _, warning := range mcpMgr.ConnectAll(context.Background()) {
		fmt.Fprintln(os.Stderr, warning)
	}
	mcpMgr.StartBackground(context.Background())
	defer mcpMgr.Close()

	// MCP OAuth watcher for daemon follow mode
	mcpMgr.SetURLOpener(func(url string) error {
		fmt.Fprintf(os.Stderr, "\x1b[34m\u2b21 MCP OAuth:\x1b[0m opening browser %s\r\n", url)
		return nil
	})
	mcpOAuthDone := make(chan string, 4)
	go func() {
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			pending := mcpMgr.PendingOAuth()
			if pending == nil {
				continue
			}
			mcpMgr.ClearPendingOAuth()
			handler := pending.Handler
			serverName := pending.ServerName
			go func() {
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				if handler.SupportsDCR() {
					_ = handler.RegisterClient(ctx)
				}
				// Try device flow first
				if handler.SupportsDeviceFlow() {
					scopes := handler.GetScopes()
					if len(scopes) > 4 {
						scopes = scopes[:4]
					}
					devResp, err := handler.StartDeviceFlow(ctx, scopes)
					if err == nil {
						fmt.Fprintf(os.Stderr, "\x1b[33m\u2b21 MCP OAuth: %s\x1b[0m\r\n", serverName)
						fmt.Fprintf(os.Stderr, "\x1b[36m  Device Code: %s\x1b[0m\r\n", devResp.UserCode)
						writeClipboard(devResp.UserCode)
						fmt.Fprintf(os.Stderr, "\x1b[36m  Visit: %s\x1b[0m\r\n", devResp.VerificationURI)
						pollCtx, pollCancel := context.WithTimeout(context.Background(), 15*time.Minute)
						defer pollCancel()
						tokenResp, pollErr := handler.PollDeviceToken(pollCtx)
						if pollErr != nil {
							fmt.Fprintf(os.Stderr, "\x1b[31m  MCP OAuth failed for %s: %v\x1b[0m\r\n", serverName, pollErr)
							return
						}
						if saveErr := handler.SaveToken(tokenResp); saveErr != nil {
							fmt.Fprintf(os.Stderr, "\x1b[31m  MCP OAuth save failed: %v\x1b[0m\r\n", saveErr)
							return
						}
						fmt.Fprintf(os.Stderr, "\x1b[32m  MCP server %s authenticated \u2713\x1b[0m\r\n", serverName)
						mcpOAuthDone <- serverName
						return
					}
				}
				// Browser flow fallback
				authorizeURL, err := handler.StartAuthFlow(ctx)
				if err != nil {
					fmt.Fprintf(os.Stderr, "\x1b[31m  MCP OAuth failed for %s: %v\x1b[0m\r\n", serverName, err)
					return
				}
				fmt.Fprintf(os.Stderr, "\x1b[33m\u2b21 MCP OAuth: %s\x1b[0m\r\n", serverName)
				fmt.Fprintf(os.Stderr, "\x1b[36m  Visit: %s\x1b[0m\r\n", authorizeURL)
				cbCtx, cbCancel := context.WithTimeout(context.Background(), 5*time.Minute)
				defer cbCancel()
				code, cbErr := handler.WaitForCallback(cbCtx)
				if cbErr != nil {
					fmt.Fprintf(os.Stderr, "\x1b[31m  MCP OAuth failed for %s: %v\x1b[0m\r\n", serverName, cbErr)
					return
				}
				tokenResp, exErr := handler.ExchangeCode(cbCtx, code)
				if exErr != nil {
					fmt.Fprintf(os.Stderr, "\x1b[31m  MCP OAuth exchange failed: %v\x1b[0m\r\n", exErr)
					return
				}
				if saveErr := handler.SaveToken(tokenResp); saveErr != nil {
					fmt.Fprintf(os.Stderr, "\x1b[31m  MCP OAuth save failed: %v\x1b[0m\r\n", saveErr)
					return
				}
				fmt.Fprintf(os.Stderr, "\x1b[32m  MCP server %s authenticated \u2713\x1b[0m\r\n", serverName)
				mcpOAuthDone <- serverName
			}()
		}
	}()
	go func() {
		for name := range mcpOAuthDone {
			mcpMgr.Retry(name)
		}
	}()

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

	// Start Knight background agent (if enabled)
	homeDir, _ := os.UserHomeDir()
	knightAgent = knight.New(cfg.Knight(), homeDir, workingDir, store)
	knightAgent.SetFactory(knightFactory)
	bridge.SetActivityHook(knightAgent.NotifyActivity)
	if cfg.Knight().Enabled {
		// Create Knight emitter (reuse IM emitter)
		knightAgent.SetEmitter(emitter)
		if err := knightAgent.Start(context.Background()); err != nil {
			fmt.Fprintf(os.Stderr, "Knight startup warning: %v\n", err)
		} else {
			defer knightAgent.Stop()
			fmt.Fprintf(os.Stderr, "🌙 Knight started (budget: %dM tokens/day)\n", cfg.Knight().DailyTokenBudget/1_000_000)
		}
	}

	// Start A2A server if enabled.
	if cfg.A2A.Enabled {
		a2aSrv, a2aReg, err := startA2AServer(cfg, ag, registry, workingDir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "A2A server warning: %v\n", err)
		} else {
			defer func() {
				_ = a2aReg.Unregister()
				a2aSrv.Stop()
			}()
			fmt.Fprintf(os.Stderr, "🔗 A2A server: %s\n", a2aSrv.Endpoint())
		}
	}

	// Start command watcher
	if commandMgr != nil {
		stop := make(chan struct{})
		defer close(stop)
		safego.Go("daemon.cmd.commandReload", func() {
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
		})
	}

	// --- Follow mode setup ---
	if followActive {
		bridge.SetFollowSink(followDisplay)
	}

	// Check if stdin is a terminal (for keyboard handling)
	isTerminal := term.IsTerminal(int(os.Stdin.Fd()))

	// Startup message (before raw mode — normal \n is fine)
	fmt.Fprintf(os.Stderr, "%s\n", daemon.Tr(lang, "daemon.started", ses.ID))
	fmt.Fprintf(os.Stderr, "%s\n", daemon.Tr(lang, "daemon.workdir", workingDir))
	if isTerminal {
		fmt.Fprintf(os.Stderr, "%s\n", daemon.Tr(lang, "daemon.keys_full"))
	} else {
		fmt.Fprintf(os.Stderr, "%s\n", daemon.Tr(lang, "daemon.keys_minimal"))
	}

	// WebUI server
	webuiSrv := webui.NewServer(cfg)
	webuiSrv.SetMCPStatusFn(func() map[string]webui.MCPRuntimeStatus {
		snapshot := mcpMgr.Snapshot()
		m := make(map[string]webui.MCPRuntimeStatus, len(snapshot))
		for _, info := range snapshot {
			m[info.Name] = webui.MCPRuntimeStatus{
				Connected: string(info.Status) == "connected",
				Pending:   string(info.Status) == "pending",
				Disabled:  info.Disabled,
				Error:     info.Error,
				Tools:     info.ToolNames,
			}
		}
		return m
	})
	webuiSrv.SetIMStatusFn(func() []webui.IMRuntimeStatus {
		if imMgr == nil {
			return nil
		}
		snap := imMgr.Snapshot()
		disabledSet := map[string]bool{}
		for _, b := range snap.DisabledBindings {
			disabledSet[b.Adapter] = true
		}
		mutedSet := map[string]bool{}
		for _, b := range snap.MutedBindings {
			mutedSet[b.Adapter] = true
		}
		stateMap := map[string]im.AdapterState{}
		for _, st := range snap.Adapters {
			stateMap[st.Name] = st
		}
		// Persisted bindings: adapter -> all bound workspaces
		type persistedInfo struct{ workspace, channel, targetID string }
		persistedMap := map[string][]persistedInfo{}
		for _, pb := range imMgr.AllPersistedBindings() {
			persistedMap[pb.Adapter] = append(persistedMap[pb.Adapter], persistedInfo{pb.Workspace, pb.ChannelID, pb.TargetID})
		}
		allDirsMap := map[string][]string{}
		for a, bs := range persistedMap {
			ds := make([]string, 0, len(bs))
			for _, b := range bs {
				ds = append(ds, b.workspace)
			}
			allDirsMap[a] = ds
		}
		out := make([]webui.IMRuntimeStatus, 0)
		seen := map[string]bool{}
		for _, b := range snap.CurrentBindings {
			st := stateMap[b.Adapter]
			s := webui.IMRuntimeStatus{
				Adapter: b.Adapter, Platform: string(b.Platform), Healthy: st.Healthy, Status: st.Status,
				BoundDir: b.Workspace, ChannelID: b.ChannelID, TargetID: b.TargetID,
				Muted: mutedSet[b.Adapter], Disabled: disabledSet[b.Adapter], AllDirs: allDirsMap[b.Adapter],
			}
			if st.LastError != "" {
				s.LastError = st.LastError
			}
			out = append(out, s)
			seen[b.Adapter] = true
		}
		// Adapters with persisted bindings but no current runtime binding
		for adapter, bs := range persistedMap {
			if seen[adapter] {
				continue
			}
			st := stateMap[adapter]
			pb := bs[0]
			s := webui.IMRuntimeStatus{
				Adapter: adapter, Platform: string(st.Platform), Healthy: st.Healthy, Status: st.Status,
				LastError: st.LastError, BoundDir: pb.workspace, ChannelID: pb.channel, TargetID: pb.targetID,
				Muted: mutedSet[adapter], Disabled: disabledSet[adapter], AllDirs: allDirsMap[adapter],
			}
			out = append(out, s)
			seen[adapter] = true
		}
		for name, st := range stateMap {
			if seen[name] {
				continue
			}
			out = append(out, webui.IMRuntimeStatus{
				Adapter: name, Platform: string(st.Platform), Healthy: st.Healthy,
				Status: st.Status, LastError: st.LastError,
				Muted: mutedSet[name], Disabled: disabledSet[name],
			})
		}
		return out
	})
	webuiSrv.SetIMActionFn(func(adapter string, action string) error {
		if imMgr == nil {
			return fmt.Errorf("IM runtime not available")
		}
		switch action {
		case "mute":
			return imMgr.MuteBinding(adapter)
		case "unmute":
			return imMgr.UnmuteBinding(adapter)
		case "disable":
			return imMgr.DisableBinding(adapter)
		case "enable":
			return imMgr.EnableBinding(adapter)
		default:
			return fmt.Errorf("unknown action: %s", action)
		}
	})
	webuiAddr := "127.0.0.1:0" // auto port
	actualAddr, webuiErr := webuiSrv.Start(webuiAddr)
	if webuiErr == nil {
		fmt.Fprintf(os.Stderr, "\x1b[34m⬡ WebUI:\x1b[0m \x1b[32mhttp://%s\x1b[0m\r\n", actualAddr)
	} else {
		fmt.Fprintf(os.Stderr, "WebUI start failed: %v\r\n", webuiErr)
	}
	defer webuiSrv.Close()

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

	var daemonRestartRequested bool

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
					fmt.Fprintf(os.Stderr, "%s\r\n", daemon.Tr(lang, "daemon.follow_on"))
				} else {
					bridge.SetFollowSink(nil)
					fmt.Fprintf(os.Stderr, "%s\r\n", daemon.Tr(lang, "daemon.follow_off"))
				}
			case 'v': // IM output mode: verbose
				if emitter != nil {
					emitter.SetOutputMode("verbose")
					fmt.Fprintf(os.Stderr, "%s\r\n", daemon.Tr(lang, "daemon.output_mode", "verbose"))
				}
			case 'q': // IM output mode: quiet
				if emitter != nil {
					emitter.SetOutputMode("quiet")
					fmt.Fprintf(os.Stderr, "%s\r\n", daemon.Tr(lang, "daemon.output_mode", "quiet"))
				}
			case 's': // IM output mode: summary
				if emitter != nil {
					emitter.SetOutputMode("summary")
					fmt.Fprintf(os.Stderr, "%s\r\n", daemon.Tr(lang, "daemon.output_mode", "summary"))
				}
			case 'M': // mute all IM channels
				if imMgr != nil {
					count, err := imMgr.MuteAll()
					if err == nil && count > 0 {
						fmt.Fprintf(os.Stderr, "%s\r\n", daemon.Tr(lang, "daemon.mute_all", count))
					}
				}
			case 'U': // unmute all IM channels
				if imMgr != nil {
					count, err := imMgr.UnmuteAll()
					if err == nil && count > 0 {
						fmt.Fprintf(os.Stderr, "%s\r\n", daemon.Tr(lang, "daemon.unmute_all", count))
					}
				}
			case 'r': // restart
				daemonRestartRequested = true
				fmt.Fprintf(os.Stderr, "%s\r\n", "[ggcode restart] restarting...")
				break loop
			}
		}
	}

	// Restore terminal before printing further output
	if restoreTerminal != nil {
		restoreTerminal()
	}

	if daemonRestartRequested {
		// Self-restart: replace this process with a fresh ggcode daemon.
		binary, err := restart.ResolveBinary()
		if err != nil {
			return fmt.Errorf("restart: resolve binary: %w", err)
		}
		ses.Messages = ag.Messages()
		_ = store.Save(ses)

		var args []string
		if cfgFile != "" {
			args = append(args, "--config", cfgFile)
		}
		args = append(args, "daemon", "--follow")
		if ses.ID != "" {
			args = append(args, "--resume", ses.ID)
		}
		if bypass {
			args = append(args, "--bypass")
		}
		execArgs := append([]string{binary}, args...)
		fmt.Fprintf(os.Stderr, "[ggcode restart] exec %s\n", strings.Join(execArgs, " "))
		return syscall.Exec(binary, execArgs, os.Environ())
	}

	fmt.Fprint(os.Stderr, daemon.Tr(lang, "daemon.shutting_down")+"\n")

	// Save session on exit
	ses.Messages = ag.Messages()
	_ = store.Save(ses)

	fmt.Fprintln(os.Stderr, daemon.Tr(lang, "daemon.stopped"))
	return nil
}

// readKeyboard reads raw keystrokes from stdin and sends them to the channel.
// Returns a function that restores the terminal to its original state.
func readKeyboard(ch chan<- byte) func() {
	oldState, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		return func() {}
	}

	safego.Go("daemon.keyboard.read", func() {
		defer close(ch)
		buf := make([]byte, 1)
		for {
			n, err := os.Stdin.Read(buf)
			if err != nil || n == 0 {
				return
			}
			ch <- buf[0]
		}
	})

	return func() {
		term.Restore(int(os.Stdin.Fd()), oldState)
	}
}

// detachToBackground forks the daemon into background mode.
func detachToBackground(lang daemon.Lang, cfgFile, workingDir, sessionID string) {
	pid, err := daemon.ForkIntoBackground(cfgFile, workingDir, sessionID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s\n", daemon.Tr(lang, "daemon.bg_fail", err))
		return
	}
	fmt.Fprintf(os.Stderr, "%s\n", daemon.Tr(lang, "daemon.bg_ok", pid))
}

// pickSessionInteractive lists up to 10 sessions for the current workspace and reads the user's choice from stdin.
// Returns the selected session ID, or empty string to start a new session.
func pickSessionInteractive(store session.Store, lang daemon.Lang) string {
	sessions, err := store.List()
	if err != nil || len(sessions) == 0 {
		fmt.Fprintln(os.Stderr, daemon.Tr(lang, "daemon.resume.empty"))
		return ""
	}

	// Filter to current workspace only
	workingDir, _ := os.Getwd()
	normalizedWD := session.NormalizeWorkspacePath(workingDir)
	var filtered []*session.Session
	for _, s := range sessions {
		if s.Workspace == normalizedWD {
			filtered = append(filtered, s)
		}
	}
	if len(filtered) == 0 {
		fmt.Fprintln(os.Stderr, daemon.Tr(lang, "daemon.resume.empty"))
		return ""
	}

	// Limit to latest 10
	if len(filtered) > 10 {
		filtered = filtered[:10]
	}

	fmt.Fprintln(os.Stderr, daemon.Tr(lang, "daemon.resume.title"))
	for i, s := range filtered {
		title := s.Title
		if title == "" {
			title = "untitled"
		}
		fmt.Fprintf(os.Stderr, daemon.Tr(lang, "daemon.resume.item")+"\n", i+1, s.ID, title)
	}
	fmt.Fprint(os.Stderr, daemon.Tr(lang, "daemon.resume.prompt"))

	reader := bufio.NewReader(os.Stdin)
	line, _ := reader.ReadString('\n')
	line = strings.TrimSpace(line)
	if line == "" {
		return ""
	}
	idx, err := strconv.Atoi(line)
	if err != nil || idx < 1 || idx > len(filtered) {
		fmt.Fprintln(os.Stderr, daemon.Tr(lang, "daemon.resume.invalid"))
		return ""
	}
	return filtered[idx-1].ID
}

// daemonToolPresenter adapts the IM DescribeTool function to daemon.ToolPresenter.
type daemonToolPresenter struct {
	lang im.ToolLanguage
}

func (p *daemonToolPresenter) Present(toolName, rawArgs string) (displayName, detail, activity string) {
	pres := im.DescribeTool(p.lang, toolName, rawArgs)
	return pres.DisplayName, pres.Detail, pres.Activity
}

// writeClipboard copies text to the system clipboard (best-effort).
func writeClipboard(text string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("pbcopy")
	case "linux":
		if _, err := exec.LookPath("xclip"); err == nil {
			cmd = exec.Command("xclip", "-selection", "clipboard")
		} else if _, err := exec.LookPath("xsel"); err == nil {
			cmd = exec.Command("xsel", "--clipboard", "--input")
		} else if _, err := exec.LookPath("wl-copy"); err == nil {
			cmd = exec.Command("wl-copy")
		}
	case "windows":
		cmd = exec.Command("clip")
	}
	if cmd == nil {
		return
	}
	cmd.Stdin = strings.NewReader(text)
	_ = cmd.Run()
}
