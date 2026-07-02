package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/topcheer/ggcode/internal/a2a"
	"github.com/topcheer/ggcode/internal/agent"
	"github.com/topcheer/ggcode/internal/agentruntime"
	"github.com/topcheer/ggcode/internal/auth"
	"github.com/topcheer/ggcode/internal/checkpoint"
	"github.com/topcheer/ggcode/internal/commands"
	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/im"
	"github.com/topcheer/ggcode/internal/knight"
	"github.com/topcheer/ggcode/internal/lanchat"
	"github.com/topcheer/ggcode/internal/mcp"
	"github.com/topcheer/ggcode/internal/memory"
	"github.com/topcheer/ggcode/internal/plugin"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/runfile"

	"github.com/topcheer/ggcode/internal/safego"
	"github.com/topcheer/ggcode/internal/session"
	"github.com/topcheer/ggcode/internal/subagent"
	"github.com/topcheer/ggcode/internal/swarm"
	"github.com/topcheer/ggcode/internal/task"
	"github.com/topcheer/ggcode/internal/tool"
	"github.com/topcheer/ggcode/internal/tui"
	"github.com/topcheer/ggcode/internal/update"
	"github.com/topcheer/ggcode/internal/version"
	"github.com/topcheer/ggcode/internal/webui"
)

func NewRootCmd() *cobra.Command {
	var cfgFile string
	var resumeID string
	var pipePrompt string
	var allowedTools []string
	var allowedDirs []string
	var readOnlyAllowedDirs []string
	var bypassFlag bool
	var noHarnessFlag bool
	var outputPath string
	var helperManifest string

	cmd := &cobra.Command{
		Use:              "ggcode",
		Short:            "AI coding assistant",
		Long:             "ggcode is a terminal-based AI coding agent powered by LLMs.",
		SilenceUsage:     true,
		TraverseChildren: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Global --version/-v: print and exit immediately, before any
			// config loading or TUI initialization.
			if showVer, _ := cmd.Flags().GetBool("version"); showVer {
				fmt.Fprintln(cmd.OutOrStdout(), version.Display())
				return nil
			}
			if showVer, _ := cmd.Flags().GetBool("v"); showVer {
				fmt.Fprintln(cmd.OutOrStdout(), version.Display())
				return nil
			}

			if cfgFile == "" {
				resolved, err := resolveConfigFilePath()
				if err != nil {
					return fmt.Errorf("resolving config path: %w", err)
				}
				cfgFile = resolved
			}
			if pipePrompt == "" {
				interactive := writerIsTerminal(os.Stdout) && writerIsTerminal(os.Stdin)
				proceed, err := confirmPlaintextAPIKeysBeforeTUI(cfgFile, os.Stdin, os.Stdout, interactive)
				if err != nil {
					return err
				}
				if !proceed {
					return nil
				}
			}

			debug.Init()

			workingDirForConfig, _ := os.Getwd()
			cfg, err := config.LoadWithInstance(cfgFile, workingDirForConfig)
			if err != nil {
				return fmt.Errorf("loading config: %w", err)
			}
			if _, _, err := mcp.PersistUserClaudeServers(cfg); err != nil {
				return fmt.Errorf("persisting Claude MCP servers: %w", err)
			}

			// Pipe mode: non-interactive single execution
			if pipePrompt != "" {
				code := RunPipe(cfg, cfgFile, pipePrompt, allowedTools, allowedDirs, outputPath, bypassFlag, noHarnessFlag, readOnlyAllowedDirs)
				if code != 0 {
					debug.Close()
					os.Exit(code)
				}
				return nil
			}

			// Warn if launched from HOME directory.
			if !bypassFlag {
				wd, _ := os.Getwd()
				if home, err := os.UserHomeDir(); err == nil && wd == home {
					lang := tui.LangEnglish
					if cfg.Language != "" {
						lang = tui.NormalizeLanguage(cfg.Language)
					}
					if !tui.ConfirmHomeDir(lang) {
						fmt.Fprintln(os.Stderr, "Please cd into a project directory and run ggcode again.")
						return nil
					}
				}
			}

			// Onboard wizard for first-time users without a working LLM config.
			if !bypassFlag && cfg.NeedsOnboard() {
				if err := runOnboardAndRestart(cfg); err != nil {
					fmt.Fprintf(os.Stderr, "Onboard failed: %v\n", err)
					os.Exit(1)
				}
				return nil
			}

			resumePicker, _ := cmd.Flags().GetBool("resume-picker")
			if resumePicker {
				return run(cfg, cfgFile, "picker", bypassFlag)
			}
			return run(cfg, cfgFile, resumeID, bypassFlag)
		},
	}

	cmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file path")

	// Global --version/-v flag — many tools and agents probe this before
	// trying a "version" subcommand. Must be handled before RunE starts the TUI.
	cmd.Flags().Bool("version", false, "print version and exit")
	cmd.Flags().BoolP("v", "v", false, "shorthand for --version")
	cmd.Flags().StringVar(&resumeID, "resume", "", "resume a previous session by ID")
	cmd.Flags().Bool("resume-picker", false, "interactively select a session to resume")
	cmd.Flags().StringVarP(&pipePrompt, "prompt", "p", "", "pipe mode: non-interactive execution with a prompt")
	cmd.Flags().StringArrayVar(&allowedTools, "allowedTools", nil, "tools to allow in pipe mode (can be repeated)")
	cmd.Flags().StringArrayVar(&allowedDirs, "allowedDir", nil, "override writable sandbox directory for pipe mode (can be repeated)")
	_ = cmd.Flags().MarkHidden("allowedDir")
	cmd.Flags().StringArrayVar(&readOnlyAllowedDirs, "readOnlyAllowedDir", nil, "extra read-only sandbox directory for pipe mode (can be repeated)")
	_ = cmd.Flags().MarkHidden("readOnlyAllowedDir")
	cmd.Flags().BoolVar(&bypassFlag, "bypass", false, "start in bypass permission mode (auto-approve safe ops, warn on dangerous)")
	cmd.Flags().BoolVar(&noHarnessFlag, "no-harness", false, "skip harness auto-run routing in pipe mode (force normal agent)")
	cmd.Flags().StringVar(&outputPath, "output", "", "output file path (default: stdout)")

	helperCmd := &cobra.Command{
		Use:    "update-helper",
		Short:  "Internal update helper",
		Hidden: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(helperManifest) == "" {
				return fmt.Errorf("missing --manifest")
			}
			return update.RunHelper(helperManifest)
		},
	}
	helperCmd.Flags().StringVar(&helperManifest, "manifest", "", "update manifest path")
	cmd.AddCommand(helperCmd)

	// version subcommand — outputs version string and exits immediately.
	// Used by installers, package managers, and cross-install detection.
	versionCmd := &cobra.Command{
		Use:   "version",
		Short: "Print the ggcode version",
		Run: func(cmd *cobra.Command, _ []string) {
			fmt.Fprintln(cmd.OutOrStdout(), version.Display())
		},
	}
	cmd.AddCommand(versionCmd)

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
	cmd.AddCommand(newHarnessCmd(&cfgFile))
	cmd.AddCommand(newMCPCmd(&cfgFile))
	cmd.AddCommand(newPluginCmd(&cfgFile))
	cmd.AddCommand(newIMCmd(&cfgFile))
	cmd.AddCommand(newDaemonCmd(&cfgFile))
	cmd.AddCommand(newLLMProbeCmd(&cfgFile))
	cmd.AddCommand(newACPCommand(&cfgFile))
	cmd.AddCommand(newStatusCmd())
	configureHelpRendering(cmd)

	return cmd
}

func resolveConfigFilePath() (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for _, candidate := range []string{
		filepath.Join(wd, "ggcode.yaml"),
		filepath.Join(wd, ".ggcode", "ggcode.yaml"),
	} {
		info, err := os.Stat(candidate)
		if err == nil && !info.IsDir() {
			return candidate, nil
		}
		if err != nil && !os.IsNotExist(err) {
			return "", err
		}
	}
	return config.ConfigPath(), nil
}

func configureHelpRendering(cmd *cobra.Command) {
	cmd.InitDefaultHelpFlag()
	cmd.SetHelpFunc(func(c *cobra.Command, _ []string) {
		_ = writeCommandHelp(c.OutOrStdout(), c)
	})
	cmd.SetUsageFunc(func(c *cobra.Command) error {
		return writeCommandUsage(c.OutOrStderr(), c)
	})
}

func writeCommandHelp(w io.Writer, cmd *cobra.Command) error {
	var b strings.Builder

	desc := strings.TrimSpace(firstNonEmpty(cmd.Long, cmd.Short))
	if desc != "" {
		b.WriteString(desc)
		b.WriteString("\n\n")
	}

	b.WriteString("Usage:\n")
	for _, line := range usageLines(cmd) {
		b.WriteString(line)
		b.WriteString("\n")
	}

	writeCommandList(&b, "Available Commands", visibleSubcommands(cmd))
	writeFlagList(&b, "Flags", mergedFlags(cmd))

	if len(visibleSubcommands(cmd)) > 0 {
		b.WriteString("\nUse \"")
		b.WriteString(cmd.CommandPath())
		b.WriteString(" [command] --help\" for more information about a command.\n")
	}

	_, err := writeCLIText(w, b.String())
	return err
}

func writeCommandUsage(w io.Writer, cmd *cobra.Command) error {
	var b strings.Builder
	b.WriteString("Usage:\n")
	for _, line := range usageLines(cmd) {
		b.WriteString(line)
		b.WriteString("\n")
	}
	writeFlagList(&b, "Flags", mergedFlags(cmd))
	_, err := writeCLIText(w, b.String())
	return err
}

func usageLines(cmd *cobra.Command) []string {
	lines := []string{cmd.UseLine()}
	if cmd == cmd.Root() && len(visibleSubcommands(cmd)) > 0 {
		lines = append(lines, cmd.CommandPath()+" [command]")
	}
	return lines
}

func visibleSubcommands(cmd *cobra.Command) []*cobra.Command {
	var out []*cobra.Command
	for _, sub := range cmd.Commands() {
		if !sub.IsAvailableCommand() || sub.Hidden {
			continue
		}
		out = append(out, sub)
	}
	return out
}

func mergedFlags(cmd *cobra.Command) []*pflag.Flag {
	seen := map[string]struct{}{}
	var out []*pflag.Flag
	appendSet := func(fs *pflag.FlagSet) {
		if fs == nil {
			return
		}
		fs.VisitAll(func(flag *pflag.Flag) {
			if flag.Hidden {
				return
			}
			if _, ok := seen[flag.Name]; ok {
				return
			}
			seen[flag.Name] = struct{}{}
			out = append(out, flag)
		})
	}
	appendSet(cmd.NonInheritedFlags())
	appendSet(cmd.InheritedFlags())
	return out
}

func writeCommandList(b *strings.Builder, title string, commands []*cobra.Command) {
	if len(commands) == 0 {
		return
	}
	b.WriteString("\n")
	b.WriteString(title)
	b.WriteString(":\n")
	for _, sub := range commands {
		b.WriteString("- ")
		b.WriteString(sub.Name())
		b.WriteString(": ")
		b.WriteString(strings.TrimSpace(sub.Short))
		b.WriteString("\n")
	}
}

func writeFlagList(b *strings.Builder, title string, flags []*pflag.Flag) {
	if len(flags) == 0 {
		return
	}
	b.WriteString("\n")
	b.WriteString(title)
	b.WriteString(":\n")
	for _, flag := range flags {
		b.WriteString("- ")
		b.WriteString(formatFlagLabel(flag))
		b.WriteString(": ")
		b.WriteString(strings.TrimSpace(flag.Usage))
		b.WriteString("\n")
	}
}

func formatFlagLabel(flag *pflag.Flag) string {
	parts := make([]string, 0, 2)
	if flag.Shorthand != "" {
		parts = append(parts, "-"+flag.Shorthand)
	}
	parts = append(parts, "--"+flag.Name)
	label := strings.Join(parts, ", ")
	if valueType := strings.TrimSpace(flag.Value.Type()); valueType != "" && valueType != "bool" {
		label += " " + valueType
	}
	return label
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func run(cfg *config.Config, cfgFile, resumeID string, bypass bool) error {
	trace := newStartupTrace("root.run")
	defer trace.Mark("return")

	prov, resolved, err := ResolveProvider(cfg)
	if err != nil {
		return err
	}
	trace.Mark("resolve provider")

	_, knightProv, err := resolveKnightProvider(cfg, resolved, prov)
	if err != nil {
		return err
	}
	trace.Mark("resolve knight provider")

	workingDir, _ := os.Getwd()
	trace.Mark("working directory")
	policy := agentruntime.BuildInteractivePermissionPolicy(cfg, workingDir, bypass)
	mode := agentruntime.InteractivePermissionMode(cfg, bypass)
	trace.Mark("permission policy")

	var ag *agent.Agent // declared early so closures can capture it

	core, err := agentruntime.BuildInteractiveRuntimeCore(cfg, workingDir, policy)
	if err != nil {
		return err
	}
	registry := core.Registry
	mcpMgr := core.MCPManager
	pluginMgr := core.PluginManager
	autoMem := core.AutoMemory
	projectAutoMem := core.ProjectAutoMem
	saveMemoryTool := core.SaveMemoryTool
	startupAssets := core.StartupAssets
	autoFiles := startupAssets.AutoFiles
	commandMgr := startupAssets.CommandManager
	trace.Mark("build interactive runtime core")

	projectMemoryLoader := func() (string, []string, error) {
		return memory.LoadProjectMemory(workingDir)
	}
	var skillUsageHandler func(provider.TokenUsage)
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
	skillTool := agentruntime.NewSkillTool(commandMgr, mcpMgr, prov, registry, skillAgentFactory, workingDir, func(usage provider.TokenUsage) {
		if skillUsageHandler != nil {
			skillUsageHandler(usage)
		}
	}, nil) // SystemPromptBuilder set below after buildCurrentSystemPrompt is defined
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

	// Discover and register ACP agent clients (delegate tool)
	acpClientMgr := agentruntime.NewACPClientManager(workingDir, policy, nil)
	agentruntime.RegisterDelegateTool(registry, acpClientMgr, func() *subagent.Manager { return subMgr }, workingDir, func() string {
		if ag != nil {
			return ag.WorkingDir()
		}
		return workingDir
	})
	if len(acpClientMgr.Available()) > 0 {
		debug.Log("startup", "discovered ACP agents: %v", acpClientMgr.Available())
	}
	trace.Mark("register acp client delegate tool")

	// Detect git status
	gitStatus := detectGitStatus(workingDir)
	trace.Mark("detect git status")

	// Collect tool names
	tools := registry.List()
	toolNames := make([]string, len(tools))
	for i, t := range tools {
		toolNames[i] = t.Name()
	}
	trace.Mark("collect tool names")

	// Declare early so buildCurrentSystemPrompt closure can reference them.
	var a2aRegistry *a2a.Registry
	var lanchatHub *lanchat.Hub

	// buildRemoteAgentMeta returns enrichment data (team, role, languages)
	// from lanchat presence exchange, keyed by instance ID. Returns nil if
	// lanchat is not available or no peers are known yet.
	buildRemoteAgentMeta := func() map[string]a2a.RemoteAgentMeta {
		if lanchatHub == nil {
			return nil
		}
		participants := lanchatHub.Participants()
		if len(participants) == 0 {
			return nil
		}
		meta := make(map[string]a2a.RemoteAgentMeta, len(participants))
		for _, p := range participants {
			meta[p.NodeID] = a2a.RemoteAgentMeta{
				Team:        p.Team,
				Role:        p.Role,
				Languages:   p.Languages,
				ProjectName: p.ProjectName,
			}
		}
		return meta
	}

	buildCurrentSystemPrompt := func() (string, []string) {
		// Remote agents info is injected dynamically via systemPromptInjector
		// (lanchat peers), not baked into the static prompt.
		return agentruntime.BuildInteractiveSystemPromptWithPromptRefs(cfg, workingDir, mode, registry, commandMgr, autoMem, projectAutoMem, gitStatus, "")
	}
	systemPrompt, promptSkillRefs := buildCurrentSystemPrompt()
	trace.Mark(fmt.Sprintf("build initial system prompt skills=%d bytes=%d", len(promptSkillRefs), len(systemPrompt)))

	// Set the sub-agent system prompt builder on the skill tool now that
	// buildCurrentSystemPrompt is available.
	skillTool.SystemPromptBuilder = func(task, agentType string) string {
		remoteAgentsInfo := ""
		if a2aRegistry != nil {
			if instances := a2aRegistry.CachedInstances(); len(instances) > 0 {
				remoteAgentsInfo = a2a.FormatRemoteAgents(instances, buildRemoteAgentMeta())
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

	// Register skill tool now that SystemPromptBuilder is set (must be before
	// any registry.Clone() for sub-agent/teammate tool isolation).
	_ = registry.Register(skillTool)
	trace.Mark("register skill tool")

	var promptSkillRefsMu sync.RWMutex
	currentPromptSkillRefs := func() []string {
		promptSkillRefsMu.RLock()
		defer promptSkillRefsMu.RUnlock()
		return append([]string(nil), promptSkillRefs...)
	}

	// Setup sub-agent manager
	subMgr = subagent.NewManager(cfg.SubAgents)
	defer subMgr.Shutdown()
	trace.Mark("setup sub-agent manager")

	// Setup agent
	maxIter := cfg.MaxIterations
	ag = agent.NewAgent(prov, registry, systemPrompt, maxIter)
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
			debug.Log("root", "Knight scenario record failed: %v", scenarioErr)
		}
	})
	agentruntime.ApplyResolvedLimitsToAgent(ag, resolved)
	agentruntime.StartAsyncRelayModelLimitRefresh(cfg, resolved, ag, nil)
	ag.SetPermissionPolicy(policy)
	ag.SetHookConfig(cfg.Hooks)
	ag.SetWorkingDir(workingDir)
	ag.SetCheckpointManager(checkpoint.NewManager(50))
	tool.SetPreWriteHook(tool.CheckpointSaver(ag.CheckpointManager()))
	ag.SetSupportsVision(resolved.SupportsVision)
	trace.Mark("setup agent")

	// Setup session store
	store, err := session.NewDefaultStore()
	if err != nil {
		return fmt.Errorf("creating session store: %w", err)
	}
	trace.Mark("create session store")

	// Clean up stale lock files from crashed/killed processes.
	storeDir, _ := session.DefaultDir()
	session.CleanupStaleLocks(storeDir)

	var replPendingSessionLock *session.SessionLock

	if resumeID == "picker" {
		selectedID, err := pickResumeSession(store, session.CurrentWorkspacePath())
		if err != nil {
			return err
		}
		if selectedID != "" {
			storeDir, _ := session.DefaultDir()
			lock, lockErr := session.TryAcquireSessionLock(storeDir, selectedID)
			if lockErr == nil && lock != nil && lock.Acquired() {
				replPendingSessionLock = lock
				resumeID = selectedID
			} else {
				// Race: session was locked between picker filter and now.
				fmt.Fprintf(os.Stderr, "  Session %s is locked by another instance. Starting a new session.\n", selectedID[:8])
			}
		}
		trace.Mark("pick resume session")
	} else if resumeID == "" {
		// Auto-load: find the most recent unlocked workspace session.
		// Walk sessions from newest to oldest; first one we can lock wins.
		// If all are locked by other instances, create a new session.
		workspace := workingDir
		sessions, err := store.ListForWorkspace(workspace)
		if err != nil {
			debug.Log("root", "ListForWorkspace error: %v", err)
		}
		if len(sessions) > 0 {
			storeDir, _ := session.DefaultDir()
			for _, ses := range sessions {
				lock, lockErr := session.TryAcquireSessionLock(storeDir, ses.ID)
				if lockErr == nil && lock != nil && lock.Acquired() {
					replPendingSessionLock = lock
					resumeID = ses.ID
					trace.Mark("auto-load session")
					break
				}
			}
			if resumeID == "" {
				fmt.Fprintf(os.Stderr, "\n  All %d workspace session(s) are in use by other instances. Starting a new session.\n", len(sessions))
			}
		}
		trace.Mark("pick resume session")
	}
	homeDir, _ := os.UserHomeDir()
	knightAgent = knight.New(cfg.Knight(), homeDir, workingDir, store)
	knightAgent.SetFactory(knightFactory)
	trace.Mark("create knight")

	var knightConflictHint string
	if cfg.Knight().Enabled {
		if err := knightAgent.Start(context.Background()); err != nil {
			if errors.Is(err, knight.ErrLockConflict) {
				pid, _ := knight.LockHeldBy(workingDir)
				knightConflictHint = knight.FormatLockMessage(pid)
			} else {
				debug.Log("root", "Knight startup warning: %v", err)
			}
		} else {
			defer knightAgent.Stop()
			if commandMgr.Reload() {
				refreshAgentSystemPrompt()
			} else {
				knightAgent.RecordSkillPromptExposure(currentPromptSkillRefs())
			}
		}
	}
	trace.Mark("start knight")

	// Build MCP info for TUI
	mcpInfos := toTuiMCPInfos(mcpMgr.Snapshot())
	trace.Mark("build tui mcp info")

	// Start A2A server if enabled.
	var a2aServer *a2a.Server
	// a2aRegistry already declared above for system prompt access
	var a2aTaskHandler *a2a.TaskHandler
	if !cfg.A2A.Disabled {
		// A2A instance override already applied by LoadWithInstance.
		a2aSrv, a2aReg, a2aHandler, err := startA2AServer(cfg, ag, registry, workingDir)
		if err != nil {
			debug.Log("root", "A2A server startup warning: %v", err)
		} else {
			a2aServer = a2aSrv
			a2aRegistry = a2aReg
			a2aTaskHandler = a2aHandler
			// Start async background refresh so CachedInstances() returns
			// useful data without ever blocking the UI thread.
			a2aBgCtx, a2aBgCancel := context.WithCancel(context.Background())
			a2aReg.StartBackgroundRefresh(a2aBgCtx)
			defer func() {
				a2aBgCancel()
				if a2aRegistry != nil {
					_ = a2aRegistry.Unregister()
				}
				if a2aServer != nil {
					a2aServer.Stop()
				}
			}()
		}
	}
	trace.Mark("start a2a")

	// Start LAN chat if A2A server is running.
	// lanchatHub is declared above (before buildCurrentSystemPrompt) so the
	// prompt builder closure can read lanchat presence meta for enrichment.
	if a2aServer != nil && !cfg.A2A.Disabled {
		chatStore := lanchat.NewStore(filepath.Join(config.ConfigDir(), "lanchat"))
		chatMode := "cli"
		// In daemon-follow mode, auto-approve agent messages
		lanchatHub = lanchat.NewHub(
			a2aRegistry.SelfID(),
			chatMode,
			a2aServer.Endpoint(),
			cfg.A2A.EffectiveAPIKey(),
			chatStore,
			lanchat.DetectWorkspaceMeta(workingDir),
		)
		lanchatHub.SetAttachments(lanchat.NewAttachmentManager())
		lanchat.MountHandlers(a2aServer.Mux(), lanchatHub)
		// Sync peers from A2A registry
		safego.Go("lanchat.syncPeers", func() {
			syncPeers := func() {
				instances := a2aRegistry.CachedInstances()
				if instances == nil {
					return // cache not populated yet
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
			// Initial sync after 3s (let mDNS browser warm up)
			time.Sleep(3 * time.Second)
			syncPeers()
			// Then periodic every 10s
			ticker := time.NewTicker(10 * time.Second)
			defer ticker.Stop()
			for range ticker.C {
				syncPeers()
			}
		})
	}

	// Start TUI REPL
	repl := tui.NewREPL(ag, policy)
	skillUsageHandler = repl.SessionUsageHandler()
	if a2aTaskHandler != nil {
		repl.SetA2AHandler(a2aTaskHandler)
	}
	if lanchatHub != nil {
		repl.SetLanChatHub(lanchatHub)
	}
	trace.Mark("create repl")

	var imMgr *im.Manager
	{
		adapters := make(map[string]bool)
		for name, acfg := range cfg.IM.Adapters {
			adapters[name] = acfg.Enabled
		}
		runtimeInit, err := im.InitRuntime(im.RuntimeInitOptions{
			Workspace:       workingDir,
			EnabledAdapters: adapters,
		})
		if err != nil {
			return fmt.Errorf("initializing IM runtime: %w", err)
		}
		imMgr = runtimeInit.Manager
		if knightAgent != nil {
			knightAgent.SetEmitter(im.NewIMEmitter(imMgr, cfg.Language, workingDir))
		}
		if cfg.IM.Enabled {
			controller, err := im.StartCurrentBindingAdapter(context.Background(), cfg.IM, imMgr)
			if err != nil {
				return fmt.Errorf("starting current workspace IM adapter: %w", err)
			}
			defer controller.Stop()
		}
		repl.SetIMManager(imMgr)
	}
	trace.Mark("setup im")

	if execPath, err := os.Executable(); err == nil {
		repl.SetUpdateService(update.NewService(version.Display(), execPath, cfgFile, workingDir))
	}
	trace.Mark("setup update service")

	repl.SetConfig(cfg)
	repl.SetSessionStore(store)
	repl.SetMCPServers(mcpInfos)
	repl.SetMCPManager(mcpMgr)
	repl.SetCore(core)
	repl.SetPluginManager(pluginMgr)
	repl.SetCommandsManager(commandMgr)
	repl.SetSkillsChangedHook(refreshAgentSystemPrompt)
	repl.SetSystemPromptRebuilder(func() string {
		nextPrompt, nextRefs := buildCurrentSystemPrompt()
		systemPrompt = nextPrompt
		promptSkillRefsMu.Lock()
		promptSkillRefs = append(promptSkillRefs[:0], nextRefs...)
		promptSkillRefsMu.Unlock()
		return nextPrompt
	})
	repl.SetAutoMemory(autoMem)
	repl.SetAutoMemoryFiles(autoFiles)
	repl.SetProjectMemoryLoader(projectMemoryLoader)
	repl.SetSystemPromptBuilder(func(task, agentType string) string {
		remoteAgentsInfo := ""
		if a2aRegistry != nil {
			if instances := a2aRegistry.CachedInstances(); len(instances) > 0 {
				remoteAgentsInfo = a2a.FormatRemoteAgents(instances, buildRemoteAgentMeta())
			}
		}
		return agentruntime.BuildSubAgentSystemPrompt(agentruntime.SubAgentPromptContext{
			Cfg:              cfg,
			WorkingDir:       workingDir,
			Registry:         registry,
			CommandMgr:       commandMgr,
			GlobalAutoMem:    autoMem,
			ProjectAutoMem:   projectAutoMem,
			GitStatus:        func() string { return gitStatus },
			RemoteAgentsInfo: func() string { return remoteAgentsInfo },
		}, task, agentType)
	})
	repl.SetSubAgentManager(subMgr, prov, registry)
	repl.SetAskUserTool(registry)
	repl.SetCommandPane(registry, workingDir)
	repl.SetKnight(knightAgent)
	// Show knight status hint at startup (conflict takes priority, then general status)
	if knightConflictHint != "" {
		repl.SetKnightStartupHint(knightConflictHint)
	} else if cfg.Knight().Enabled {
		repl.SetKnightStartupHint("Knight auto-evolution is enabled. Use /knight off to disable.")
	} else {
		repl.SetKnightStartupHint("Knight is disabled. Use /knight on to enable.")
	}
	trace.Mark("wire repl dependencies")

	// Register task, cron, plan mode, config, and send_message tools
	taskMgr := task.NewManager()
	repl.SetTaskManager(taskMgr, registry)

	cronScheduler := agentruntime.NewSessionCronScheduler(resumeID, workingDir, nil) // enqueue callback wired by SetCronScheduler
	repl.SetCronScheduler(cronScheduler, registry)
	repl.SetPlanModeTools(registry)
	repl.SetSendMessageTool(subMgr, registry)
	repl.SetTaskOutputTool(subMgr, registry)
	trace.Mark("register repl tools")

	// Register swarm tools
	swarmAgentFactory := func(prov provider.Provider, tools interface{}, systemPrompt string, maxTurns int) swarm.AgentRunner {
		reg, _ := tools.(*tool.Registry)
		if reg == nil {
			reg = registry // fallback to the main registry
		}
		a := agent.NewAgent(prov, reg, systemPrompt, maxTurns)
		a.SetWorkingDir(ag.WorkingDir())
		return a
	}
	swarmToolBuilder := func(_ []string) interface{} {
		cloned := registry.Clone() // each teammate gets independent tool instances
		// Unconditionally remove tools that teammates must never use.
		for _, name := range []string{
			"ask_user", "spawn_agent", "wait_agent", "list_agents",
			"teammate_spawn", "teammate_shutdown", "team_create", "team_delete",
		} {
			cloned.Unregister(name)
		}
		return cloned
	}
	swarmMgr := swarm.NewManager(cfg.Swarm, prov, swarmAgentFactory, swarmToolBuilder)
	swarmMgr.SetWorkingDir(ag.WorkingDir())
	swarmMgr.SetUsageHandler(repl.SessionUsageHandler())
	swarmMgr.SetSystemPromptBuilder(func(name, teamName, wd string) string {
		remoteAgentsInfo := ""
		if a2aRegistry != nil {
			if instances := a2aRegistry.CachedInstances(); len(instances) > 0 {
				remoteAgentsInfo = a2a.FormatRemoteAgents(instances, buildRemoteAgentMeta())
			}
		}
		return agentruntime.BuildTeammateSystemPrompt(agentruntime.SubAgentPromptContext{
			Cfg:              cfg,
			WorkingDir:       wd,
			Registry:         registry,
			CommandMgr:       commandMgr,
			GlobalAutoMem:    autoMem,
			ProjectAutoMem:   projectAutoMem,
			GitStatus:        func() string { return detectGitStatus(wd) },
			RemoteAgentsInfo: func() string { return remoteAgentsInfo },
		}, name, teamName)
	})
	repl.SetSwarmManager(swarmMgr, registry)
	repl.SetACPClientManager(acpClientMgr)
	trace.Mark("setup swarm")

	// Start webui for TUI mode (session browser + webchat)
	webuiSrv := webui.NewServer(cfg)
	webuiSrv.SetSessionStore(store, workingDir)
	trace.Mark("create webui")

	// Wire MCP status for webui config page
	webuiSrv.SetMCPStatusFn(func() map[string]webui.MCPRuntimeStatus {
		return mcpSnapshotToWebUI(mcpMgr.Snapshot())
	})

	// Wire A2A discover for webui config page
	webuiSrv.SetA2ADiscoverFn(func() []webui.A2ADiscoveredInstance {
		if a2aRegistry == nil {
			return nil
		}
		instances, err := a2aRegistry.Discover()
		if err != nil {
			return nil
		}
		return a2aInstancesToWebUI(instances)
	})

	// Wire IM status for webui config page
	if imMgr != nil {
		webuiSrv.SetIMStatusFn(func() []webui.IMRuntimeStatus {
			return imSnapshotToWebUI(imMgr.Snapshot(), cfg)
		})
	}

	// Wire Knight status for webui config page
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

	// Create TUI chat bridge: webchat messages → TUI event loop
	tuiBridge := webui.NewTUIChatBridge(ag, &tuiWebchatSender{repl: repl})
	webuiSrv.SetChatBridge(tuiBridge)
	repl.SetWebUIBridge(tuiBridge)

	// Wire WebUI restart: send remoteRestartMsg into the Bubble Tea event
	// loop so the TUI cleanly shuts down and execRestarts (same mechanism
	// as IM /restart and TUI /restart slash command).
	webuiSrv.SetRestartFn(func() {
		repl.InjectRestart()
	})

	trace.Mark("wire webui callbacks")

	actualAddr, webuiErr := webuiSrv.Start("127.0.0.1:0")
	if webuiErr != nil {
		// Silently continue — webui is optional
		debug.Log("root", "webui failed to start: %v", webuiErr)
	} else {
		defer webuiSrv.Close()
		// Schedule the URL display for after TUI is ready (see repl startup goroutine)
		repl.SetWebUIReadyAddr(actualAddr, webuiSrv.Token())
		// Expose runtime status via /api/status
		repl.SetWorkingDir(workingDir)
		webuiSrv.SetStatusFn(repl.RuntimeStatus)
		// Write port file for external process discovery
		runfile.Write(runfile.PortFile{
			Addr:      actualAddr,
			Token:     webuiSrv.Token(),
			PID:       os.Getpid(),
			SessionID: resumeID,
			Workspace: workingDir,
			Mode:      cfg.DefaultMode,
		})
		defer runfile.Remove(resumeID)
		// Ensure cleanup on syscall.Exec restart (defers don't fire on exec)
		repl.SetPreExecCleanup(func() { runfile.Remove(resumeID) })
	}
	trace.Mark("start webui")

	if resumeID != "" {
		repl.SetResumeID(resumeID)
	}
	if replPendingSessionLock != nil {
		repl.SetSessionLock(replPendingSessionLock)
	}

	// Wire config tool UI notify — sync TUI state after provider changes
	core.SetConfigUINotify(func() {
		repl.OnConfigProviderChanged()
	})

	trace.Mark("before repl run")
	return repl.Run()
}

type startupTrace struct {
	name  string
	start time.Time
	last  time.Time
}

func newStartupTrace(name string) *startupTrace {
	now := time.Now()
	debug.Log("root", "startup timing %s start pid=%d", name, os.Getpid())
	return &startupTrace{name: name, start: now, last: now}
}

func (t *startupTrace) Mark(label string) {
	if t == nil {
		return
	}
	now := time.Now()
	debug.Log("root", "startup timing %s %-44s delta=%s total=%s", t.name, label, now.Sub(t.last).Round(time.Millisecond), now.Sub(t.start).Round(time.Millisecond))
	t.last = now
}

// tuiWebchatSender implements webui.WebchatMessageSender by routing webchat
// messages into the TUI bubbletea event loop.
type tuiWebchatSender struct {
	repl *tui.REPL
}

func (s *tuiWebchatSender) SendWebchatMessage(text string) {
	if s.repl == nil {
		return
	}
	s.repl.InjectWebchatMessage(text)
}

// a2aAPIKey resolves the A2A API key from config.
func a2aAPIKey(cfg *config.Config) string {
	return cfg.A2A.EffectiveAPIKey()
}

// startA2AServer starts the A2A HTTP server, registers this instance in the local
// registry, discovers other running instances, and registers cross-instance MCP tools.
func parseA2ATimeout(s string) time.Duration {
	if s == "" {
		return 5 * time.Minute
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 5 * time.Minute
	}
	return d
}

func startA2AServer(cfg *config.Config, ag *agent.Agent, reg *tool.Registry, workingDir string) (*a2a.Server, *a2a.Registry, *a2a.TaskHandler, error) {
	a2aReg, err := a2a.NewRegistry()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("a2a registry: %w", err)
	}

	handler := a2a.NewTaskHandler(workingDir, ag, reg,
		a2a.WithMaxTasks(cfg.A2A.MaxTasks),
		a2a.WithTimeout(parseA2ATimeout(cfg.A2A.TaskTimeout)),
	)

	srv := a2a.NewServer(a2a.ServerConfig{
		Host:    cfg.A2A.Host,
		Port:    cfg.A2A.Port,
		APIKey:  a2aAPIKey(cfg),
		APIKeys: cfg.A2A.Auth.APIKeys,
	}, handler)

	// Wire OAuth2/OIDC token validation if configured
	if cfg.A2A.Auth.OAuth2 != nil {
		oc := cfg.A2A.Auth.OAuth2
		_, _, clientID, _, _ := auth.ResolveA2AAuth(oc.Provider, oc.ClientID, oc.IssuerURL, oc.Scopes)
		issuerURL := oc.IssuerURL
		if issuerURL == "" && oc.Provider != "" {
			if p := auth.ResolveProviderPreset(oc.Provider); p != nil {
				issuerURL = p.TokenURL
			}
		}
		if issuerURL != "" && clientID != "" {
			tv, err := auth.NewTokenValidator(clientID, issuerURL,
				auth.WithHMACSecret(cfg.A2A.Auth.HMACSecret),
				auth.WithValidIssuers(cfg.A2A.Auth.ValidIssuers),
			)
			if err != nil {
				srv.Stop()
				return nil, nil, nil, fmt.Errorf("a2a oauth2: %w", err)
			}
			srv.SetTokenValidator(tv)
		}
	}
	if cfg.A2A.Auth.OIDC != nil {
		oc := cfg.A2A.Auth.OIDC
		_, _, clientID, _, _ := auth.ResolveA2AAuth(oc.Provider, oc.ClientID, oc.IssuerURL, oc.Scopes)
		issuerURL := oc.IssuerURL
		if issuerURL == "" && oc.Provider != "" {
			if p := auth.ResolveProviderPreset(oc.Provider); p != nil && p.OIDCDiscovery != "" {
				issuerURL = p.OIDCDiscovery
			}
		}
		if issuerURL != "" && clientID != "" {
			tv, err := auth.NewTokenValidator(clientID, issuerURL,
				auth.WithHMACSecret(cfg.A2A.Auth.HMACSecret),
				auth.WithValidIssuers(cfg.A2A.Auth.ValidIssuers),
			)
			if err != nil {
				srv.Stop()
				return nil, nil, nil, fmt.Errorf("a2a oidc: %w", err)
			}
			srv.SetTokenValidator(tv)
		}
	}
	if cfg.A2A.Auth.MTLS != nil {
		mtlsCfg := &auth.MTLSConfig{
			CertFile: cfg.A2A.Auth.MTLS.CertFile,
			KeyFile:  cfg.A2A.Auth.MTLS.KeyFile,
			CAFile:   cfg.A2A.Auth.MTLS.CAFile,
		}
		tlsCfg, err := mtlsCfg.BuildTLSConfig()
		if err != nil {
			srv.Stop()
			return nil, nil, nil, fmt.Errorf("a2a mtls: %w", err)
		}
		srv.SetTLSConfig(tlsCfg)
	}

	if err := srv.Start(); err != nil {
		return nil, nil, nil, fmt.Errorf("a2a start: %w", err)
	}

	// Register this instance in the shared local registry.
	instance := a2a.InstanceInfo{
		ID:           a2a.GenerateInstanceID(),
		PID:          os.Getpid(),
		Workspace:    workingDir,
		StartedAt:    time.Now().Format(time.RFC3339),
		Endpoint:     srv.Endpoint(),
		AgentCardURL: srv.Endpoint() + "/.well-known/agent.json",
		Status:       "ready",
	}
	a2aReg.SetInterfaces(cfg.A2A.Interfaces)
	if err := a2aReg.Register(instance); err != nil {
		srv.Stop()
		return nil, nil, nil, fmt.Errorf("a2a register: %w", err)
	}

	// Register MCP bridge tools for external MCP clients (Claude, Cursor, etc.)
	// that don't speak A2A natively. These 4 tools let any MCP client discover
	// and interact with this ggcode instance.
	bridgeClient := a2a.NewClient(srv.Endpoint(), a2aAPIKey(cfg))
	for _, t := range a2a.MCPBridgeTools(bridgeClient) {
		_ = reg.Register(t)
	}

	// Register A2A remote tool for agent-to-agent communication.
	// This lets this ggcode agent discover and call other running ggcode instances.
	remoteTool := a2a.NewRemoteTool(a2aReg, a2aAPIKey(cfg))
	_ = reg.Register(remoteTool)

	// Periodically refresh the remote instance cache so new instances are discovered.
	safego.Go("root.a2a.refreshCache", func() {
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			remoteTool.RefreshCache()
		}
	})

	debug.Log("root", "A2A server started at %s", srv.Endpoint())
	return srv, a2aReg, handler, nil
}

// reportDiscoveredInstances logs discovered ggcode instances at startup.
func reportDiscoveredInstances(a2aReg *a2a.Registry) {
	others, err := a2aReg.Discover()
	if err != nil {
		debug.Log("a2a", "discover failed: %v", err)
		return
	}
	if len(others) > 0 {
		debug.Log("a2a", "discovered %d other ggcode instance(s)", len(others))
		for _, inst := range others {
			name := filepath.Base(inst.Workspace)
			debug.Log("a2a", "  - %s → %s", name, inst.Endpoint)
		}
	} else {
		debug.Log("a2a", "no other instances found")
	}
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
) (string, []string, string, []string, *commands.Manager) {
	var (
		projectMem   string
		projectFiles []string
		autoContent  string
		autoFiles    []string
		commandMgr   *commands.Manager
	)

	var wg sync.WaitGroup
	wg.Add(3)

	safego.Go("root.startup.projectMem", func() {
		defer wg.Done()
		projectMem, projectFiles, _ = memory.LoadProjectMemory(workingDir)
	})

	safego.Go("root.startup.autoMem", func() {
		defer wg.Done()
		autoContent, autoFiles, _ = autoMem.LoadAll()
	})

	safego.Go("root.startup.commands", func() {
		defer wg.Done()
		commandMgr = commands.NewManager(workingDir)
	})

	wg.Wait()

	if commandMgr == nil {
		commandMgr = commands.NewManager(workingDir)
	}

	return projectMem, projectFiles, autoContent, autoFiles, commandMgr
}

func loadInteractiveStartupAssets(
	workingDir string,
	autoMem *memory.AutoMemory,
	projectAutoMem *memory.AutoMemory,
) (string, []string, string, *commands.Manager) {
	trace := newStartupTrace("root.startup-assets")
	defer trace.Mark("return")

	var (
		autoContent        string
		autoFiles          []string
		projectAutoContent string
		commandMgr         *commands.Manager
	)

	var wg sync.WaitGroup
	wg.Add(3)

	safego.Go("root.interactive.autoMem", func() {
		defer wg.Done()
		start := time.Now()
		autoContent, autoFiles, _ = autoMem.LoadAll()
		debug.Log("root", "startup timing root.startup-assets autoMem.LoadAll files=%d duration=%s", len(autoFiles), time.Since(start).Round(time.Millisecond))
	})

	safego.Go("root.interactive.projectAutoMem", func() {
		defer wg.Done()
		start := time.Now()
		if projectAutoMem != nil {
			projectAutoContent, _, _ = projectAutoMem.LoadAll()
		}
		debug.Log("root", "startup timing root.startup-assets projectAutoMem.LoadAll enabled=%v bytes=%d duration=%s", projectAutoMem != nil, len(projectAutoContent), time.Since(start).Round(time.Millisecond))
	})

	safego.Go("root.interactive.commands", func() {
		defer wg.Done()
		start := time.Now()
		commandMgr = commands.NewManager(workingDir)
		cmdCount := 0
		if commandMgr != nil {
			cmdCount = len(commandMgr.Commands())
		}
		debug.Log("root", "startup timing root.startup-assets commands.NewManager commands=%d duration=%s", cmdCount, time.Since(start).Round(time.Millisecond))
	})

	wg.Wait()
	trace.Mark("wait parallel assets")

	if commandMgr == nil {
		start := time.Now()
		commandMgr = commands.NewManager(workingDir)
		debug.Log("root", "startup timing root.startup-assets commands.NewManager fallback duration=%s", time.Since(start).Round(time.Millisecond))
	}

	return autoContent, autoFiles, projectAutoContent, commandMgr
}

func buildSkillsSystemPrompt(skills []*commands.Command) string {
	prompt, _ := buildSkillsSystemPromptWithPromptRefs(skills)
	return prompt
}

func buildSkillsSystemPromptWithPromptRefs(skills []*commands.Command) (string, []string) {
	var lines []string
	lines = append(lines,
		"Use the skill tool to load reusable workflows when they clearly match the user's task.",
		"",
		"When a listed skill is a close match, invoke the skill tool before continuing.",
		"Do not mention a skill without calling the skill tool.",
		"Do not use the skill tool for built-in CLI commands like /help or /clear.",
		"",
		"Available skills:",
	)
	const maxChars = 4000
	const maxDescChars = 180
	total := 0
	included := 0
	mcpSkillCount := 0
	mcpServers := make(map[string]struct{})
	var promptSkillRefs []string
	for _, skill := range prioritizedSkillsForPrompt(skills) {
		name := strings.TrimSpace(skill.Name)
		if name == "" {
			continue
		}
		if skill.LoadedFrom == commands.LoadedFromMCP || skill.Source == commands.SourceMCP {
			mcpSkillCount++
			if server, _, ok := strings.Cut(name, ":"); ok {
				server = strings.TrimSpace(server)
				if server != "" {
					mcpServers[server] = struct{}{}
				}
			}
			continue
		}
		desc := strings.TrimSpace(skill.Description)
		if when := strings.TrimSpace(skill.WhenToUse); when != "" {
			if desc != "" {
				desc += " - "
			}
			desc += when
		}
		if len(desc) > maxDescChars {
			desc = desc[:maxDescChars-1] + "..."
		}
		line := fmt.Sprintf("- %s: %s", name, desc)
		if total+len(line)+1 > maxChars {
			break
		}
		lines = append(lines, line)
		total += len(line) + 1
		included++
		if ref := skillPromptExposureRef(skill); ref != "" {
			promptSkillRefs = append(promptSkillRefs, ref)
		}
	}
	if mcpSkillCount > 0 {
		servers := sortedStringKeys(mcpServers)
		summary := fmt.Sprintf("- MCP prompt-backed skills are also available from connected MCP servers (%d total", mcpSkillCount)
		if len(servers) > 0 {
			summary += "; servers: " + strings.Join(servers, ", ")
		}
		summary += ")."
		if total+len(summary)+1 <= maxChars {
			lines = append(lines, summary)
			total += len(summary) + 1
		}
	}
	if hidden := countModelVisibleSkills(skills) - included - mcpSkillCount; hidden > 0 {
		lines = append(lines, fmt.Sprintf("- ... and %d more skills available via the skill tool and /skills", hidden))
	}
	return strings.Join(lines, "\n"), promptSkillRefs
}

func skillPromptExposureRef(skill *commands.Command) string {
	if skill == nil || skill.LoadedFrom != commands.LoadedFromSkills {
		return ""
	}
	name := strings.TrimSpace(skill.Name)
	if name == "" {
		return ""
	}
	switch skill.Source {
	case commands.SourceProject:
		return knight.FormatSkillRefForDisplay("project", name)
	case commands.SourceUser:
		return knight.FormatSkillRefForDisplay("global", name)
	default:
		return ""
	}
}

func prioritizedSkillsForPrompt(skills []*commands.Command) []*commands.Command {
	out := make([]*commands.Command, 0, len(skills))
	for _, skill := range skills {
		if skill == nil || skill.DisableModelInvocation || !skill.Enabled || strings.TrimSpace(skill.Name) == "" {
			continue
		}
		out = append(out, skill)
	}
	sort.SliceStable(out, func(i, j int) bool {
		iBundled := out[i].LoadedFrom == commands.LoadedFromBundled || out[i].Source == commands.SourceBundled
		jBundled := out[j].LoadedFrom == commands.LoadedFromBundled || out[j].Source == commands.SourceBundled
		if iBundled != jBundled {
			return iBundled
		}
		iMCP := out[i].LoadedFrom == commands.LoadedFromMCP || out[i].Source == commands.SourceMCP
		jMCP := out[j].LoadedFrom == commands.LoadedFromMCP || out[j].Source == commands.SourceMCP
		if iMCP != jMCP {
			return !iMCP
		}
		return out[i].Name < out[j].Name
	})
	return out
}

func sortedStringKeys(values map[string]struct{}) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, 0, len(values))
	for value := range values {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func countModelVisibleSkills(skills []*commands.Command) int {
	count := 0
	for _, skill := range skills {
		if skill != nil && !skill.DisableModelInvocation && skill.Enabled && strings.TrimSpace(skill.Name) != "" {
			count++
		}
	}
	return count
}

func buildMCPSkillCommands(snapshots []tool.MCPServerSnapshot) []*commands.Command {
	out := make([]*commands.Command, 0)
	for _, snap := range snapshots {
		for _, promptName := range snap.PromptNames {
			name := strings.TrimSpace(snap.Name + ":" + promptName)
			if name == ":" || strings.TrimSpace(promptName) == "" || strings.TrimSpace(snap.Name) == "" {
				continue
			}
			out = append(out, &commands.Command{
				Name:          name,
				Description:   fmt.Sprintf("MCP prompt from %s", snap.Name),
				WhenToUse:     fmt.Sprintf("Use when the %s MCP prompt %q matches the user's request.", snap.Name, promptName),
				Source:        commands.SourceMCP,
				LoadedFrom:    commands.LoadedFromMCP,
				UserInvocable: true,
			})
		}
	}
	return out
}

func toTuiMCPInfos(infos []plugin.MCPServerInfo) []tui.MCPInfo {
	out := make([]tui.MCPInfo, 0, len(infos))
	for _, info := range infos {
		out = append(out, tui.MCPInfo{
			Name:          info.Name,
			ToolNames:     info.ToolNames,
			PromptNames:   info.PromptNames,
			ResourceNames: info.ResourceNames,
			Connected:     info.Status == plugin.MCPStatusConnected,
			Pending:       info.Status == plugin.MCPStatusPending,
			Error:         info.Error,
			Transport:     info.Transport,
			Migrated:      info.Migrated,
		})
	}
	return out
}
