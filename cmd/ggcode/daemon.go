package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/topcheer/ggcode/internal/a2a"
	"github.com/topcheer/ggcode/internal/agent"
	"github.com/topcheer/ggcode/internal/agentruntime"
	"github.com/topcheer/ggcode/internal/checkpoint"
	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/daemon"
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/im"
	"github.com/topcheer/ggcode/internal/knight"
	"github.com/topcheer/ggcode/internal/lanchat"
	"github.com/topcheer/ggcode/internal/mcp"
	"github.com/topcheer/ggcode/internal/permission"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/restart"
	"github.com/topcheer/ggcode/internal/runfile"
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
			newSession, _ := cmd.Flags().GetBool("new-session")
			if daemonized, _ := cmd.Flags().GetBool("__daemonized"); daemonized {
				noIM, _ := cmd.Flags().GetBool("__no-im")
				return runDaemon(cfg, resolvedCfg, bypassFlag, followFlag, resumeID, true, noIM, tunnelFlag, newSession)
			}

			// If --background, fork and exit parent
			if backgroundFlag {
				return startBackgroundDaemon(cfg, resolvedCfg, bypassFlag, resumeID, newSession)
			}

			// Normal foreground start
			noIM, _ := cmd.Flags().GetBool("__no-im")
			return runDaemon(cfg, resolvedCfg, bypassFlag, followFlag, resumeID, false, noIM, tunnelFlag, newSession)
		},
	}

	cmd.Flags().BoolVar(&bypassFlag, "bypass", false, "start in bypass permission mode (auto-approve safe ops)")
	cmd.Flags().BoolVar(&followFlag, "follow", false, "auto-enable follow mode")
	cmd.Flags().BoolVarP(&backgroundFlag, "background", "b", false, "start in background")
	cmd.Flags().BoolVar(&tunnelFlag, "tunnel", false, "start with mobile tunnel (QR code for GGCode Mobile)")
	cmd.Flags().StringVar(&resumeID, "resume", "", "resume a previous session by ID; use --resume-picker for interactive selection")
	cmd.Flags().Bool("resume-picker", false, "interactively select a session to resume")
	cmd.Flags().Bool("new-session", false, "skip auto-loading the most recent unlocked session; always start fresh")
	cmd.Flags().Bool("__daemonized", false, "internal: already daemonized")
	cmd.Flags().Bool("__no-im", false, "internal: skip IM binding check (A2A-only testing)")
	_ = cmd.Flags().MarkHidden("__daemonized")
	_ = cmd.Flags().MarkHidden("__no-im")
	cmd.MarkFlagsMutuallyExclusive("follow", "background")
	cmd.MarkFlagsMutuallyExclusive("resume", "resume-picker", "new-session")
	cmd.MarkFlagsMutuallyExclusive("resume", "resume-picker")
	return cmd
}

// startBackgroundDaemon forks the process into background and exits the parent.
func startBackgroundDaemon(cfg *config.Config, cfgFile string, bypass bool, resumeID string, newSession bool) error {
	workingDir, _ := os.Getwd()
	lang := daemon.ResolveLang(cfg.Language)

	extraArgs := []string{"--bypass=" + fmt.Sprintf("%v", bypass)}
	if resumeID != "" {
		extraArgs = append(extraArgs, "--resume="+resumeID)
	}
	if newSession {
		extraArgs = append(extraArgs, "--new-session")
	}
	pid, err := daemon.ForkIntoBackground(cfgFile, workingDir, "", extraArgs...)
	if err != nil {
		return fmt.Errorf("starting background daemon: %w", err)
	}
	fmt.Fprintf(os.Stderr, "%s\n", daemon.Tr(lang, "daemon.started_bg", pid))
	fmt.Fprintf(os.Stderr, "%s\n", daemon.Tr(lang, "daemon.workdir", workingDir))
	return nil
}

func runDaemon(cfg *config.Config, cfgFile string, bypass bool, followActive bool, resumeID string, _ bool, noIM bool, startTunnel bool, newSession bool) error {
	// --- Steps 1-8: same as run() in root.go ---

	prov, resolved, err := ResolveProvider(cfg)
	if err != nil {
		return err
	}
	_, knightProv, err := resolveKnightProvider(cfg, resolved, prov)
	if err != nil {
		return err
	}

	workingDir, _ := os.Getwd()
	mode := agentruntime.InteractivePermissionMode(cfg, bypass)
	policy := agentruntime.BuildInteractivePermissionPolicy(cfg, workingDir, bypass)

	// Tools
	var ag *agent.Agent
	core, err := agentruntime.BuildInteractiveRuntimeCore(cfg, workingDir, policy)
	if err != nil {
		return err
	}
	registry := core.Registry
	mcpMgr := core.MCPManager
	autoMem := core.AutoMemory
	projectAutoMem := core.ProjectAutoMem
	saveMemoryTool := core.SaveMemoryTool
	startupAssets := core.StartupAssets
	commandMgr := startupAssets.CommandManager
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

	skillTool := agentruntime.NewSkillTool(commandMgr, mcpMgr, prov, registry, skillAgentFactory, workingDir, nil, nil)
	skillTool.OnSkillUsed = func(ref string) {
		if knightAgent != nil {
			knightAgent.RecordSkillUse(ref)
		}
	}
	skillTool.OnSkillCompleted = func(event tool.SkillExecutionEvent) {
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
	}
	// NOTE: skillTool registration deferred until after buildCurrentSystemPrompt is defined
	var subMgr *subagent.Manager
	acpClientMgr := agentruntime.NewACPClientManager(workingDir, policy, func(_ context.Context, _ string, _ string) permission.Decision {
		return permission.Allow
	})
	agentruntime.RegisterDelegateTool(registry, acpClientMgr, func() *subagent.Manager { return subMgr }, workingDir, func() string {
		if ag != nil {
			return ag.WorkingDir()
		}
		return workingDir
	})
	if len(acpClientMgr.Available()) > 0 {
		defer acpClientMgr.CloseAll()
	}

	// System prompt
	gitStatus := detectGitStatus(workingDir)
	tools := registry.List()
	toolNames := make([]string, len(tools))
	for i, t := range tools {
		toolNames[i] = t.Name()
	}
	// Start the background section collector (same pattern as root.go).
	agentruntime.InitGlobalSectionCollector(workingDir)
	defer agentruntime.StopGlobalSectionCollector()

	// Declare early so buildCurrentSystemPrompt closure can reference it.
	var a2aReg *a2a.Registry

	buildCurrentSystemPrompt := func() (string, []string) {
		remoteAgentsInfo := ""
		if a2aReg != nil {
			if instances := a2aReg.CachedInstances(); len(instances) > 1 {
				remoteAgentsInfo = a2a.FormatRemoteAgents(instances, nil)
			}
		}
		return agentruntime.BuildInteractiveSystemPromptWithPromptRefs(cfg, workingDir, mode, registry, commandMgr, autoMem, projectAutoMem, gitStatus, remoteAgentsInfo)
	}
	systemPrompt, promptSkillRefs := buildCurrentSystemPrompt()

	// Wire sub-agent system prompt builder into skillTool (same pattern as root.go)
	skillTool.SystemPromptBuilder = func(task, agentType string) string {
		remoteAgentsInfo := ""
		if a2aReg != nil {
			if instances := a2aReg.CachedInstances(); len(instances) > 0 {
				remoteAgentsInfo = a2a.FormatRemoteAgents(instances, nil)
			}
		}
		return agentruntime.BuildSubAgentSystemPrompt(agentruntime.SubAgentPromptContext{
			Cfg:              cfg,
			WorkingDir:       workingDir,
			Registry:         registry,
			CommandMgr:       commandMgr,
			GlobalAutoMem:    autoMem,
			ProjectAutoMem:   projectAutoMem,
			GitStatus:        func() string { return detectGitStatus(workingDir) },
			RemoteAgentsInfo: func() string { return remoteAgentsInfo },
		}, task, agentType)
	}
	_ = registry.Register(skillTool)

	var promptSkillRefsMu sync.RWMutex
	currentPromptSkillRefs := func() []string {
		promptSkillRefsMu.RLock()
		defer promptSkillRefsMu.RUnlock()
		return append([]string(nil), promptSkillRefs...)
	}

	// Agent
	ag = agent.NewAgent(prov, registry, systemPrompt, cfg.MaxIterations)
	if rTool, ok := registry.Get("runtime"); ok {
		if rt, ok := rTool.(tool.RuntimeTool); ok {
			if dp, ok := rt.Provider.(*daemonRuntimeProvider); ok {
				dp.agent = ag
			}
		}
	}
	core.SetConfigAgent(ag)
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
	saveMemoryTool.SetAfterSave(refreshAgentSystemPrompt)
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
	setupDaemonReflection(ag, workingDir)
	agentruntime.ApplyResolvedLimitsToAgent(ag, resolved)
	agentruntime.StartAsyncRelayModelLimitRefresh(cfg, resolved, ag, nil)
	ag.SetProbeKey(provider.MakeProbeKey(resolved.VendorID, resolved.BaseURL, resolved.Model))
	ag.SetPermissionPolicy(policy)
	ag.SetHookConfig(cfg.Hooks)
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

	// Clean up stale lock files from crashed/killed processes.
	storeDir, _ := session.DefaultDir()
	session.CleanupStaleLocks(storeDir)

	var daemonRestartRequested bool
	restartCh := make(chan struct{}, 1)

	// IM Manager
	adapters := make(map[string]bool)
	if cfg != nil {
		for name, acfg := range cfg.IM.Adapters {
			adapters[name] = acfg.Enabled
		}
	}
	runtimeInit, err := im.InitRuntime(im.RuntimeInitOptions{
		Workspace:        workingDir,
		EnabledAdapters:  adapters,
		RegisterInstance: false, // Deferred to line 606 where session ID is available
	})
	if err != nil {
		return fmt.Errorf("initializing IM runtime: %w", err)
	}
	imMgr := runtimeInit.Manager
	bindingStore := runtimeInit.BindingStore

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

	// Determine language
	lang := daemon.ResolveLang(cfg.Language)

	// Create or resume session
	var ses *session.Session
	var sessionLock *session.SessionLock
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
		// Restore messages to agent (includes reconcile + microcompact).
		compacted, beforeTokens, afterTokens := agentruntime.RestoreSessionIntoAgent(ag, ses)
		if compacted {
			fmt.Fprintf(os.Stderr, "Restored session was oversized (%d tokens), truncated to %d tokens to fit context window\n", beforeTokens, afterTokens)
		}
		// Restore session-scoped permission mode (if set).
		if ses.PermissionMode != "" {
			sessionMode := permission.ParsePermissionMode(ses.PermissionMode)
			if cp, ok := policy.(*permission.ConfigPolicy); ok {
				cp.SetMode(sessionMode)
			}
			mode = sessionMode
			debug.Log("daemon", "restored permission mode %s from session", sessionMode)
		}
	} else {
		// Auto-load: find the most recent unlocked workspace session.
		// Skip if --new-session flag was passed.
		if !newSession {
			workspace := workingDir
			sessions, err := store.ListForWorkspace(workspace)
			if err != nil {
				debug.Log("daemon", "ListForWorkspace error: %v", err)
			}
			for _, s := range sessions {
				lock, lockErr := session.TryAcquireSessionLock(storeDir, s.ID)
				if lockErr == nil && lock != nil && lock.Acquired() {
					existing, loadErr := store.Load(s.ID)
					if loadErr != nil {
						lock.Release()
						continue
					}
					ses = existing
					resumeID = s.ID
					sessionLock = lock
					agentruntime.RestoreSessionIntoAgent(ag, ses)
					debug.Log("daemon", "auto-loaded session %s", s.ID)
					break
				}
			}
		}
		if ses == nil {
			vendor := cfg.Vendor
			endpoint := cfg.Endpoint
			modelName := cfg.Model
			ses = session.NewSession(vendor, endpoint, modelName)
		}
	}
	if err := store.Save(ses); err != nil {
		return fmt.Errorf("saving session: %w", err)
	}
	ag.SetSessionID(ses.ID)

	// Acquire session lock to prevent concurrent instances on the same session.
	// If auto-load or --resume already acquired a lock for this session,
	// it is stored in autoLock and we skip the second TryAcquireSessionLock.
	if sessionLock == nil && ses.ID != "" {
		lock, lockErr := session.TryAcquireSessionLock(storeDir, ses.ID)
		if lockErr == nil && lock != nil && lock.Acquired() {
			sessionLock = lock
		} else if lock != nil && !lock.Acquired() {
			pid := lock.HolderPID()
			return fmt.Errorf("session %s is locked by another instance (PID %d)", ses.ID[:8], pid)
		}
	}
	// Ensure session lock is released on ANY exit path (e.g., IM adapter
	// start failure at line ~696). Explicit releases below set sessionLock
	// to nil, so this defer is a no-op on normal/restart exit paths.
	defer func() {
		if sessionLock != nil {
			sessionLock.Release()
		}
	}()

	// Create emitter and daemon bridge
	emitter := im.NewIMEmitter(imMgr, string(lang), workingDir)
	if cfg.IM.OutputMode != "" {
		emitter.SetOutputMode(cfg.IM.OutputMode)
	}
	bridge := im.NewDaemonBridge(imMgr, ag, emitter, store, ses)
	defer bridge.Close()

	// Bind tunnel host to session for projection recording
	core.Tunnel.BindSession(ses, store)

	bridge.SetHarnessConfig(cfg.Harness.AutoRunMode(), cfg.Harness.AutoInit, workingDir)

	// Wire checkpoint handler — persist compacted state after summarize
	ag.SetCheckpointHandler(func(summaryMsgID, lastMsgID string, tokenCount int) {
		if err := store.AppendCheckpoint(ses, summaryMsgID, lastMsgID, tokenCount); err != nil {
			debug.Log("daemon", "checkpoint save failed: %v", err)
		} else {
			debug.Log("daemon", "checkpoint saved: summary_msg_id=%s last_msg_id=%s tokens=%d", summaryMsgID, lastMsgID, tokenCount)
		}
	})

	// Per-message persistence: every Add() triggers async JSONL append.
	ag.SetPersistHandler(func(msg provider.Message) {
		if err := store.AppendMessageToDisk(ses, msg); err != nil {
			debug.Log("daemon", "persist handler: AppendMessageToDisk failed: %v", err)
		}
	})

	// Wire ask_user handler
	if tl, ok := registry.Get("ask_user"); ok {
		if askTool, ok := tl.(*tool.AskUserTool); ok {
			askTool.SetHandler(bridge.HandleAskUser)
		}
	}

	// Wire switch_mode tool to persist mode changes to session metadata
	if sm, ok := registry.Get("switch_mode"); ok {
		if smt, ok := sm.(*tool.SwitchModeTool); ok {
			if cp, ok := policy.(*permission.ConfigPolicy); ok {
				smt.SetSwitcher(&daemonModeSwitcher{policy: cp, ses: ses, store: store})
			}
		}
	}

	// Wire IM tool to the runtime manager
	if imt, ok := registry.Get("im"); ok {
		if imTool, ok := imt.(tool.IMTool); ok {
			imTool.Manager = im.NewToolManagerAdapter(imMgr)
			registry.Unregister("im")
			registry.Register(imTool)
		}
	}

	// Wire runtime tool to daemon's runtime status
	if rt, ok := registry.Get("runtime"); ok {
		if rTool, ok := rt.(tool.RuntimeTool); ok {
			rTool.Provider = &daemonRuntimeProvider{
				ses:    ses,
				cfg:    cfg,
				imMgr:  imMgr,
				bridge: bridge,
			}
			registry.Unregister("runtime")
			registry.Register(rTool)
		}
	}

	// Cron tools — enqueue fires the prompt as a user message via the
	// daemon bridge. If queue_if_busy=false (default) and agent is busy,
	// skip the firing instead of interrupting.
	cronScheduler := agentruntime.NewSessionCronScheduler(ses.ID, workingDir, func(prompt string, queueIfBusy bool) {
		if !queueIfBusy && bridge.HasActiveRun() {
			debug.Log("daemon", "[cron] skipping prompt (agent busy, queue_if_busy=false): %s", prompt)
			return
		}
		debug.Log("daemon", "[cron] firing prompt (queue_if_busy=%v): %s", queueIfBusy, prompt)
		bridge.SendUserMessage([]provider.ContentBlock{
			{Type: "text", Text: prompt},
		})
	})
	agentruntime.RegisterCronTools(registry, cronScheduler)

	// Sub-agent manager
	subMgr = subagent.NewManager(cfg.SubAgents)
	defer subMgr.Shutdown()

	// Wire cascade-cancel so daemon interrupts cascade to sub-agents/delegates
	// (mirrors TUI cancelActiveRun and desktop ChatBridge.Cancel).
	bridge.SetCascadeCancel(func() {
		subMgr.CancelAll()
	})

	// Create follow display (always, so the pairing watcher can use it)
	toolLang := im.ToolLangEn
	if lang == daemon.LangZhCN {
		toolLang = im.ToolLangZhCN
	}
	toolPresenter := &daemonToolPresenter{lang: toolLang}
	followDisplay := daemon.NewTerminalFollowDisplay(os.Stderr, lang, workingDir, toolPresenter)

	// Set bridge on manager
	imMgr.SetBridge(bridge)

	// Track agent busy state for /api/status
	var agentBusy atomic.Bool
	bridge.SetRunStateHook(func(busy bool) { agentBusy.Store(busy) })

	// Mobile connection status callback (set when tunnel connects)
	statusMu := &sync.Mutex{}
	var mobileConnectedFn func() (bool, string)

	// Start mobile tunnel if requested
	var tunnelSession *tunnel.Session
	if startTunnel {
		sessionInfo := tunnel.SessionInfoData{
			Workspace: workingDir,
			Model:     resolved.Model,
			Provider:  resolved.VendorName,
			Mode:      mode.String(),
			Version:   version.Version,
		}
		var shareResult *agentruntime.ShareResult
		result, err := core.Tunnel.StartShare(agentruntime.ShareConfig{
			Workspace: workingDir,
			Model:     resolved.Model,
			Provider:  resolved.VendorName,
			Mode:      mode.String(),
			Version:   version.Version,
			ClientTag: "daemon",
			SnapshotProvider: func() tunnel.BrokerSnapshot {
				return daemonSnapshot(bridge, workingDir, resolved, mode.String())
			},
			OnCommand: func(cmd tunnel.GatewayMessage) {
				var b *tunnel.Broker
				if shareResult != nil {
					b = shareResult.Broker
				}
				ctrl := newDaemonTunnelShareController(b, bridge, sessionInfo, core.Tunnel)
				ctrl.HandleCommand(bridge, cmd)
			},
			OnConnected: func(info tunnel.RelayConnectedState) {
				if info.Role == "client" {
					fmt.Fprintf(os.Stderr, "\r\n  [tunnel] Mobile connected\r\n")
				}
			},
		})
		shareResult = result
		if err != nil {
			fmt.Fprintf(os.Stderr, "tunnel failed: %v\n", err)
		} else {
			tunnelSession = result.Session

			// Wire hooks for status/activity push
			ctrl := newDaemonTunnelShareController(result.Broker, bridge, sessionInfo, core.Tunnel)
			bridge.SetRunStateHook(func(busy bool) {
				agentBusy.Store(busy)
				ctrl.HandleRunState(busy)
			})
			bridge.SetUserMessageHook(ctrl.HandleUserMessage)
			unsubscribeTunnel := bridge.Subscribe(ctrl.HandleStreamEvent)
			defer unsubscribeTunnel()
			defer bridge.SetRunStateHook(nil)
			defer bridge.SetUserMessageHook(nil)
			// Wire mobile connection status for /api/status
			statusMu.Lock()
			mobileConnectedFn = func() (bool, string) {
				sid := result.Broker.SessionID()
				return sid != "", sid
			}
			statusMu.Unlock()

			fmt.Fprintf(os.Stderr, "\n  Mobile Tunnel Active\n")
			fmt.Fprintf(os.Stderr, "  URL: %s\n\n", result.ConnectURL)
			fmt.Fprintf(os.Stderr, "%s\n", result.QRCode)
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

	// Bind session to IM manager so that session-scoped binding ownership
	// works correctly. InitRuntime binds with an empty SessionID; we need
	// to rebind with the real session ID before RegisterInstance.
	imMgr.BindSession(im.SessionBinding{
		SessionID: ses.ID,
		Workspace: workingDir,
	})

	// Register this instance for multi-instance detection BEFORE starting adapters.
	// This ensures session-scoped binding ownership (claimUnclaimedBindings +
	// muteNonOwnedBindings) runs before StartCurrentBindingAdapter so only
	// bindings owned by this session are started.
	_, others, err := imMgr.RegisterInstance(workingDir, ses.ID)
	if err != nil {
		debug.Log("daemon", "instance detect register failed: %v", err)
	} else if len(others) > 0 {
		primary := others[0]
		fmt.Fprintf(os.Stderr, "Auto-muted IM channels — primary instance (PID %d, started %s)\n",
			primary.PID, primary.StartedAt.Format("15:04"))
	}
	defer imMgr.UnregisterInstance()

	// Start adapters (only non-muted bindings will start)
	if cfg.IM.Enabled {
		controller, err := im.StartCurrentBindingAdapter(context.Background(), cfg.IM, imMgr)
		if err != nil {
			return fmt.Errorf("starting IM adapter: %w", err)
		}
		defer controller.Stop()
	}

	// Start background services (MCP connections, etc.)
	if mcpMgr := core.MCPManager; mcpMgr != nil {
		// Daemon follow mode: show MCP OAuth URLs in terminal
		mcpMgr.SetURLOpener(func(url string) error {
			fmt.Fprintf(os.Stderr, "\x1b[34m\u2b21 MCP OAuth:\x1b[0m opening browser %s\r\n", url)
			return nil
		})
	}
	core.StartBackgroundServices()
	defer core.Close()

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
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
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
	_, _ = agentruntime.ApplyProjectMemoryToAgent(ag, workingDir)

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
		resolved, prov, err := agentruntime.ActivateCurrentSelection(cfg, vendor, endpoint, model)
		if err != nil {
			return "", err
		}
		agentruntime.ApplyProviderToAgent(ag, prov, resolved)
		agentruntime.StartAsyncRelayModelLimitRefresh(cfg, resolved, ag, nil)
		if ses != nil {
			ses.Vendor = cfg.Vendor
			ses.Endpoint = cfg.Endpoint
			ses.Model = resolved.Model
			// Persist model selection to session JSONL (session-scoped).
			if err := store.AppendMetaToDisk(ses); err != nil {
				debug.Log("daemon", "model switch: meta persist failed: %v", err)
			}
		}
		// Sync vendor/endpoint definitions to global config.
		agentruntime.SyncVendorEndpointToGlobal(cfg, cfg.Vendor, cfg.Endpoint)
		if vendor == "" && endpoint == "" && model == "" {
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
		if model != "" {
			return fmt.Sprintf("✅ Model switched to: %s (%s)", resolved.Model, resolved.VendorName), nil
		}
		if vendor != "" {
			return fmt.Sprintf("✅ Provider switched to: %s (%s) → model: %s", resolved.VendorName, resolved.EndpointName, resolved.Model), nil
		}
		return fmt.Sprintf("✅ Config updated: %s (%s) → %s", resolved.VendorName, resolved.EndpointName, resolved.Model), nil
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
	} else {
		fmt.Fprintf(os.Stderr, "Knight is disabled. Use /knight on to enable.\n")
	}

	// Start A2A server if enabled.
	var a2aSrv *a2a.Server
	// a2aReg already declared above for system prompt access
	var a2aHandler *a2a.TaskHandler
	var lanchatHub *lanchat.Hub
	if !cfg.A2A.Disabled {
		// A2A instance override already applied by LoadWithInstance.
		a2aSrv, a2aReg, a2aHandler, err = startA2AServer(cfg, ag, registry, workingDir)
		if err != nil {
			debug.Log("daemon", "A2A server start failed: %v", err)
			fmt.Fprintf(os.Stderr, "A2A server warning: %v\n", err)
		} else {
			a2aBgCtx, a2aBgCancel := context.WithCancel(context.Background())
			a2aReg.StartBackgroundRefresh(a2aBgCtx)
			defer func() {
				a2aBgCancel()
				_ = a2aReg.Unregister()
				if lanchatHub != nil {
					lanchatHub.Close()
				}
				a2aSrv.Stop()
			}()
			fmt.Fprintf(os.Stderr, "🔗 A2A server: %s\n", a2aSrv.Endpoint())

			// Create lanchat Hub for LAN peer discovery and messaging.
			chatStore := lanchat.NewStore(filepath.Join(config.ConfigDir(), "lanchat"))
			lanchatHub = lanchat.NewHub(
				a2aReg.SelfID(),
				"daemon",
				a2aSrv.Endpoint(),
				cfg.A2A.EffectiveAPIKey(),
				chatStore,
				lanchat.DetectWorkspaceMeta(workingDir),
			)
			lanchatHub.SetAttachments(lanchat.NewAttachmentManager())
			lanchat.MountHandlers(a2aSrv.Mux(), lanchatHub, a2aSrv.Port())

			// Model health reporting: advertise current model, classify run
			// failures into degraded status, and probe recovery in the
			// background (daemon has no user watching errors, so the probe
			// loop is the primary recovery signal here).
			lanchatHub.SetModel(cfg.Model)
			lanchatHub.SetHealthProber(ag.HealthCheck)
			ag.SetRunHealthHandler(func(runErr error) {
				if lanchatHub == nil {
					return
				}
				if runErr == nil {
					lanchatHub.ReportAgentSuccess()
				} else {
					lanchatHub.ReportAgentFailure(provider.ClassifyLLMError(runErr))
				}
			})

			// Sync peers from A2A registry
			safego.Go("daemon.lanchat.syncPeers", func() {
				syncPeers := func() {
					instances := a2aReg.CachedInstances()
					if instances == nil {
						return
					}
					peers := make([]lanchat.Participant, 0, len(instances))
					for _, inst := range instances {
						peers = append(peers, lanchat.Participant{
							NodeID:   inst.ID,
							Endpoint: inst.Endpoint,
						})
					}
					lanchatHub.UpdatePeers(peers)
				}
				select {
				case <-time.After(3 * time.Second):
					syncPeers()
				case <-a2aBgCtx.Done():
					return
				}
				ticker := time.NewTicker(10 * time.Second)
				defer ticker.Stop()
				for {
					select {
					case <-ticker.C:
						syncPeers()
					case <-a2aBgCtx.Done():
						return
					}
				}
			})

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
		instances := a2aReg.CachedInstances()
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
		// Write port file for external process discovery
		runfile.Write(runfile.PortFile{
			Addr:      actualAddr,
			Token:     webuiSrv.Token(),
			PID:       os.Getpid(),
			SessionID: ses.ID,
			Workspace: workingDir,
			Mode:      cfg.DefaultMode,
		})
		defer runfile.Remove(ses.ID)
	} else {
		fmt.Fprintf(os.Stderr, "WebUI start failed: %v\r\n", webuiErr)
	}
	defer webuiSrv.Close()

	// Expose runtime status via /api/status
	webuiSrv.SetStatusFn(func() webui.RuntimeStatus {
		st := webui.RuntimeStatus{
			PID:            os.Getpid(),
			Workspace:      workingDir,
			AgentBusy:      agentBusy.Load(),
			PermissionMode: policy.Mode().String(),
		}
		if cfg != nil {
			st.Vendor = cfg.Vendor
			st.Endpoint = cfg.Endpoint
			st.Model = cfg.Model
			st.Language = cfg.Language
		}
		if ag != nil && ag.ContextManager() != nil {
			st.ContextWindow = ag.ContextManager().ContextWindow()
			st.MaxTokens = ag.ContextManager().OutputReserve()
		}
		// IM adapters
		if imMgr != nil {
			snap := imMgr.Snapshot()
			for _, a := range snap.Adapters {
				st.IMAdapters = append(st.IMAdapters, webui.IMAdapterInfo{
					Name:    a.Name,
					Type:    string(a.Platform),
					Online:  a.Healthy,
					Channel: a.ContactURI,
				})
			}
		}
		// Mobile tunnel (set when tunnel connects)
		statusMu.Lock()
		if mobileConnectedFn != nil {
			connected, sid := mobileConnectedFn()
			st.MobileConn.Connected = connected
			st.MobileConn.SessionID = sid
		}
		statusMu.Unlock()
		return st
	})

	// Signal handling
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	// Keyboard goroutine (only if stdin is a terminal)
	kbCh := make(chan byte, 8)
	var restoreTerminal func()
	if isTerminal {
		restoreTerminal = readKeyboard(kbCh)
	}

	// Register PID file for cleanup (skip if detaching to background —
	// the forked child owns the PID file after detach).
	detachedToBackground := false
	defer func() {
		if !detachedToBackground {
			daemon.CleanupDaemon(workingDir)
		}
	}()

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
				detachedToBackground = true
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
			case 't': // toggle mobile tunnel share
				if core.Tunnel.GetShareInfo() != nil {
					// Already active: re-print QR code
					info := core.Tunnel.GetShareInfo()
					fmt.Fprintf(os.Stderr, "\r\n  Mobile Tunnel Active\r\n")
					fmt.Fprintf(os.Stderr, "  URL: %s\r\n\r\n", info.ConnectURL)
					fmt.Fprintf(os.Stderr, "%s\r\n", strings.ReplaceAll(info.QRCode, "\n", "\r\n"))
				} else {
					// Create share controller once (before StartShare so OnCommand can use it)
					sessionInfo := tunnel.SessionInfoData{
						Workspace: workingDir,
						Model:     resolved.Model,
						Provider:  resolved.VendorName,
						Mode:      mode.String(),
						Version:   version.Version,
					}
					// Forward-declare broker ref so OnCommand closure can use it
					var shareResult *agentruntime.ShareResult
					// Start tunnel via unified StartShare
					result, err := core.Tunnel.StartShare(agentruntime.ShareConfig{
						Workspace: workingDir,
						Model:     resolved.Model,
						Provider:  resolved.VendorName,
						Mode:      mode.String(),
						Version:   version.Version,
						ClientTag: "daemon",
						SnapshotProvider: func() tunnel.BrokerSnapshot {
							return daemonSnapshot(bridge, workingDir, resolved, mode.String())
						},
						OnCommand: func(cmd tunnel.GatewayMessage) {
							// Route inbound commands through a controller wired to the share broker
							var broker *tunnel.Broker
							if shareResult != nil {
								broker = shareResult.Broker
							}
							ctrl := newDaemonTunnelShareController(broker, bridge, sessionInfo, core.Tunnel)
							ctrl.HandleCommand(bridge, cmd)
						},
						OnConnected: func(info tunnel.RelayConnectedState) {
							if info.Role == "client" {
								fmt.Fprintf(os.Stderr, "\r\n  [tunnel] Mobile connected\r\n")
							}
						},
					})
					shareResult = result
					if err != nil {
						debug.Log("daemon", "tunnel share failed: %v", err)
						fmt.Fprintf(os.Stderr, "\r\n  Tunnel failed: %v\r\n", err)
					} else {
						// Wire hooks for status/activity push
						ctrl := newDaemonTunnelShareController(result.Broker, bridge, sessionInfo, core.Tunnel)
						bridge.SetRunStateHook(func(busy bool) {
							agentBusy.Store(busy)
							ctrl.HandleRunState(busy)
						})
						bridge.SetUserMessageHook(ctrl.HandleUserMessage)
						unsubscribeTunnel := bridge.Subscribe(ctrl.HandleStreamEvent)
						_ = unsubscribeTunnel // cleaned up on daemon exit

						fmt.Fprintf(os.Stderr, "\r\n  Mobile Tunnel Active\r\n")
						fmt.Fprintf(os.Stderr, "  URL: %s\r\n\r\n", result.ConnectURL)
						fmt.Fprintf(os.Stderr, "%s\r\n", strings.ReplaceAll(result.QRCode, "\n", "\r\n"))
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
		// Self-restart via in-place syscall.Exec — no child process needed.
		binary, err := restart.ResolveBinary()
		if err != nil {
			return fmt.Errorf("restart: resolve binary: %w", err)
		}
		bridge.Close()
		if lanchatHub != nil {
			lanchatHub.Close()
		}
		// ⚠️ Do NOT do ses.Messages = ag.Messages() + store.Save(ses).
		// That would rewrite the JSONL with compacted messages and destroy
		// pre-compaction history. appendAssistantMessages already appended
		// new messages incrementally during the session. Just update meta.
		_ = store.AppendMetaToDisk(ses)

		// Release the session lock before exec — the new process will acquire it fresh.
		// syscall.Exec preserves FDs with flocks, so without releasing here the
		// new process would fail to acquire its own lock on the same session.
		if sessionLock != nil {
			sessionLock.Release()
			sessionLock = nil
		}

		// Clean up port file before exec — syscall.Exec skips defers.
		runfile.Remove(ses.ID)

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

		// If /restart debug was requested, inject GGCODE_DEBUG=1
		env := os.Environ()
		if bridge.ConsumeRestartDebug() {
			debug.Log("daemon", "restart with GGCODE_DEBUG=1")
			env = append(env, "GGCODE_DEBUG=1")
		}

		debug.Log("daemon", "exec restart: %s %v", binary, args)
		if err := restart.ExecRestart(binary, args, env); err != nil {
			fmt.Fprintf(os.Stderr, "[ggcode restart] failed: %v\r\n", err)
		}
		return nil
	}

	fmt.Fprint(os.Stderr, daemon.Tr(lang, "daemon.shutting_down")+"\r\n")

	// Save session on exit
	bridge.Close()
	// ⚠️ Do NOT do ses.Messages = ag.Messages() + store.Save(ses).
	// That would rewrite the JSONL with compacted messages and destroy
	// pre-compaction history. appendAssistantMessages already appended
	// new messages incrementally during the session. Just update meta.
	if err := store.AppendMetaToDisk(ses); err != nil {
		debug.Log("daemon", "exit: meta persist failed: %v", err)
	}

	// Clean up empty session files on exit.
	if err := store.CleanupIfEmpty(ses); err != nil {
		debug.Log("daemon", "exit cleanup: failed to delete empty session: %v", err)
	}

	// Release session lock on exit
	if sessionLock != nil {
		sessionLock.Release()
		sessionLock = nil
	}

	fmt.Fprintln(os.Stderr, daemon.Tr(lang, "daemon.stopped"))
	return nil
}
