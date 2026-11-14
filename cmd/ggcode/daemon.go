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
	"github.com/topcheer/ggcode/internal/qrcode"
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
	var bypassFlag, followFlag, backgroundFlag, tunnelFlag, fullLoadFlag, etunnelFlag bool
	var resumeID string

	cmd := &cobra.Command{
		Use:   "daemon",
		Short: "Run ggcode in daemon mode, controlled via IM",
		Long:  "Run ggcode as a headless daemon. Messages from paired IM channels are forwarded to the agent, and responses are sent back via IM. Requires at least one IM channel bound to the current workspace.",
		RunE: func(cmd *cobra.Command, args []string) error {

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
			if persistWarnings, _, err := mcp.PersistUserClaudeServers(cfg); err != nil {
				return fmt.Errorf("persisting Claude MCP servers: %w", err)
			} else {
				for _, w := range persistWarnings {
					fmt.Fprintln(os.Stderr, "warning:", w)
				}
			}

			newSession, _ := cmd.Flags().GetBool("new-session")
			if daemonized, _ := cmd.Flags().GetBool("__daemonized"); daemonized {
				noIM, _ := cmd.Flags().GetBool("__no-im")
				return runDaemon(cfg, resolvedCfg, bypassFlag, followFlag, resumeID, true, noIM, tunnelFlag, newSession, fullLoadFlag, etunnelFlag)
			}

			if backgroundFlag {
				return startBackgroundDaemon(cfg, resolvedCfg, bypassFlag, resumeID, newSession, fullLoadFlag)
			}

			noIM, _ := cmd.Flags().GetBool("__no-im")
			return runDaemon(cfg, resolvedCfg, bypassFlag, followFlag, resumeID, false, noIM, tunnelFlag, newSession, fullLoadFlag, etunnelFlag)
		},
	}

	cmd.Flags().BoolVar(&bypassFlag, "bypass", false, "start in bypass permission mode (auto-approve safe ops)")
	cmd.Flags().BoolVar(&followFlag, "follow", false, "auto-enable follow mode")
	cmd.Flags().BoolVar(&etunnelFlag, "etunnel", false, "auto-start the et WebUI tunnel after init and print URL + QR (works with --follow)")
	cmd.Flags().BoolVarP(&backgroundFlag, "background", "b", false, "start in background")
	cmd.Flags().BoolVar(&tunnelFlag, "tunnel", false, "start with mobile tunnel (QR code for GGCode Mobile)")
	cmd.Flags().StringVar(&resumeID, "resume", "", "resume a previous session by ID; use --resume-picker for interactive selection")
	cmd.Flags().Bool("resume-picker", false, "interactively select a session to resume")
	cmd.Flags().Bool("new-session", false, "skip auto-loading the most recent unlocked session; always start fresh")
	cmd.Flags().BoolVar(&fullLoadFlag, "full", false, "load all messages from the session file (default: only last 24h for rendering)")
	cmd.Flags().Bool("__daemonized", false, "internal: already daemonized")
	cmd.Flags().Bool("__no-im", false, "internal: skip IM binding check (A2A-only testing)")
	_ = cmd.Flags().MarkHidden("__daemonized")
	_ = cmd.Flags().MarkHidden("__no-im")
	cmd.MarkFlagsMutuallyExclusive("follow", "background")
	cmd.MarkFlagsMutuallyExclusive("resume", "resume-picker", "new-session")
	cmd.MarkFlagsMutuallyExclusive("resume", "resume-picker")
	return cmd
}

func startBackgroundDaemon(cfg *config.Config, cfgFile string, bypass bool, resumeID string, newSession bool, fullLoad bool) error {
	workingDir, _ := os.Getwd()
	lang := daemon.ResolveLang(cfg.Language)

	if err := daemon.EnsureDaemonSlot(workingDir); err != nil {
		return err
	}

	extraArgs := []string{"--bypass=" + fmt.Sprintf("%v", bypass)}
	if resumeID != "" {
		extraArgs = append(extraArgs, "--resume="+resumeID)
	}
	if newSession {
		extraArgs = append(extraArgs, "--new-session")
	}
	if fullLoad {
		extraArgs = append(extraArgs, "--full")
	}
	pid, err := daemon.ForkIntoBackground(cfgFile, workingDir, "", extraArgs...)
	if err != nil {
		return fmt.Errorf("starting background daemon: %w", err)
	}
	fmt.Fprintf(os.Stderr, "%s\n", daemon.Tr(lang, "daemon.started_bg", pid))
	fmt.Fprintf(os.Stderr, "%s\n", daemon.Tr(lang, "daemon.workdir", workingDir))
	return nil
}

func unbindBelongsToWorkspace(bindingWorkspace, daemonWorkspace string) bool {
	if bindingWorkspace == "" {
		return true
	}
	return session.NormalizeWorkspacePath(bindingWorkspace) == session.NormalizeWorkspacePath(daemonWorkspace)
}

func runtimeBypassActive(policy permission.PermissionPolicy, mode permission.PermissionMode) bool {
	if cp, ok := policy.(*permission.ConfigPolicy); ok {
		return cp.CurrentMode() == permission.BypassMode
	}
	return mode == permission.BypassMode
}

func runDaemon(cfg *config.Config, cfgFile string, bypass bool, followActive bool, resumeID string, _ bool, noIM bool, startTunnel bool, newSession bool, fullLoad bool, etAuto bool) error {
	// #5367: post-exec self-check, daemon side. If this process was started
	// by a restart exec, verify the running image matches the recorded intent
	// and report the actually-running revision (loud banner on mismatch).
	if notice, _ := restart.StartupVerifyNotice(); notice != "" {
		fmt.Fprintln(os.Stderr, notice)
		restart.ConsumeIntent()
	}

	prov, resolved, err := ResolveProvider(cfg)
	if err != nil {
		return err
	}

	// #2868: `resolved` is reassigned by the provider-switch hook (IM
	// goroutine) and read by the keyboard 't' tunnel path and the snapshot
	// goroutine — guard the pointer with a mutex. ActivateCurrentSelection
	// returns a FRESH pointer on every activation and never mutates an old
	// one, so a lock-copied pointer is safe to read without holding the lock.

	resolvedMu := &sync.Mutex{}
	currentResolved := func() *config.ResolvedEndpoint {
		resolvedMu.Lock()
		defer resolvedMu.Unlock()
		return resolved
	}
	_, knightProv, err := resolveKnightProvider(cfg, resolved, prov)
	if err != nil {
		return err
	}

	workingDir, _ := os.Getwd()
	mode := agentruntime.InteractivePermissionMode(cfg, bypass)
	policy := agentruntime.BuildInteractivePermissionPolicy(cfg, workingDir, bypass)

	var ag *agent.Agent
	core, err := agentruntime.BuildInteractiveRuntimeCore(cfg, workingDir, policy)
	if err == nil {
		core.SetConfigPath(cfgFile)
	}
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

	var knightAgent *knight.Knight

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

	skillTool := agentruntime.NewSkillTool(commandMgr, mcpMgr, prov, registry, skillAgentFactory, workingDir, nil, nil, nil)
	if os.Getenv("GGCODE_TRIAL_FORK") != "" {
		_ = registry.Register(&tool.TrialForkTool{
			Provider:     prov,
			Tools:        registry,
			AgentFactory: skillAgentFactory,
			WorkingDir:   workingDir,
		})
	}
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

	gitStatus := detectGitStatus(workingDir)
	tools := registry.List()
	toolNames := make([]string, len(tools))
	for i, t := range tools {
		toolNames[i] = t.Name()
	}

	agentruntime.InitGlobalSectionCollector(workingDir)
	defer agentruntime.StopGlobalSectionCollector()

	var a2aReg *a2a.Registry

	buildCurrentSystemPrompt := func() (string, []string) {
		remoteAgentsInfo := ""
		if a2aReg != nil {
			if instances := a2aReg.CachedInstances(); len(instances) > 0 {
				remoteAgentsInfo = a2a.FormatRemoteAgents(instances, nil)
			}
		}
		return agentruntime.BuildInteractiveSystemPromptWithPromptRefs(cfg, workingDir, mode, registry, commandMgr, autoMem, projectAutoMem, gitStatus, remoteAgentsInfo, "")
	}
	systemPrompt, promptSkillRefs := buildCurrentSystemPrompt()

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

	ag = agent.NewAgent(prov, registry, systemPrompt, cfg.MaxIterations)

	core.SetConfigAgent(ag)
	refreshAgentSystemPrompt := func() {
		nextPrompt, nextRefs := buildCurrentSystemPrompt()

		promptSkillRefsMu.Lock()
		systemPrompt = nextPrompt
		promptSkillRefs = append(promptSkillRefs[:0], nextRefs...)
		promptSkillRefsMu.Unlock()
		ag.UpdateSystemPrompt(nextPrompt)
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
	ag.SetAuxModel(resolved, cfg.AuxModel)
	ag.SetWorkingDir(workingDir)
	ag.SetSupportsVision(resolved.SupportsVision)
	ag.SetCheckpointManager(checkpoint.NewPersistentManager(50, workingDir))
	tool.SetPreWriteHook(tool.CheckpointSaver(ag.CheckpointManager()))

	ag.SetApprovalHandler(func(_ context.Context, toolName string, input string) permission.Decision {
		return permission.Allow
	})

	store, err := session.NewDefaultStore()
	if err != nil {
		return fmt.Errorf("creating session store: %w", err)
	}

	if fullLoad {
		store.SetFullLoad(true)
	}

	storeDir, _ := session.DefaultDir()
	session.CleanupStaleLocks(storeDir)

	var daemonRestartRequested bool
	restartCh := make(chan struct{}, 1)

	adapters := make(map[string]bool)
	if cfg != nil {
		for name, acfg := range cfg.IM.Adapters {
			adapters[name] = acfg.Enabled
		}
	}
	runtimeInit, err := im.InitRuntime(im.RuntimeInitOptions{
		Workspace:        workingDir,
		EnabledAdapters:  adapters,
		RegisterInstance: false,
	})
	if err != nil {
		return fmt.Errorf("initializing IM runtime: %w", err)
	}
	imMgr := runtimeInit.Manager
	bindingStore := runtimeInit.BindingStore

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

	lang := daemon.ResolveLang(cfg.Language)

	var ses *session.Session
	var sessionLock *session.SessionLock
	if resumeID == "-" || resumeID == "picker" {

		resumeID = pickSessionInteractive(store, lang)
	}
	if resumeID != "" {
		existing, err := store.Load(resumeID)
		if err != nil {
			return fmt.Errorf("loading session %s: %w", resumeID, err)
		}
		ses = existing

		compacted, beforeTokens, afterTokens := agentruntime.RestoreSessionIntoAgent(ag, ses)
		if compacted {
			fmt.Fprintf(os.Stderr, "Restored session was oversized (%d tokens), truncated to %d tokens to fit context window\n", beforeTokens, afterTokens)
		}

		if ses.PermissionMode != "" {
			sessionMode := permission.ParsePermissionMode(ses.PermissionMode)
			if cp, ok := policy.(*permission.ConfigPolicy); ok {
				cp.SetMode(sessionMode)
			}
			mode = sessionMode
			debug.Log("daemon", "restored permission mode %s from session", sessionMode)
		}
	} else {

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

	if sessionLock == nil && ses.ID != "" {
		lock, lockErr := session.TryAcquireSessionLock(storeDir, ses.ID)

		for attempts := 0; lockErr == nil && lock != nil && !lock.Acquired() && attempts < 25; attempts++ {
			time.Sleep(200 * time.Millisecond)
			lock, lockErr = session.TryAcquireSessionLock(storeDir, ses.ID)
		}
		if lockErr == nil && lock != nil && lock.Acquired() {
			sessionLock = lock
		} else if lockErr != nil {

			short := ses.ID
			if len(short) > 8 {
				short = short[:8]
			}
			return fmt.Errorf("acquiring session lock for %s (fail-closed; refusing to run lockless): %w", short, lockErr)
		} else if lock != nil && !lock.Acquired() {
			pid := lock.HolderPID()

			short := ses.ID
			if len(short) > 8 {
				short = short[:8]
			}
			return fmt.Errorf("session %s is locked by another instance (PID %d)", short, pid)
		}
	}

	defer func() {
		if sessionLock != nil {
			sessionLock.Release()
		}
	}()

	emitter := im.NewIMEmitter(imMgr, string(lang), workingDir)
	if cfg.IM.OutputMode != "" {
		emitter.SetOutputMode(cfg.IM.OutputMode)
	}
	bridge := im.NewDaemonBridge(imMgr, ag, emitter, store, ses)
	defer bridge.Close()

	core.Tunnel.BindSession(ses, store)

	ag.SetCheckpointHandler(func(summaryMsgID, lastMsgID string, tokenCount int) {
		if err := store.AppendCheckpoint(ses, summaryMsgID, lastMsgID, tokenCount); err != nil {
			debug.Log("daemon", "checkpoint save failed: %v", err)
		} else {
			debug.Log("daemon", "checkpoint saved: summary_msg_id=%s last_msg_id=%s tokens=%d", summaryMsgID, lastMsgID, tokenCount)
		}
	})

	ag.SetPersistHandler(func(msg provider.Message) {
		if err := store.AppendMessageToDisk(ses, msg); err != nil {
			debug.Log("daemon", "persist handler: AppendMessageToDisk failed: %v", err)
		}
	})

	if tl, ok := registry.Get("ask_user"); ok {
		if askTool, ok := tl.(*tool.AskUserTool); ok {
			askTool.SetHandler(bridge.HandleAskUser)
		}
	}

	if sm, ok := registry.Get("switch_mode"); ok {
		if smt, ok := sm.(*tool.SwitchModeTool); ok {
			if cp, ok := policy.(*permission.ConfigPolicy); ok {
				smt.SetSwitcher(&daemonModeSwitcher{policy: cp, ses: ses, store: store})
			}
		}
	}

	if imt, ok := registry.Get("im"); ok {
		if imTool, ok := imt.(tool.IMTool); ok {
			imTool.Manager = im.NewToolManagerAdapter(imMgr)
			registry.Unregister("im")
			registry.Register(imTool)
		}
	}

	if rt, ok := registry.Get("runtime"); ok {
		if rTool, ok := rt.(tool.RuntimeTool); ok {

			rTool.Provider = &daemonRuntimeProvider{
				ses:    ses,
				cfg:    cfg,
				imMgr:  imMgr,
				bridge: bridge,
				agent:  ag,
			}
			registry.Unregister("runtime")
			registry.Register(rTool)
		}
	}

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

	fileWatch := agentruntime.NewFileWatchTrigger(cfg.Watch, workingDir, cronScheduler.Emit)
	fileWatch.Start()
	defer fileWatch.Stop()

	idleMaint := agentruntime.ApplyIdleMaintenance(ag, cfg.Idle)

	agentruntime.AttachIdleConsolidation(idleMaint, autoMem, workingDir)
	defer idleMaint.Stop()

	subMgr = subagent.NewManager(cfg.SubAgents)
	defer subMgr.Shutdown()

	bridge.SetCascadeCancel(func() {
		subMgr.CancelAll()
	})

	toolLang := im.ToolLangEn
	if lang == daemon.LangZhCN {
		toolLang = im.ToolLangZhCN
	}
	toolPresenter := &daemonToolPresenter{lang: toolLang}
	followDisplay := daemon.NewTerminalFollowDisplay(os.Stderr, lang, workingDir, toolPresenter)

	imMgr.SetBridge(bridge)

	var agentBusy atomic.Bool
	bridge.SetRunStateHook(func(busy bool) { agentBusy.Store(busy) })

	statusMu := &sync.Mutex{}
	var mobileConnectedFn func() (bool, string)

	var tunnelSession *tunnel.Session
	if startTunnel {

		rs := currentResolved()
		sessionInfo := tunnel.SessionInfoData{
			Workspace: workingDir,
			Model:     rs.Model,
			Provider:  rs.VendorName,
			Mode:      mode.String(),
			Version:   version.Version,
		}
		var shareResult *agentruntime.ShareResult
		result, err := core.Tunnel.StartShare(agentruntime.ShareConfig{
			Workspace: workingDir,
			Model:     rs.Model,
			Provider:  rs.VendorName,
			Mode:      mode.String(),
			Version:   version.Version,
			ClientTag: "daemon",
			SnapshotProvider: func() tunnel.BrokerSnapshot {
				return daemonSnapshot(bridge, workingDir, currentResolved(), mode.String())
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

	var prevPairingChallenge *im.PairingChallenge
	imMgr.SetOnUpdate(func(snap im.StatusSnapshot) {
		current := snap.PendingPairing
		wasPending := prevPairingChallenge != nil
		isPending := current != nil

		if isPending && !wasPending {

			platformName := daemon.PlatformDisplayName(string(current.Platform))
			kind := string(current.Kind)
			followDisplay.OnPairingChallenge(platformName, current.ChannelID, current.Code, kind)
		} else if !isPending && wasPending {

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

	imMgr.BindSession(im.SessionBinding{
		SessionID: ses.ID,
		Workspace: workingDir,
	})

	_, others, err := imMgr.RegisterInstance(workingDir, ses.ID)
	if err != nil {
		debug.Log("daemon", "instance detect register failed: %v", err)
	} else if len(others) > 0 {
		primary := others[0]
		fmt.Fprintf(os.Stderr, "Auto-muted IM channels - primary instance (PID %d, started %s)\n",
			primary.PID, primary.StartedAt.Format("15:04"))
	}
	defer imMgr.UnregisterInstance()

	if cfg.IM.Enabled {
		controller, err := im.StartCurrentBindingAdapter(context.Background(), cfg.IM, imMgr)
		if err != nil {
			return fmt.Errorf("starting IM adapter: %w", err)
		}
		defer controller.Stop()
	}

	if mcpMgr := core.MCPManager; mcpMgr != nil {

		mcpMgr.SetURLOpener(func(url string) error {
			fmt.Fprintf(os.Stderr, "\x1b[34m\u2b21 MCP OAuth:\x1b[0m opening browser %s\r\n", url)
			return nil
		})
	}
	core.StartBackgroundServices()
	defer core.Close()

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
			mcpMgr.ClearPendingOAuth(pending.ServerName)
			handler := pending.Handler
			serverName := pending.ServerName
			safego.Go("daemon.mcpOAuth.handle", func() {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
				defer cancel()
				if handler.SupportsDCR() {
					_ = handler.RegisterClient(ctx)
				}

				if handler.SupportsDeviceFlow() {

					scopes := handler.GetScopes()
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

	_, _ = agentruntime.ApplyProjectMemoryToAgent(ag, workingDir)

	homeDir, _ := os.UserHomeDir()
	knightAgent = knight.New(cfg.Knight(), homeDir, workingDir, store)
	knightAgent.SetFactory(knightFactory)
	bridge.SetActivityHook(knightAgent.NotifyActivity)

	var lanchatHub *lanchat.Hub
	bridge.SetRestartHook(func() {
		daemonRestartRequested = true
		select {
		case restartCh <- struct{}{}:
		default:
		}
	})
	bridge.SetProviderSwitchHook(func(vendor, endpoint, model string) (string, error) {

		r, prov, err := agentruntime.ActivateCurrentSelection(cfg, vendor, endpoint, model)
		if err != nil {
			return "", err
		}
		resolvedMu.Lock()
		resolved = r
		resolvedMu.Unlock()
		agentruntime.ApplyProviderToAgent(ag, prov, r)

		if lanchatHub != nil {
			lanchatHub.SetModel(r.Model)
		}

		// #3994: every read below uses the LOCAL r (the fresh pointer
		// ActivateCurrentSelection just returned), never the outer resolved -
		// multiple IM adapters can enter this hook concurrently, and the outer
		// pointer is reassigned under resolvedMu by the other goroutine between
		// L937's unlock and any later read (data race; #2868's own discipline:
		// reads go through currentResolved() or a held-lock copy - r is even
		// stronger, it is this call's immutable snapshot).

		agentruntime.StartAsyncRelayModelLimitRefresh(cfg, r, ag, nil)
		if ses != nil {
			ses.Vendor = cfg.Vendor
			ses.Endpoint = cfg.Endpoint
			ses.Model = r.Model

			if err := store.AppendMetaToDisk(ses); err != nil {
				debug.Log("daemon", "model switch: meta persist failed: %v", err)
			}
		}

		agentruntime.SyncVendorEndpointToGlobal(cfg, cfg.Vendor, cfg.Endpoint)
		if vendor == "" && endpoint == "" && model == "" {
			vendors := make([]string, 0)
			for v := range cfg.Vendors {
				vendors = append(vendors, v)
			}
			endpoints := cfg.EndpointNames(cfg.Vendor)
			models := r.Models
			if len(models) == 0 && r.Model != "" {
				models = []string{r.Model}
			}
			return fmt.Sprintf(
				"📋 Current config:\n  Provider: %s (%s)\n  Model: %s\n  Available providers: %s\n  Endpoints: %s\n  Models: %s",
				r.VendorName, r.EndpointName, r.Model,
				strings.Join(vendors, ", "),
				strings.Join(endpoints, ", "),
				strings.Join(models, ", "),
			), nil
		}
		if model != "" {
			return fmt.Sprintf("✅ Model switched to: %s (%s)", r.Model, r.VendorName), nil
		}
		if vendor != "" {
			return fmt.Sprintf("✅ Provider switched to: %s (%s) → model: %s", r.VendorName, r.EndpointName, r.Model), nil
		}
		return fmt.Sprintf("✅ Config updated: %s (%s) → %s", r.VendorName, r.EndpointName, r.Model), nil
	})

	bridge.SetVisionTurnHook(
		func() string { return agentruntime.VisionTurnModel(cfg) },
		func(model string) error {
			prev := cfg.Model
			resolved, prov, err := agentruntime.ActivateCurrentSelection(cfg, "", "", model)
			if err != nil {

				if prev != "" && prev != model {
					if r, p, rerr := agentruntime.ActivateCurrentSelection(cfg, "", "", prev); rerr == nil {
						agentruntime.ApplyProviderToAgent(ag, p, r)
					} else {
						debug.Log("daemon", "vision turn switch failed and restore failed: %v / %v", err, rerr)
					}
				}
				return err
			}
			agentruntime.ApplyProviderToAgent(ag, prov, resolved)
			return nil
		})
	debug.Log("daemon", "Knight config: enabled=%v trust=%s budget=%d idle=%ds capabilities=%v",
		cfg.Knight().Enabled, cfg.Knight().TrustLevel, cfg.Knight().DailyTokenBudget,
		cfg.Knight().IdleDelaySec, cfg.Knight().Capabilities)
	if cfg.Knight().Enabled {

		knightAgent.SetEmitter(emitter)

		knightAgent.SetEventSink(&knight.FuncSink{
			OnStart: func(taskName string) {
				fmt.Fprintf(os.Stderr, "🌙 Knight: starting %s\r\n", taskName)
			},
			OnComplete: func(taskName string, report string, duration time.Duration) {
				suffix := ""
				if duration > 0 {
					suffix = fmt.Sprintf(" (%.0fs)", duration.Seconds())
				}

				safeReport := strings.ReplaceAll(report, "\n", "\r\n")
				fmt.Fprintf(os.Stderr, "🌙 Knight %s completed%s - %s\r\n", taskName, suffix, safeReport)
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

	var a2aSrv *a2a.Server

	var a2aHandler *a2a.TaskHandler
	var a2aStopRefresh func()
	if !cfg.A2A.Disabled {

		a2aSrv, a2aReg, a2aHandler, a2aStopRefresh, err = startA2AServer(cfg, ag, registry, workingDir, seatIdentityLookup(func() *lanchat.Hub { return lanchatHub }))
		if err != nil {
			debug.Log("daemon", "A2A server start failed: %v", err)
			fmt.Fprintf(os.Stderr, "A2A server warning: %v\n", err)
		} else {
			a2aBgCtx, a2aBgCancel := context.WithCancel(context.Background())
			a2aReg.StartBackgroundRefresh(a2aBgCtx)
			defer func() {
				a2aBgCancel()

				a2aStopRefresh()
				_ = a2aReg.Unregister()
				if lanchatHub != nil {
					lanchatHub.Close()
				}
				a2aSrv.Stop()
			}()
			fmt.Fprintf(os.Stderr, "🔗 A2A server: %s\n", a2aSrv.Endpoint())

			chatStore := lanchat.NewStore(filepath.Join(config.ConfigDir(), "lanchat"))
			lanchatHub = lanchat.NewHub(
				a2aReg.SelfID(),
				"daemon",
				a2aSrv.Endpoint(),
				cfg.LanChat.EffectiveAPIKey(),
				chatStore,
				lanchat.DetectWorkspaceMeta(workingDir),
			)
			lanchatHub.SetRequireAgentApproval(cfg.LanChat.RequireApprovalForAgents)
			// #5616: publish our seat nick into the roster (cross-process nick
			// addressing) now that the hub exists.
			if a2aReg != nil {
				nick := lanchatHub.AgentNick()
				if nick == "" {
					nick = lanchatHub.HumanNick()
				}
				a2aReg.SetSelfIdentity(nick, "", "")
			}

			ag.SetSystemPromptInjector(func() string {
				return lanchat.SharedWorkspaceAlert(
					lanchatHub,
					lanchatHub.NodeID(),
					workingDir,
					lanchat.DirHasVCS(workingDir),
					cfg.Language,
				)
			})
			agentruntime.SetSharedWorkspaceProbe(func() string {
				return lanchat.SharedWorkspaceAlert(
					lanchatHub,
					lanchatHub.NodeID(),
					workingDir,
					lanchat.DirHasVCS(workingDir),
					cfg.Language,
				)
			})
			lanchatHub.SetAttachments(lanchat.NewAttachmentManager())
			lanchat.MountHandlers(a2aSrv.Mux(), lanchatHub, a2aSrv.Port())

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

	if followActive {
		bridge.SetFollowSink(followDisplay)
	}

	isTerminal := term.IsTerminal(int(os.Stdin.Fd()))

	fmt.Fprintf(os.Stderr, "%s\n", daemon.Tr(lang, "daemon.started", ses.ID))
	fmt.Fprintf(os.Stderr, "%s\n", daemon.Tr(lang, "daemon.workdir", workingDir))
	if isTerminal {
		fmt.Fprintf(os.Stderr, "%s\n", daemon.Tr(lang, "daemon.keys_full"))
	} else {
		fmt.Fprintf(os.Stderr, "%s\n", daemon.Tr(lang, "daemon.keys_minimal"))
	}

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

			deleted := false
			for _, pb := range imMgr.AllPersistedBindings() {
				if pb.Adapter != adapter {
					continue
				}
				if !unbindBelongsToWorkspace(pb.Workspace, workingDir) {

					continue
				}
				if err := imMgr.DeleteBinding(adapter, pb.Workspace); err != nil {
					return err
				}
				deleted = true
			}
			if !deleted {

				return fmt.Errorf("no persisted binding for adapter %q in workspace %q", adapter, session.NormalizeWorkspacePath(workingDir))
			}
			return nil
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
	// 双隧道状态快照:daemon 无 TUI Model,share 面由 TunnelHost 直出
	// (修复:此前未注入 → /api/tunnel/status 恒全 false,webui 共享弹窗
	// 卡"等待二维码"而后端 QR 已生成);et 面默认 inactive(daemon 不承载 /et)。
	etCtl := &webui.TunnelCtl{}
	webuiSrv.SetTunnelStatusFn(func() map[string]any {
		return map[string]any{
			"share": core.Tunnel.ShareStatusSnapshot(),
			"et": map[string]any{
				"active": etCtl.TunnelURL() != "",
				"url":    etCtl.TunnelURL(),
			},
		}
	})
	webuiAddr := "127.0.0.1:0"
	actualAddr, webuiErr := webuiSrv.Start(webuiAddr)
	if webuiErr == nil {
		url := "http://" + actualAddr
		if tk := webuiSrv.Token(); tk != "" {
			url += "#token=" + tk
		}
		// /et over IM inbound (daemon previously did not host the command).
		bridge.SetEtunnel(etCtl, url,
			func() (string, string, string) { return cfg.ETunnel.Subdomain, cfg.ETunnel.Token, cfg.ETunnel.Plan },
			func(subdomain, token string) error {
				cfg.ETunnel.Subdomain = subdomain
				cfg.ETunnel.Token = token
				return cfg.Save()
			})
		fmt.Fprintf(os.Stderr, "\x1b[34m\u2B21 WebUI:\x1b[0m \x1b[32m%s\x1b[0m\r\n", url)

		// --etunnel: auto-start the public WebUI tunnel after init and print
		// the URL plus a scannable terminal QR (mobile opens it directly,
		// token rides in the #fragment).
		if etAuto {
			go func(localURL string) {
				defer safego.Recover("daemon.etunnel-autostart")
				msg, err := webui.ControlEt(etCtl, localURL,
					cfg.ETunnel.Subdomain, cfg.ETunnel.Token, cfg.ETunnel.Plan,
					nil, []string{"on"})
				if err != nil {
					fmt.Fprintf(os.Stderr, "\x1b[33m\u26A0 --etunnel: %v\x1b[0m\r\n", err)
					return
				}
				fmt.Fprintf(os.Stderr, "\x1b[34m\u2B21 %s\x1b[0m\r\n", msg)
				fullURL := etCtl.TunnelURL()
				if tk := webuiSrv.Token(); tk != "" && fullURL != "" {
					fullURL += "#token=" + tk
				}
				if qr, qerr := qrcode.RenderCompact(fullURL); qerr == nil {
					fmt.Fprintf(os.Stderr, "%s\r\n\x1b[8m(scan to open the WebUI from mobile)\x1b[0m\r\n", qr)
				}
			}(url)
		}

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

		statusMu.Lock()
		if mobileConnectedFn != nil {
			connected, sid := mobileConnectedFn()
			st.MobileConn.Connected = connected
			st.MobileConn.SessionID = sid
		}
		statusMu.Unlock()
		return st
	})

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	kbCh := make(chan byte, 8)
	var restoreTerminal func()
	if isTerminal {
		restoreTerminal = readKeyboard(kbCh)
	}

	detachedToBackground := false
	defer func() {
		if !detachedToBackground {
			daemon.CleanupDaemon(workingDir)
		}
	}()

	var unsubscribeTunnelPrev func()

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
			case 'x':
				break loop
			case 'd':

				var detachExtra []string
				if runtimeBypassActive(policy, mode) {
					detachExtra = append(detachExtra, "--bypass")
				}
				if core.Tunnel.GetShareInfo() != nil {
					detachExtra = append(detachExtra, "--tunnel")
				}
				if fullLoad {
					detachExtra = append(detachExtra, "--full")
				}

				if detachToBackground(lang, cfgFile, workingDir, ses.ID, detachExtra) {
					detachedToBackground = true
					break loop
				}
			case 'f':
				followActive = !followActive
				if followActive {
					bridge.SetFollowSink(followDisplay)
					fmt.Fprintf(os.Stderr, "%s\r\n", daemon.Tr(lang, "daemon.follow_on"))
				} else {
					bridge.SetFollowSink(nil)
					fmt.Fprintf(os.Stderr, "%s\r\n", daemon.Tr(lang, "daemon.follow_off"))
				}
			case 'v':
				if emitter != nil {
					emitter.SetOutputMode("verbose")
					fmt.Fprintf(os.Stderr, "%s\r\n", daemon.Tr(lang, "daemon.output_mode", "verbose"))
				}
			case 'q':
				if emitter != nil {
					emitter.SetOutputMode("quiet")
					fmt.Fprintf(os.Stderr, "%s\r\n", daemon.Tr(lang, "daemon.output_mode", "quiet"))
				}
			case 's':
				if emitter != nil {
					emitter.SetOutputMode("summary")
					fmt.Fprintf(os.Stderr, "%s\r\n", daemon.Tr(lang, "daemon.output_mode", "summary"))
				}
			case 'M':
				if imMgr != nil {
					count, err := imMgr.MuteAll()
					if err == nil && count > 0 {
						fmt.Fprintf(os.Stderr, "%s\r\n", daemon.Tr(lang, "daemon.mute_all", count))
					}
				}
			case 'U':
				if imMgr != nil {
					count, err := imMgr.UnmuteAll()
					if err == nil && count > 0 {
						fmt.Fprintf(os.Stderr, "%s\r\n", daemon.Tr(lang, "daemon.unmute_all", count))
					}
				}
			case 't':
				if core.Tunnel.GetShareInfo() != nil {

					info := core.Tunnel.GetShareInfo()
					fmt.Fprintf(os.Stderr, "\r\n  Mobile Tunnel Active\r\n")
					fmt.Fprintf(os.Stderr, "  URL: %s\r\n\r\n", info.ConnectURL)
					fmt.Fprintf(os.Stderr, "%s\r\n", strings.ReplaceAll(info.QRCode, "\n", "\r\n"))
				} else {

					rs := currentResolved()
					sessionInfo := tunnel.SessionInfoData{
						Workspace: workingDir,
						Model:     rs.Model,
						Provider:  rs.VendorName,
						Mode:      mode.String(),
						Version:   version.Version,
					}

					var shareResult *agentruntime.ShareResult

					result, err := core.Tunnel.StartShare(agentruntime.ShareConfig{
						Workspace: workingDir,
						Model:     rs.Model,
						Provider:  rs.VendorName,
						Mode:      mode.String(),
						Version:   version.Version,
						ClientTag: "daemon",
						SnapshotProvider: func() tunnel.BrokerSnapshot {
							return daemonSnapshot(bridge, workingDir, currentResolved(), mode.String())
						},
						OnCommand: func(cmd tunnel.GatewayMessage) {

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

						ctrl := newDaemonTunnelShareController(result.Broker, bridge, sessionInfo, core.Tunnel)
						bridge.SetRunStateHook(func(busy bool) {
							agentBusy.Store(busy)
							ctrl.HandleRunState(busy)
						})
						bridge.SetUserMessageHook(ctrl.HandleUserMessage)

						if unsubscribeTunnelPrev != nil {
							unsubscribeTunnelPrev()
						}
						unsubscribeTunnelPrev = bridge.Subscribe(ctrl.HandleStreamEvent)

						fmt.Fprintf(os.Stderr, "\r\n  Mobile Tunnel Active\r\n")
						fmt.Fprintf(os.Stderr, "  URL: %s\r\n\r\n", result.ConnectURL)
						fmt.Fprintf(os.Stderr, "%s\r\n", strings.ReplaceAll(result.QRCode, "\n", "\r\n"))
					}
				}
			case 'r':
				daemonRestartRequested = true
				fmt.Fprintf(os.Stderr, "%s\r\n", "[ggcode restart] restarting...")
				break loop
			}
		}
	}

	if restoreTerminal != nil {
		restoreTerminal()
	}

	if daemonRestartRequested {

		binary, err := restart.ResolveBinary()
		if err != nil {
			return fmt.Errorf("restart: resolve binary: %w", err)
		}
		bridge.Close()
		if lanchatHub != nil {
			lanchatHub.Close()
		}

		_ = store.AppendMetaToDisk(ses)

		if sessionLock != nil {
			sessionLock.Release()
			sessionLock = nil
		}

		runfile.Remove(ses.ID)

		var args []string

		if cfgFile != "" {
			args = append(args, "--config", cfgFile)
		}
		args = append(args, "daemon", "--follow")
		args = append(args, "--__daemonized")
		if ses.ID != "" {
			args = append(args, "--resume", ses.ID)
		}
		if bypass {
			args = append(args, "--bypass")
		}

		if startTunnel || core.Tunnel.GetShareInfo() != nil {
			args = append(args, "--tunnel")
		}
		if fullLoad {
			args = append(args, "--full")
		}

		env := os.Environ()
		if bridge.ConsumeRestartDebug() {
			debug.Log("daemon", "restart with GGCODE_DEBUG=1")
			env = append(env, "GGCODE_DEBUG=1")
		}

		debug.Log("daemon", "exec restart: %s %v", binary, args)
		if err := restart.ExecRestart(binary, args, env); err != nil {

			debug.Log("daemon", "exec restart failed: %v", err)
			fmt.Fprintf(os.Stderr, "[ggcode restart] failed: %v\r\n", err)
			return fmt.Errorf("restart: exec: %w", err)
		}
		return nil
	}

	fmt.Fprint(os.Stderr, daemon.Tr(lang, "daemon.shutting_down")+"\r\n")

	bridge.Close()

	if err := store.AppendMetaToDisk(ses); err != nil {
		debug.Log("daemon", "exit: meta persist failed: %v", err)
	}

	if err := store.CleanupIfEmpty(ses); err != nil {
		debug.Log("daemon", "exit cleanup: failed to delete empty session: %v", err)
	}

	if sessionLock != nil {
		sessionLock.Release()
		sessionLock = nil
	}

	fmt.Fprintln(os.Stderr, daemon.Tr(lang, "daemon.stopped"))
	return nil
}
