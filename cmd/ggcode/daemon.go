package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/topcheer/ggcode/internal/a2a"
	"github.com/topcheer/ggcode/internal/agent"
	"github.com/topcheer/ggcode/internal/checkpoint"
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
	"github.com/topcheer/ggcode/internal/tunnel"
	"github.com/topcheer/ggcode/internal/version"
	"github.com/topcheer/ggcode/internal/webui"
)

func newDaemonCmd(cfgFile *string) *cobra.Command {
	var bypassFlag, followFlag, backgroundFlag, tunnelFlag bool
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
			daemonWorkDir, _ := os.Getwd()
			cfg, err := config.LoadWithInstance(resolvedCfg, daemonWorkDir)
			if err != nil {
				return fmt.Errorf("loading config: %w", err)
			}
			if _, _, err := mcp.PersistUserClaudeServers(cfg); err != nil {
				return fmt.Errorf("persisting Claude MCP servers: %w", err)
			}

			// If --__daemonized, skip fork logic — we ARE the daemonized child
			if daemonized, _ := cmd.Flags().GetBool("__daemonized"); daemonized {
				noIM, _ := cmd.Flags().GetBool("__no-im")
				return runDaemon(cfg, resolvedCfg, bypassFlag, followFlag, resumeID, true, noIM, tunnelFlag)
			}

			// If --background, fork and exit parent
			if backgroundFlag {
				return startBackgroundDaemon(cfg, resolvedCfg, bypassFlag, resumeID)
			}

			// Normal foreground start
			noIM, _ := cmd.Flags().GetBool("__no-im")
			return runDaemon(cfg, resolvedCfg, bypassFlag, followFlag, resumeID, false, noIM, tunnelFlag)
		},
	}

	cmd.Flags().BoolVar(&bypassFlag, "bypass", false, "start in bypass permission mode (auto-approve safe ops)")
	cmd.Flags().BoolVar(&followFlag, "follow", false, "auto-enable follow mode")
	cmd.Flags().BoolVarP(&backgroundFlag, "background", "b", false, "start in background")
	cmd.Flags().BoolVar(&tunnelFlag, "tunnel", false, "start with mobile tunnel (QR code for GGCode Mobile)")
	cmd.Flags().StringVar(&resumeID, "resume", "", "resume a previous session by ID; use --resume-picker for interactive selection")
	cmd.Flags().Bool("resume-picker", false, "interactively select a session to resume")
	cmd.Flags().Bool("__daemonized", false, "internal: already daemonized")
	cmd.Flags().Bool("__no-im", false, "internal: skip IM binding check (A2A-only testing)")
	_ = cmd.Flags().MarkHidden("__daemonized")
	_ = cmd.Flags().MarkHidden("__no-im")
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

func runDaemon(cfg *config.Config, cfgFile string, bypass bool, followActive bool, resumeID string, _ bool, noIM bool, startTunnel bool) error {
	// --- Steps 1-8: same as run() in root.go ---

	prov, resolved, err := ResolveProvider(cfg)
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
	projectAutoMem := memory.NewProjectAutoMemory(workingDir)
	_ = registry.Register(tool.NewSaveMemoryTool(autoMem, projectAutoMem))

	_, _, _, commandMgr := loadInteractiveStartupAssets(workingDir, autoMem, projectAutoMem)
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
		WorkingDir:   workingDir,
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
	buildCurrentSystemPrompt := func() (string, []string) {
		userSlashCmds := commandMgr.UserSlashCommands()
		customCmdNames := make([]string, 0, len(userSlashCmds))
		for name := range userSlashCmds {
			customCmdNames = append(customCmdNames, name)
		}
		prompt := config.BuildSystemPrompt(cfg.ExtraPrompt, workingDir, cfg.Language, toolNames, gitStatus, customCmdNames)
		skillsPrompt, promptSkillRefs := buildSkillsSystemPromptWithPromptRefs(commandMgr.List())
		if skillsPrompt != "" {
			prompt += "\n\n## Skills\n" + skillsPrompt
		}
		if mode == permission.AutopilotMode {
			prompt += "\n\n## Autopilot\nDo not stop to ask the user for preferences or confirmation if a reasonable default exists. Choose the safest reversible assumption, explain it briefly if useful, and keep going until there is no meaningful work left. If progress is blocked on a user action, environment step, or missing external information that you cannot safely do yourself, call `ask_user` promptly instead of reporting that you are blocked and waiting. If you can perform the next step yourself with the available tools, do it instead of asking."
		}
		if globalAutoContent, _, _ := autoMem.LoadAll(); globalAutoContent != "" {
			prompt += "\n\n## Auto Memory (Global)\n" + globalAutoContent
		}
		if projectAutoMem != nil {
			if projContent, _, _ := projectAutoMem.LoadAll(); projContent != "" {
				prompt += "\n\n## Auto Memory (Project)\n" + projContent
			}
		}
		return prompt, promptSkillRefs
	}
	systemPrompt, promptSkillRefs := buildCurrentSystemPrompt()
	var promptSkillRefsMu sync.RWMutex
	currentPromptSkillRefs := func() []string {
		promptSkillRefsMu.RLock()
		defer promptSkillRefsMu.RUnlock()
		return append([]string(nil), promptSkillRefs...)
	}

	// Agent
	ag = agent.NewAgent(prov, registry, systemPrompt, cfg.MaxIterations)
	refreshAgentSystemPrompt := func() {
		nextPrompt, nextRefs := buildCurrentSystemPrompt()
		systemPrompt = nextPrompt
		promptSkillRefsMu.Lock()
		promptSkillRefs = append(promptSkillRefs[:0], nextRefs...)
		promptSkillRefsMu.Unlock()
		ag.UpdateSystemPrompt(systemPrompt)
		if knightAgent != nil {
			knightAgent.RecordSkillPromptExposure(nextRefs)
		}
	}
	ag.SetRunResultWithContentHandler(func(content []provider.ContentBlock, err error) {
		if knightAgent == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return
		}
		refs := currentPromptSkillRefs()
		if len(refs) == 0 {
			return
		}
		knightAgent.RecordPromptSkillOutcome(refs, err == nil)
		if scenarioErr := knightAgent.RecordPromptSkillScenario(refs, content, err == nil, err); scenarioErr != nil {
			debug.Log("daemon", "Knight scenario record failed: %v", scenarioErr)
		}
	})
	if resolved.ContextWindow > 0 {
		ag.ContextManager().SetContextWindow(resolved.ContextWindow)
	}
	ag.SetProbeKey(provider.MakeProbeKey(resolved.VendorID, resolved.BaseURL, resolved.Model))
	if resolved.MaxTokens > 0 {
		ag.ContextManager().SetOutputReserve(resolved.MaxTokens)
	}
	ag.SetPermissionPolicy(policy)
	ag.SetWorkingDir(workingDir)
	ag.SetSupportsVision(resolved.SupportsVision)
	ag.SetCheckpointManager(checkpoint.NewManager(50))
	tool.SetPreWriteHook(tool.CheckpointSaver(ag.CheckpointManager()))

	// Approval handler: always auto-approve in daemon mode.
	// Daemon has no TUI for interactive prompts. BypassMode and AutoMode
	// auto-approve by design. SupervisedMode is intentionally mapped to
	// Allow here because the IM approval flow (ask_user tool) handles
	// user confirmation at a higher level — the agent decides when to
	// ask via ask_user, not via the permission system's Ask callback.
	// See docs/design/daemon-permission-model.md for rationale.
	ag.SetApprovalHandler(func(_ context.Context, toolName string, input string) permission.Decision {
		return permission.Allow
	})

	// --- Steps 9+: IM & Daemon setup ---

	// Session store
	store, err := session.NewDefaultStore()
	if err != nil {
		return fmt.Errorf("creating session store: %w", err)
	}

	var daemonRestartRequested bool
	restartCh := make(chan struct{}, 1)

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
		if noIM {
			fmt.Fprintf(os.Stderr, "⚠️  %s\n", daemon.Tr(daemon.ResolveLang(cfg.Language), "daemon.no_binding"))
		} else {
			return fmt.Errorf("%s", daemon.Tr(daemon.ResolveLang(cfg.Language), "daemon.no_binding"))
		}
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
	if cfg != nil {
		adapters := make(map[string]bool)
		for name, acfg := range cfg.IM.Adapters {
			adapters[name] = acfg.Enabled
		}
		imMgr.ApplyAdapterConfig(adapters)
	}

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
	bridge.SetHarnessConfig(cfg.Harness.AutoRunMode(), cfg.Harness.AutoInit, workingDir)

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

	// Start mobile tunnel if requested
	var tunnelSession *tunnel.Session
	var tunnelBroker *tunnel.Broker
	if startTunnel {
		tunnelSession = tunnel.NewSession()
		info, err := tunnelSession.Start(context.Background())
		if err != nil {
			fmt.Fprintf(os.Stderr, "tunnel failed: %v\n", err)
		} else {
			tunnelBroker = tunnel.NewBroker(tunnelSession)
			fmt.Fprintf(os.Stderr, "\n  Mobile Tunnel Active\n")
			fmt.Fprintf(os.Stderr, "  URL: %s\n\n", info.ConnectURL)
			fmt.Fprintf(os.Stderr, "%s\n", info.QRCode)
			// Subscribe daemon bridge events to tunnel broker
			bridge.Subscribe(func(ev provider.StreamEvent) {
				switch ev.Type {
				case provider.StreamEventText:
					tunnelBroker.PushText(tunnelBroker.NextMessageID(), ev.Text)
				case provider.StreamEventToolCallDone:
					name := ev.Tool.Name
					if name == "" {
						name = "tool"
					}
					tunnelBroker.PushToolCall(name, string(ev.Tool.Arguments), name)
				case provider.StreamEventToolResult:
					tunnelBroker.PushToolResult(ev.Tool.Name, ev.Result, ev.IsError)
				case provider.StreamEventDone:
					tunnelBroker.PushStatus(tunnel.StatusIdle, "")
				}
			})
			tunnelBroker.SendSessionInfo(tunnel.SessionInfoData{
				Workspace: workingDir,
				Model:     resolved.Model,
				Provider:  resolved.VendorName,
				Mode:      mode.String(),
				Version:   version.Version,
			})
			defer tunnelSession.Stop()
		}
	}

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
	mcpCtx, mcpCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer mcpCancel()
	for _, warning := range mcpMgr.ConnectAll(mcpCtx) {
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
	safego.Go("daemon.mcpOAuth.poll", func() {
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
			safego.Go("daemon.mcpOAuth.handle", func() {
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
			})
		}
	})
	safego.Go("daemon.mcpOAuth.retry", func() {
		for name := range mcpOAuthDone {
			mcpMgr.Retry(name)
		}
	})

	// Load project memory synchronously (daemon mode has no TUI event loop)
	if projectMemoryLoader != nil {
		content, memFiles, err := projectMemoryLoader()
		if err == nil && content != "" {
			ag.AddMessage(provider.Message{
				Role:    "system",
				Content: []provider.ContentBlock{{Type: "text", Text: "## Project Memory\n" + content}},
			})
		}
		if len(memFiles) > 0 {
			ag.SetProjectMemoryFiles(memFiles)
		}
	}

	// Start Knight background agent (if enabled)
	homeDir, _ := os.UserHomeDir()
	knightAgent = knight.New(cfg.Knight(), homeDir, workingDir, store)
	knightAgent.SetFactory(knightFactory)
	bridge.SetActivityHook(knightAgent.NotifyActivity)
	bridge.SetRestartHook(func() {
		daemonRestartRequested = true
		select {
		case restartCh <- struct{}{}:
		default:
		}
	})
	bridge.SetProviderSwitchHook(func(vendor, endpoint, model string) (string, error) {
		return daemonProviderSwitch(cfg, vendor, endpoint, model)
	})
	debug.Log("daemon", "Knight config: enabled=%v trust=%s budget=%d idle=%ds capabilities=%v",
		cfg.Knight().Enabled, cfg.Knight().TrustLevel, cfg.Knight().DailyTokenBudget,
		cfg.Knight().IdleDelaySec, cfg.Knight().Capabilities)
	if cfg.Knight().Enabled {
		// Create Knight emitter (reuse IM emitter)
		knightAgent.SetEmitter(emitter)
		// Wire Knight task events to stderr for follow display
		knightAgent.SetEventSink(&knight.FuncSink{
			OnStart: func(taskName string) {
				fmt.Fprintf(os.Stderr, "🌙 Knight: starting %s\r\n", taskName)
			},
			OnComplete: func(taskName string, report string, duration time.Duration) {
				suffix := ""
				if duration > 0 {
					suffix = fmt.Sprintf(" (%.0fs)", duration.Seconds())
				}
				// report may contain \n — convert to \r\n for raw terminal mode
				safeReport := strings.ReplaceAll(report, "\n", "\r\n")
				fmt.Fprintf(os.Stderr, "🌙 Knight %s completed%s — %s\r\n", taskName, suffix, safeReport)
			},
		})
		if err := knightAgent.Start(context.Background()); err != nil {
			if errors.Is(err, knight.ErrLockConflict) {
				pid, _ := knight.LockHeldBy(workingDir)
				fmt.Fprintf(os.Stderr, "🌙 %s\n", knight.FormatLockMessage(pid))
			} else {
				fmt.Fprintf(os.Stderr, "Knight startup warning: %v\n", err)
			}
		} else {
			defer knightAgent.Stop()
			if commandMgr.Reload() {
				refreshAgentSystemPrompt()
			} else {
				knightAgent.RecordSkillPromptExposure(currentPromptSkillRefs())
			}
			fmt.Fprintf(os.Stderr, "🌙 Knight started (budget: %dM tokens/day)\n", cfg.Knight().DailyTokenBudget/1_000_000)
		}
	}

	// Start A2A server if enabled.
	var a2aSrv *a2a.Server
	var a2aReg *a2a.Registry
	var a2aHandler *a2a.TaskHandler
	if !cfg.A2A.Disabled {
		// A2A instance override already applied by LoadWithInstance.
		a2aSrv, a2aReg, a2aHandler, err = startA2AServer(cfg, ag, registry, workingDir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "A2A server warning: %v\n", err)
		} else {
			defer func() {
				_ = a2aReg.Unregister()
				a2aSrv.Stop()
			}()
			fmt.Fprintf(os.Stderr, "🔗 A2A server: %s\n", a2aSrv.Endpoint())

			// Wire A2A task events to follow display + IM
			if a2aHandler != nil {
				a2aHandler.SetOnTaskEvent(func(msg a2a.TaskEventMessage) {
					fmt.Fprintf(os.Stderr, "  %s\r\n", msg.Message)
					if emitter != nil {
						emitter.EmitText(msg.Message)
					}
				})
			}
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
					if commandMgr.Reload() {
						refreshAgentSystemPrompt()
					}
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
		// Config is the source of truth for disabled state.
		configDisabledSet := map[string]bool{}
		if cfg != nil {
			for name, acfg := range cfg.IM.Adapters {
				if !acfg.Enabled {
					configDisabledSet[name] = true
				}
			}
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
				Muted: !configDisabledSet[b.Adapter] && mutedSet[b.Adapter], Disabled: configDisabledSet[b.Adapter] || disabledSet[b.Adapter], AllDirs: allDirsMap[b.Adapter],
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
				Muted: !configDisabledSet[adapter] && mutedSet[adapter], Disabled: configDisabledSet[adapter] || disabledSet[adapter], AllDirs: allDirsMap[adapter],
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
				Muted: !configDisabledSet[name] && mutedSet[name], Disabled: configDisabledSet[name] || disabledSet[name],
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
		case "unbind":
			for _, pb := range imMgr.AllPersistedBindings() {
				if pb.Adapter == adapter {
					return imMgr.DeleteBinding(adapter, pb.Workspace)
				}
			}
			return fmt.Errorf("no persisted binding for adapter %q", adapter)
		default:
			return fmt.Errorf("unknown action: %s", action)
		}
	})
	webuiSrv.SetRestartFn(func() {
		daemonRestartRequested = true
		select {
		case restartCh <- struct{}{}:
		default:
		}
	})
	webuiSrv.SetA2ADiscoverFn(func() []webui.A2ADiscoveredInstance {
		if a2aReg == nil {
			return nil
		}
		instances, err := a2aReg.Discover()
		if err != nil {
			return nil
		}
		var result []webui.A2ADiscoveredInstance
		for _, inst := range instances {
			result = append(result, webui.A2ADiscoveredInstance{
				ID:        inst.ID,
				Workspace: inst.Workspace,
				Endpoint:  inst.Endpoint,
				Status:    inst.Status,
				StartedAt: inst.StartedAt,
			})
		}
		return result
	})
	webuiSrv.SetKnightStatusFn(func() webui.KnightStatus {
		if knightAgent == nil {
			return webui.KnightStatus{Enabled: false, Status: "not initialized"}
		}
		used, remaining, limit := knightAgent.BudgetStatus()
		status := webui.KnightStatus{
			Enabled: true,
			Running: knightAgent.Running(),
			Status:  knightAgent.Status(),
			Budget: webui.KnightBudget{
				Used:      used,
				Remaining: remaining,
				Limit:     limit,
			},
		}
		if idx := knightAgent.Index(); idx != nil {
			if active, err := idx.ActiveSkills(); err == nil {
				for _, s := range active {
					status.Active = append(status.Active, webui.KnightSkill{
						Name:        s.Meta.Name,
						Description: s.Meta.Description,
						Scope:       s.Scope,
						CreatedBy:   s.Meta.CreatedBy,
						UsageCount:  s.Meta.UsageCount,
						Frozen:      s.Meta.Frozen,
						Platforms:   s.Meta.Platforms,
					})
				}
			}
			if staging, err := idx.StagingSkills(); err == nil {
				for _, s := range staging {
					status.Staging = append(status.Staging, webui.KnightSkill{
						Name:        s.Meta.Name,
						Description: s.Meta.Description,
						Scope:       s.Scope,
						Staging:     true,
						CreatedBy:   s.Meta.CreatedBy,
						UsageCount:  s.Meta.UsageCount,
						Frozen:      s.Meta.Frozen,
						Platforms:   s.Meta.Platforms,
					})
				}
			}
		}
		if q := knightAgent.Queue(); q != nil {
			if items, err := q.List(); err == nil {
				for _, c := range items {
					status.Queue = append(status.Queue, webui.KnightCandidate{
						Name:           c.Name,
						Description:    c.Description,
						Category:       c.Category,
						Score:          c.Score,
						EvidenceCount:  c.EvidenceCount,
						Reason:         c.Reason,
						SourceSessions: c.SourceSessions,
					})
				}
			}
		}
		return status
	})
	webuiSrv.SetKnightActionFn(func(action, skillName string, params map[string]interface{}) error {
		if knightAgent == nil {
			return fmt.Errorf("Knight not initialized")
		}
		switch action {
		case "promote":
			return knightAgent.PromoteStaging(skillName)
		case "reject":
			return knightAgent.RejectStaging(skillName)
		case "freeze":
			return knightAgent.SetSkillFrozen(skillName, true)
		case "unfreeze":
			return knightAgent.SetSkillFrozen(skillName, false)
		case "rollback":
			return knightAgent.RollbackSkill(skillName)
		case "record_effectiveness":
			score := 3
			if v, ok := params["score"]; ok {
				if f, ok := v.(float64); ok {
					score = int(f)
				}
			}
			knightAgent.RecordSkillEffectiveness(skillName, score)
			return nil
		case "analyze":
			return knightAgent.PerformSkillAnalysis(context.Background())
		case "validate":
			_, err := knightAgent.PerformSkillValidation(context.Background())
			return err
		case "delete_queue":
			if q := knightAgent.Queue(); q != nil {
				return q.Remove(knight.SkillCandidate{Name: skillName})
			}
			return fmt.Errorf("candidate queue not available")
		default:
			return fmt.Errorf("unknown action: %s", action)
		}
	})
	webuiSrv.SetKnightSkillContentFn(func(name string, staging bool) (string, error) {
		if knightAgent == nil {
			return "", fmt.Errorf("Knight not initialized")
		}
		var entry *knight.SkillEntry
		var err error
		if staging {
			entry, err = knightAgent.FindStagingSkill(name)
		} else {
			entry, err = knightAgent.FindActiveSkill(name)
		}
		if err != nil || entry == nil {
			return "", fmt.Errorf("skill %q not found", name)
		}
		data, err := os.ReadFile(entry.Path)
		if err != nil {
			return "", fmt.Errorf("read skill file: %w", err)
		}
		return string(data), nil
	})
	webuiSrv.SetSessionStore(store, workingDir)
	webuiSrv.SetChatBridge(bridge)
	webuiAddr := "127.0.0.1:0" // auto port
	actualAddr, webuiErr := webuiSrv.Start(webuiAddr)
	if webuiErr == nil {
		url := "http://" + actualAddr
		if tk := webuiSrv.Token(); tk != "" {
			url += "#token=" + tk
		}
		fmt.Fprintf(os.Stderr, "\x1b[34m\u2B21 WebUI:\x1b[0m \x1b[32m%s\x1b[0m\r\n", url)
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

	// Main loop
loop:
	for {
		select {
		case sig := <-sigCh:
			_ = sig
			break loop
		case <-restartCh:
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

		// If /restart debug was requested, inject GGCODE_DEBUG=1
		env := os.Environ()
		if bridge.ConsumeRestartDebug() {
			debug.Log("daemon", "restart with GGCODE_DEBUG=1")
			env = append(env, "GGCODE_DEBUG=1")
		}

		debug.Log("daemon", "restart exec %s", strings.Join(execArgs, " "))
		if runtime.GOOS == "windows" {
			// Windows doesn't support syscall.Exec — start new process and exit.
			restartCmd := exec.Command(binary, args...)
			restartCmd.Stdin = os.Stdin
			restartCmd.Stdout = os.Stdout
			restartCmd.Stderr = os.Stderr
			restartCmd.Env = env
			if err := restartCmd.Start(); err != nil {
				fmt.Fprintf(os.Stderr, "[ggcode restart] failed: %v\r\n", err)
				return nil
			}
			os.Exit(0)
		}
		return syscall.Exec(binary, execArgs, env)
	}

	fmt.Fprint(os.Stderr, daemon.Tr(lang, "daemon.shutting_down")+"\r\n")

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
		fmt.Fprintf(os.Stderr, "%s\r\n", daemon.Tr(lang, "daemon.bg_fail", err))
		return
	}
	fmt.Fprintf(os.Stderr, "%s\r\n", daemon.Tr(lang, "daemon.bg_ok", pid))
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

// daemonProviderSwitch handles /provider, /model, /config IM commands.
// All three params may be empty (show current config), or selectively set.
func daemonProviderSwitch(cfg *config.Config, vendor, endpoint, model string) (string, error) {
	// All empty → show current config
	if vendor == "" && endpoint == "" && model == "" {
		resolved, err := cfg.ResolveActiveEndpoint()
		if err != nil {
			return "", fmt.Errorf("failed to resolve config: %w", err)
		}
		vendors := make([]string, 0)
		for v := range cfg.Vendors {
			vendors = append(vendors, v)
		}
		endpoints := cfg.EndpointNames(cfg.Vendor)
		models := resolved.Models
		if len(models) == 0 && resolved.Model != "" {
			models = []string{resolved.Model}
		}
		return fmt.Sprintf(
			"📋 Current config:\n  Provider: %s (%s)\n  Model: %s\n  Available providers: %s\n  Endpoints: %s\n  Models: %s",
			resolved.VendorName, resolved.EndpointName, resolved.Model,
			strings.Join(vendors, ", "),
			strings.Join(endpoints, ", "),
			strings.Join(models, ", "),
		), nil
	}

	// Set the selection
	if err := cfg.SetActiveSelection(vendor, endpoint, model); err != nil {
		return "", err
	}

	resolved, err := cfg.ResolveActiveEndpoint()
	if err != nil {
		return "", fmt.Errorf("failed to resolve after switch: %w", err)
	}

	if model != "" {
		return fmt.Sprintf("✅ Model switched to: %s (%s)", resolved.Model, resolved.VendorName), nil
	}
	if vendor != "" {
		return fmt.Sprintf("✅ Provider switched to: %s (%s) → model: %s", resolved.VendorName, resolved.EndpointName, resolved.Model), nil
	}
	return fmt.Sprintf("✅ Config updated: %s (%s) → %s", resolved.VendorName, resolved.EndpointName, resolved.Model), nil
}
