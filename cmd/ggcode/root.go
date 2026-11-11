package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"golang.org/x/term"

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
	"github.com/topcheer/ggcode/internal/permission"
	"github.com/topcheer/ggcode/internal/plugin"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/restart"
	"github.com/topcheer/ggcode/internal/runfile"
	"github.com/topcheer/ggcode/internal/util"

	"github.com/topcheer/ggcode/internal/safego"
	"github.com/topcheer/ggcode/internal/session"
	"github.com/topcheer/ggcode/internal/subagent"
	"github.com/topcheer/ggcode/internal/swarm"
	"github.com/topcheer/ggcode/internal/task"
	"github.com/topcheer/ggcode/internal/tool"
	"github.com/topcheer/ggcode/internal/tui"
	"github.com/topcheer/ggcode/internal/update"
	"github.com/topcheer/ggcode/internal/vcs"
	"github.com/topcheer/ggcode/internal/version"
	"github.com/topcheer/ggcode/internal/webui"
)

var persistSessionFlag bool

func NewRootCmd() *cobra.Command {
	var cfgFile string
	var resumeID string
	var pipePrompt string
	var allowedTools []string
	var allowedDirs []string
	var readOnlyAllowedDirs []string
	var bypassFlag bool
	var plainFlag bool
	var outputPath string
	var outputSchemaPath string

	var tokenBudgetFlag int64
	var timeBudgetFlag time.Duration
	var helperManifest string

	cmd := &cobra.Command{
		Use:              "ggcode",
		Short:            "AI coding assistant",
		Long:             "ggcode is a terminal-based AI coding agent powered by LLMs.",
		SilenceUsage:     true,
		TraverseChildren: true,
		RunE: func(cmd *cobra.Command, args []string) error {

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

			cfg, err := loadConfigForRun(cfgFile, &adhocLLM)
			if err != nil {
				return err
			}

			if adhocLLM.adhocActive() {
				cfgFile = ""
			}

			if tokenBudgetFlag > 0 {
				cfg.SessionTokenBudget = tokenBudgetFlag
			}
			if timeBudgetFlag > 0 {
				cfg.SessionTimeBudget = timeBudgetFlag
			}
			if persistWarnings, _, err := mcp.PersistUserClaudeServers(cfg); err != nil {
				return fmt.Errorf("persisting Claude MCP servers: %w", err)
			} else {
				for _, w := range persistWarnings {
					fmt.Fprintln(os.Stderr, "warning:", w)
				}
			}

			if plainFlag {
				if err := os.Setenv("NO_COLOR", "1"); err != nil {
					fmt.Fprintln(os.Stderr, "warning: failed to set NO_COLOR:", err)
				}
			}

			if pipePrompt != "" {
				code := RunPipe(cfg, cfgFile, pipePrompt, allowedTools, allowedDirs, outputPath, bypassFlag, readOnlyAllowedDirs, outputSchemaPath)
				if code != 0 {
					debug.Close()
					os.Exit(code)
				}
				return nil
			}

			if !bypassFlag {
				wd, _ := os.Getwd()
				if home, err := os.UserHomeDir(); err == nil && wd == home {
					lang := tui.LangEnglish
					if cfg.Language != "" {
						lang = tui.NormalizeLanguage(cfg.Language)
					}

					var confirmed bool
					if plainFlag || isDumbTerminal() {
						confirmed = tui.ConfirmHomeDirPlain(lang)
					} else {
						confirmed = tui.ConfirmHomeDir(lang)
					}
					if !confirmed {
						fmt.Fprintln(os.Stderr, "Please cd into a project directory and run ggcode again.")
						return nil
					}
				}
			}

			if !bypassFlag && cfg.NeedsOnboard() {
				if err := runOnboardAndRestart(cfg); err != nil {
					fmt.Fprintf(os.Stderr, "Onboard failed: %v\n", err)
					os.Exit(1)
				}
				return nil
			}

			resumePicker, _ := cmd.Flags().GetBool("resume-picker")
			newSession, _ := cmd.Flags().GetBool("new-session")
			if resumePicker {

				sessionDir, _ := session.DefaultDir()
				store, err := session.NewJSONLStore(sessionDir)
				if err != nil {
					return fmt.Errorf("creating session store for picker: %w", err)
				}
				selectedID, err := pickResumeSession(store, session.CurrentWorkspacePath())
				if err != nil {
					return err
				}
				if selectedID == "" {

					return run(cfg, cfgFile, "__new__", bypassFlag, plainFlag)
				}
				return run(cfg, cfgFile, selectedID, bypassFlag, plainFlag)
			}
			if newSession {
				return run(cfg, cfgFile, "__new__", bypassFlag, plainFlag)
			}
			return run(cfg, cfgFile, resumeID, bypassFlag, plainFlag)
		},
	}

	cmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file path")

	cmd.Flags().Bool("version", false, "print version and exit")
	cmd.Flags().BoolP("v", "v", false, "shorthand for --version")
	cmd.Flags().StringVar(&resumeID, "resume", "", "resume a previous session by ID")
	cmd.Flags().Bool("resume-picker", false, "interactively select a session to resume")
	cmd.Flags().Bool("new-session", false, "skip auto-loading the most recent unlocked session; always start fresh")
	cmd.Flags().StringVarP(&pipePrompt, "prompt", "p", "", "pipe mode: non-interactive execution with a prompt")
	cmd.Flags().StringArrayVar(&allowedTools, "allowedTools", nil, "tools to allow in pipe mode (can be repeated)")
	cmd.Flags().StringArrayVar(&allowedDirs, "allowedDir", nil, "override writable sandbox directory for pipe mode (can be repeated)")
	_ = cmd.Flags().MarkHidden("allowedDir")
	cmd.Flags().StringArrayVar(&readOnlyAllowedDirs, "readOnlyAllowedDir", nil, "extra read-only sandbox directory for pipe mode (can be repeated)")
	_ = cmd.Flags().MarkHidden("readOnlyAllowedDir")
	cmd.Flags().BoolVar(&bypassFlag, "bypass", false, "start in bypass permission mode (auto-approve safe ops, warn on dangerous)")
	cmd.Flags().BoolVar(&plainFlag, "plain", false, "linear frontend: no TUI frames, line-oriented IO for dumb terminals/screen readers (sets NO_COLOR; TERM=dumb auto-selects)")
	cmd.Flags().BoolVar(&persistSessionFlag, "persist-session", false, "persist each message of a --plain session to the session store (write-only; no auto-resume)")
	cmd.Flags().StringVar(&outputPath, "output", "", "output file path (default: stdout)")
	cmd.Flags().StringVar(&outputSchemaPath, "output-schema", "", "path to a JSON Schema the final response must conform to (structured outputs; supported endpoints constrain decoding, others fall back to prompt guidance)")

	cmd.Flags().Int64Var(&tokenBudgetFlag, "token-budget", 0, "per-run session token budget override (input+output tokens; 0 = use config)")
	cmd.Flags().DurationVar(&timeBudgetFlag, "time-budget", 0, "per-run wall-clock soft budget override (e.g. 10m, 1h; 0 = use config)")

	cmd.Flags().StringVar(&adhocLLM.BaseURL, "llm-base-url", "", "ad-hoc provider mode: base URL of the endpoint (env "+adhocEnvBaseURL+")")
	cmd.Flags().StringVar(&adhocLLM.Model, "llm-model", "", "ad-hoc provider mode: model name (env "+adhocEnvModel+")")
	cmd.Flags().StringVar(&adhocLLM.APIKey, "llm-api-key", "", "ad-hoc provider mode: API key (env "+adhocEnvAPIKey+")")
	cmd.Flags().StringVar(&adhocLLM.Protocol, "llm-protocol", "", "ad-hoc provider mode: wire protocol override - openai|anthropic|gemini (env "+adhocEnvProtocol+"; default: inferred from URL)")
	cmd.Flags().IntVar(&adhocLLM.ContextWindow, "llm-context-window", 0, "ad-hoc provider mode: context window override (env "+adhocEnvContextWindow+")")
	cmd.Flags().IntVar(&adhocLLM.MaxTokens, "llm-max-tokens", 0, "ad-hoc provider mode: max output tokens override (env "+adhocEnvMaxTokens+")")
	cmd.Flags().BoolVar(&adhocLLM.RetryAlways, "llm-retry-always", false, "ad-hoc provider mode: retry EVERY LLM error indefinitely - auth/404/quota/overflow all retried, attempt and backoff caps lifted; only Ctrl-C/cancellation stops (env "+adhocEnvRetryAlways+"=true/1)")

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

	versionCmd := &cobra.Command{
		Use:   "version",
		Short: "Print the ggcode version",
		Run: func(cmd *cobra.Command, _ []string) {
			fmt.Fprintln(cmd.OutOrStdout(), version.Display())
		},
	}
	cmd.AddCommand(versionCmd)

	cmd.AddCommand(newConfigCmd(&cfgFile))

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
	cmd.AddCommand(newMCPCmd(&cfgFile))
	cmd.AddCommand(newAuditCmd())
	cmd.AddCommand(newPluginCmd(&cfgFile))
	cmd.AddCommand(newIMCmd(&cfgFile))
	cmd.AddCommand(newDaemonCmd(&cfgFile))
	cmd.AddCommand(newLLMProbeCmd(&cfgFile))
	cmd.AddCommand(newACPCommand(&cfgFile))
	cmd.AddCommand(newStatusCmd())
	cmd.AddCommand(newBOMCmd())
	cmd.AddCommand(newReportCmd())
	cmd.AddCommand(newLoginCmd(&cfgFile))
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

func run(cfg *config.Config, cfgFile, resumeID string, bypass, plain bool) error {
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

	if n, sweepErr := util.SweepStaleTempFiles(filepath.Join(workingDir, ".ggcode"), time.Hour); sweepErr == nil && n > 0 {
		debug.Log("root", "swept %d stale atomic-write temp file(s)", n)
	}

	if n, sweepErr := util.SweepStaleTempFiles(config.ConfigDir(), time.Hour); sweepErr == nil && n > 0 {
		debug.Log("root", "swept %d stale atomic-write temp file(s) in config dir", n)
	}

	safego.Go("startup.todoSweep", func() {
		tool.SweepStaleTodoFiles(30 * 24 * time.Hour)
	})

	policy := agentruntime.BuildInteractivePermissionPolicy(cfg, workingDir, bypass)
	mode := agentruntime.InteractivePermissionMode(cfg, bypass)
	trace.Mark("permission policy")

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
	pluginMgr := core.PluginManager
	autoMem := core.AutoMemory
	projectAutoMem := core.ProjectAutoMem
	saveMemoryTool := core.SaveMemoryTool
	startupAssets := core.StartupAssets

	startupNotices := core.StartupNotices
	// #5367: post-exec self-check. If this process was started by a restart
	// exec, verify the running image matches the recorded intent and surface
	// the actually-running revision (loud banner on mismatch).
	if notice, _ := restart.StartupVerifyNotice(); notice != "" {
		startupNotices = append(startupNotices, notice)
		restart.ConsumeIntent()
	}
	autoFiles := startupAssets.AutoFiles
	commandMgr := startupAssets.CommandManager
	trace.Mark("build interactive runtime core")

	projectMemoryLoader := func() (string, []string, error) {
		files, err := memory.ProjectMemoryFilesForPath(workingDir)
		if err != nil {
			return "", nil, err
		}
		return memory.BuildProjectMemoryHint(files, workingDir), files, nil
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
	}, nil, nil)
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

	tools := registry.List()
	toolNames := make([]string, len(tools))
	for i, t := range tools {
		toolNames[i] = t.Name()
	}
	trace.Mark("collect tool names")

	var a2aRegistry *a2a.Registry
	var lanchatHub *lanchat.Hub

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

	agentruntime.InitGlobalSectionCollector(workingDir)
	defer agentruntime.StopGlobalSectionCollector()

	// #shift-handoff: handoff brief of the most recent prior session in
	// this workspace. Computed once after session selection (below - the
	// store/session do not exist yet at first prompt build) and appended
	// to every system prompt rebuild. Empty for resumed sessions (full
	// history already restored) and fresh workspaces.
	var shiftHandoffBrief string

	buildCurrentSystemPrompt := func() (string, []string) {
		// #5339: same-workspace active agents snapshot. lanchatHub is a
		// closure variable assigned later in startup; the first build may
		// see nil (empty section, no drift), later rebuilds pick it up.
		sharedAgents := ""
		if lanchatHub != nil {
			sharedAgents = lanchat.FormatWorkspaceAgents(lanchatHub.Participants(), workingDir, lanchatHub.NodeID())
		}
		prompt, promptRefs := agentruntime.BuildInteractiveSystemPromptWithPromptRefs(cfg, workingDir, mode, registry, commandMgr, autoMem, projectAutoMem, detectGitStatus(workingDir), "", sharedAgents)
		if shiftHandoffBrief != "" {
			prompt += "\n\n" + shiftHandoffBrief
		}
		return prompt, promptRefs
	}
	systemPrompt, promptSkillRefs := buildCurrentSystemPrompt()
	trace.Mark(fmt.Sprintf("build initial system prompt skills=%d bytes=%d", len(promptSkillRefs), len(systemPrompt)))

	skillTool.SystemPromptBuilder = func(task, agentType string) string {
		remoteAgentsInfo := ""
		if a2aRegistry != nil {
			if instances := a2aRegistry.CachedInstances(); len(instances) > 0 {
				remoteAgentsInfo = a2a.FormatRemoteAgents(instances, buildRemoteAgentMeta())
			}
		}
		return agentruntime.BuildSubAgentSystemPrompt(agentruntime.SubAgentPromptContext{
			Cfg:            cfg,
			WorkingDir:     workingDir,
			Registry:       registry,
			CommandMgr:     commandMgr,
			GlobalAutoMem:  autoMem,
			ProjectAutoMem: projectAutoMem,
			// #4570: lazy per-build detection, identical to the skillTool
			// sub-agent builder (#4502) - the old closure returned the startup
			// snapshot, so repl-created sub-agents carried a stale branch name
			// and dirty count after any mid-session commit/checkout/worktree.

			GitStatus:        func() string { return detectGitStatus(workingDir) },
			RemoteAgentsInfo: func() string { return remoteAgentsInfo },
		}, task, agentType)
	}

	_ = registry.Register(skillTool)
	trace.Mark("register skill tool")

	_ = registry.Register(tool.CreateSkillTool{CommandMgr: commandMgr, WorkingDir: workingDir})
	trace.Mark("register create_skill tool")

	if os.Getenv("GGCODE_TRIAL_FORK") != "" {
		_ = registry.Register(&tool.TrialForkTool{
			Provider:     prov,
			Tools:        registry,
			AgentFactory: skillAgentFactory,
			WorkingDir:   workingDir,
		})
		trace.Mark("register trial_fork tool")
	}

	var promptSkillRefsMu sync.RWMutex
	currentPromptSkillRefs := func() []string {
		promptSkillRefsMu.RLock()
		defer promptSkillRefsMu.RUnlock()
		return append([]string(nil), promptSkillRefs...)
	}

	subMgr = subagent.NewManager(cfg.SubAgents)
	defer subMgr.Shutdown()
	trace.Mark("setup sub-agent manager")

	maxIter := cfg.MaxIterations
	ag = agent.NewAgent(prov, registry, systemPrompt, maxIter)
	agentruntime.RegisterCompactContextTool(registry, ag)
	agentruntime.RegisterRecallCompactedTool(registry, ag)
	core.SetConfigAgent(ag)

	// Memory v2: resident memory curator. It watches memory save/delete
	// events and runs a debounced headless curation pass (tag, merge,
	// archive stale temporal facts) through the same store APIs the
	// memory tools use. Gated by memory.curator.enabled (default on).
	if cfg.MemoryCuratorEnabled() {
		curatorProv := prov
		if m := strings.TrimSpace(cfg.Memory.Curator.Model); m != "" && m != cfg.Model {
			// ResolveProvider reads cfg.Model; Config embeds a lock so it
			// must not be copied - swap the field, resolve, restore.
			origModel := cfg.Model
			cfg.Model = m
			p2, _, perr := ResolveProvider(cfg)
			cfg.Model = origModel
			if perr == nil {
				curatorProv = p2
			} else {
				debug.Log("root", "memory curator model override failed (%v); using default provider", perr)
			}
		}
		memoryCurator := agentruntime.NewMemoryCurator(autoMem, projectAutoMem, curatorProv, agentruntime.MemoryCuratorOptions{})
		memoryCurator.Start(context.Background())
		defer memoryCurator.Stop()
		trace.Mark("start memory curator")
	}
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
			debug.Log("root", "Knight scenario record failed: %v", scenarioErr)
		}
	})
	agentruntime.ApplyResolvedLimitsToAgent(ag, resolved)
	agentruntime.ApplyVerifyConfigToAgent(ag, cfg)
	agentruntime.StartAsyncRelayModelLimitRefresh(cfg, resolved, ag, nil)
	ag.SetPermissionPolicy(policy)
	ag.SetHookConfig(cfg.Hooks)
	ag.SetAuxModel(resolved, cfg.AuxModel)
	ag.SetWorkingDir(workingDir)
	ag.SetCheckpointManager(checkpoint.NewPersistentManager(50, workingDir))
	tool.SetPreWriteHook(tool.CheckpointSaver(ag.CheckpointManager()))
	ag.SetSupportsVision(resolved.SupportsVision)
	trace.Mark("setup agent")

	store, err := session.NewDefaultStore()
	if err != nil {
		return fmt.Errorf("creating session store: %w", err)
	}
	trace.Mark("create session store")

	safego.Go("startup.repairIndex", func() {
		changed, err := store.RepairIndex()
		if err != nil {
			debug.Log("root", "async RepairIndex error: %v", err)
		} else if changed {
			debug.Log("root", "async RepairIndex completed: index updated")
		}
	})

	storeDir, _ := session.DefaultDir()
	session.CleanupStaleLocks(storeDir)

	agent.CleanupOldJournals(24 * time.Hour)

	safego.Go("startup.sessionRetention", func() {
		if n, err := store.CleanupOlderThan(time.Now().AddDate(0, 0, -90)); err != nil {
			debug.Log("session", "retention sweep failed: %v", err)
		} else if n > 0 {
			debug.Log("session", "retention sweep removed %d session(s) older than 90d", n)
		}
	})

	safego.Go("startup.subagentSweep", func() {
		wd, _ := os.Getwd()
		subagent.SweepStaleWorkspaceDirs(wd, 90*24*time.Hour)
	})

	var replPendingSessionLock *session.SessionLock

	if resumeID == "picker" {

		// #2245: mirror the conflict branch - without this reset the
		// path silently resumed with no lock held (double-writer window).

		resumeID = ""
	} else if resumeID == "__new__" {

	} else if resumeID != "" {

		lock, lockErr := session.TryAcquireSessionLock(storeDir, resumeID)
		if lockErr == nil && lock != nil && lock.Acquired() {
			replPendingSessionLock = lock
		} else if lockErr != nil {

			shortID := resumeID
			if len(shortID) > 8 {
				shortID = shortID[:8]
			}
			fmt.Fprintf(os.Stderr, "  Warning: session lock check for %s failed (%v). Starting a new session.\n", shortID, lockErr)

			resumeID = ""
		} else {
			shortID := resumeID
			if len(shortID) > 8 {
				shortID = shortID[:8]
			}
			fmt.Fprintf(os.Stderr, "  Session %s is locked by another instance. Starting a new session.\n", shortID)
			resumeID = ""
		}
	} else if resumeID == "" {

		workspace := workingDir
		sessions, err := store.ListForWorkspace(workspace)
		if err != nil {
			debug.Log("root", "ListForWorkspace error: %v", err)
		}
		if len(sessions) > 0 {
			storeDir, _ := session.DefaultDir()
			ioFailures := 0
			for _, ses := range sessions {
				lock, lockErr := session.TryAcquireSessionLock(storeDir, ses.ID)
				if lockErr != nil {

					shortID := ses.ID
					if len(shortID) > 8 {
						shortID = shortID[:8]
					}
					fmt.Fprintf(os.Stderr, "  Warning: session lock check for %s failed (%v).\n", shortID, lockErr)
					ioFailures++
					continue
				}
				if lock != nil && lock.Acquired() {
					replPendingSessionLock = lock
					resumeID = ses.ID
					trace.Mark("auto-load session")
					break
				}
			}
			if resumeID == "" {
				if ioFailures == len(sessions) {
					fmt.Fprintf(os.Stderr, "\n  %d workspace session(s) failed lock checks (I/O errors). Starting a new session.\n", ioFailures)
				} else if ioFailures > 0 {
					fmt.Fprintf(os.Stderr, "\n  All %d workspace session(s) unavailable (%d in use by other instances, %d lock-check failures). Starting a new session.\n", len(sessions), len(sessions)-ioFailures, ioFailures)
				} else {
					fmt.Fprintf(os.Stderr, "\n  All %d workspace session(s) are in use by other instances. Starting a new session.\n", len(sessions))
				}
			}
		}
	}
	// #shift-handoff: fresh sessions only (resumeID empty). A resumed
	// session already carries its full history; a brand-new session would
	// otherwise start amnesiac about the previous session in this workspace
	// (cron/one-shot runs re-reading context from zero every time).
	if resumeID == "" && !cfg.DisableHandoff {
		shiftHandoffBrief = agentruntime.BuildShiftHandoffBrief(store, workingDir, "", agentruntime.DefaultShiftHandoffWindow)
		if shiftHandoffBrief != "" {
			systemPrompt += "\n\n" + shiftHandoffBrief
			trace.Mark(fmt.Sprintf("shift handoff injected bytes=%d", len(shiftHandoffBrief)))
		}
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

	mcpInfos := toTuiMCPInfos(mcpMgr.Snapshot())
	trace.Mark("build tui mcp info")

	if plain || isDumbTerminal() {
		trace.Mark("linear frontend fork")
		code := RunLinearREPL(ag, registry, cfg, cfgFile, startupNotices)
		if code != 0 {
			debug.Close()
			os.Exit(code)
		}
		return nil
	}

	var a2aServer *a2a.Server

	var a2aTaskHandler *a2a.TaskHandler
	if !cfg.A2A.Disabled {

		a2aSrv, a2aReg, a2aHandler, a2aStopRefresh, err := startA2AServer(cfg, ag, registry, workingDir)
		if err != nil {

			debug.Log("root", "A2A server startup warning: %v", err)
			startupNotices = append(startupNotices, fmt.Sprintf(
				"A2A server failed to start - remote task dispatch and LAN visibility are DISABLED for this session. Fix the a2a config to re-enable: %v", err))
		} else {
			a2aServer = a2aSrv
			a2aRegistry = a2aReg
			a2aTaskHandler = a2aHandler

			a2aSrv.SetSkills(append(a2a.DefaultSkills(), localA2ASkills(commandMgr)...))

			a2aBgCtx, a2aBgCancel := context.WithCancel(context.Background())
			a2aReg.StartBackgroundRefresh(a2aBgCtx)
			defer func() {
				a2aBgCancel()

				a2aStopRefresh()
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

	if a2aServer != nil && !cfg.A2A.Disabled {
		chatStore := lanchat.NewStore(filepath.Join(config.ConfigDir(), "lanchat"))
		chatMode := "cli"

		lanchatHub = lanchat.NewHub(
			a2aRegistry.SelfID(),
			chatMode,
			a2aServer.Endpoint(),
			cfg.LanChat.EffectiveAPIKey(),
			chatStore,
			lanchat.DetectWorkspaceMeta(workingDir),
		)
		lanchatHub.SetRequireAgentApproval(cfg.LanChat.RequireApprovalForAgents)
		lanchatHub.SetAttachments(lanchat.NewAttachmentManager())
		lanchat.MountHandlers(a2aServer.Mux(), lanchatHub, a2aServer.Port())

		syncCtx, syncCancel := context.WithCancel(context.Background())
		defer syncCancel()
		safego.Go("lanchat.syncPeers", func() {
			syncPeers := func() {
				instances := a2aRegistry.CachedInstances()
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

			time.Sleep(3 * time.Second)
			syncPeers()

			ticker := time.NewTicker(10 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-syncCtx.Done():
					return
				case <-ticker.C:
					syncPeers()
				}
			}
		})
	}

	repl := tui.NewREPL(ag, policy, startupNotices)
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
			Workspace:        workingDir,
			EnabledAdapters:  adapters,
			RegisterInstance: false,
		})
		if err != nil {
			return fmt.Errorf("initializing IM runtime: %w", err)
		}
		imMgr = runtimeInit.Manager
		if imMgr != nil {
			defer imMgr.UnregisterInstance()
		}
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
	repl.SetRuntimeStatusProvider()
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
		promptSkillRefsMu.Lock()
		systemPrompt = nextPrompt
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
			Cfg:            cfg,
			WorkingDir:     workingDir,
			Registry:       registry,
			CommandMgr:     commandMgr,
			GlobalAutoMem:  autoMem,
			ProjectAutoMem: projectAutoMem,

			GitStatus:        func() string { return detectGitStatus(workingDir) },
			RemoteAgentsInfo: func() string { return remoteAgentsInfo },
		}, task, agentType)
	})
	repl.SetJobManager(registry.JobManager())
	repl.SetSubAgentManager(subMgr, prov, registry)
	repl.SetAskUserTool(registry)
	repl.SetCommandPane(registry, workingDir)
	repl.SetKnight(knightAgent)

	if knightConflictHint != "" {
		repl.SetKnightStartupHint(knightConflictHint)
	} else if cfg.Knight().Enabled {
		repl.SetKnightStartupHint("Knight auto-evolution is enabled. Use /knight off to disable.")
	} else {
		repl.SetKnightStartupHint("Knight is disabled. Use /knight on to enable.")
	}
	trace.Mark("wire repl dependencies")

	taskMgr := task.NewManager()
	repl.SetTaskManager(taskMgr, registry)

	ag.SetTaskBoardSnapshotter(func() string { return taskMgr.Digest(20, 1200) })

	cronSessionID := resumeID
	if cronSessionID == "__new__" {
		cronSessionID = ""
	}
	cronScheduler := agentruntime.NewSessionCronScheduler(cronSessionID, workingDir, nil)
	repl.SetCronScheduler(cronScheduler, registry)

	fileWatch := agentruntime.NewFileWatchTrigger(cfg.Watch, workingDir, cronScheduler.Emit)
	fileWatch.Start()
	defer fileWatch.Stop()

	idleMaint := agentruntime.ApplyIdleMaintenance(ag, cfg.Idle)

	agentruntime.AttachIdleConsolidation(idleMaint, autoMem, workingDir)
	defer idleMaint.Stop()
	repl.SetPlanModeTools(registry)
	repl.SetSendMessageTool(subMgr, registry)
	repl.SetTaskOutputTool(subMgr, registry)
	trace.Mark("register repl tools")

	swarmAgentFactory := func(prov provider.Provider, tools interface{}, systemPrompt string, maxTurns int) swarm.AgentRunner {
		reg, _ := tools.(*tool.Registry)
		if reg == nil {
			reg = registry
		}
		a := agent.NewAgent(prov, reg, systemPrompt, maxTurns)
		a.SetWorkingDir(ag.WorkingDir())
		return a
	}
	swarmToolBuilder := func(_ []string) interface{} {
		cloned := registry.Clone()

		for _, name := range []string{
			"ask_user", "spawn_agent", "wait_agent", "list_agents",
			"teammate_spawn", "teammate_shutdown", "team_create", "team_delete",
		} {
			cloned.Unregister(name)
		}
		return cloned
	}
	swarmMgr := swarm.NewManagerWithProviderGetter(cfg.Swarm, prov, func() provider.Provider {
		if ag != nil {
			return ag.Provider()
		}
		return prov
	}, swarmAgentFactory, swarmToolBuilder)
	swarmMgr.SetWorkingDir(ag.WorkingDir())
	swarmMgr.SetUsageHandler(repl.SessionUsageHandler())
	// #5464: teammates must be permission-gated like the primary agent
	// (ag.SetPermissionPolicy above) - thread the active policy through.
	swarmMgr.SetPermissionPolicyGetter(func() permission.PermissionPolicy {
		return policy
	})
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

	webuiSrv := webui.NewServer(cfg)
	webuiSrv.SetSessionStore(store, workingDir)
	trace.Mark("create webui")

	webuiSrv.SetMCPStatusFn(func() map[string]webui.MCPRuntimeStatus {
		return mcpSnapshotToWebUI(mcpMgr.Snapshot())
	})

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

	if imMgr != nil {
		webuiSrv.SetIMStatusFn(func() []webui.IMRuntimeStatus {
			return imSnapshotToWebUI(imMgr.Snapshot(), cfg)
		})
	}

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
			score, err := knightScoreFromParams(params)
			if err != nil {
				return err
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

	tuiBridge := webui.NewTUIChatBridge(ag, &tuiWebchatSender{repl: repl})
	webuiSrv.SetChatBridge(tuiBridge)
	repl.SetWebUIBridge(tuiBridge)
	// 会话切换执行面注入(webui 发起,实际切换由 TUI resume 全链执行)
	webuiSrv.SetTunnelStatusFn(repl.TunnelStatusSnapshot)
	webuiSrv.SetCronListFn(func() []webui.CronJobView {
		jobs := repl.CronJobsSnapshot()
		out := make([]webui.CronJobView, 0, len(jobs))
		for _, j := range jobs {
			p := j.Prompt
			if len(p) > 120 {
				p = p[:120] + "\u2026"
			}
			out = append(out, webui.CronJobView{ID: j.ID, CronExpr: j.CronExpr, Prompt: p, Recurring: j.Recurring, Paused: j.Paused, CreatedAt: j.CreatedAt.Format("2006-01-02T15:04:05Z07:00")})
		}
		return out
	})
	webuiSrv.SetSessionSwitchFn(repl.RequestSessionSwitch)
	// 聊天三能力(模型/effort/mode)注入:webui 复用 TUI slash 命令全链
	webuiSrv.SetChatCommandFn(repl.SubmitCommand)
	webuiSrv.SetChatControlsFn(repl.ChatControlsSnapshot)
	// 插入消息队列:三事件广播(append/dequeue/clear)+连接时快照
	repl.SetQueueNotifyFn(webuiSrv.EmitQueueEvent)
	webuiSrv.SetQueueSnapshotFn(repl.QueuedSubmissions)

	// 子代理实时流一等接线(§6.1):manager 回调构造 StreamEvent 广播进总线,
	// TUI/webui/桌面共用同一契约(webui WS 由 streamEventToJSON 转 subagent_delta 帧)
	subMgr.SetOnStreamText(func(agentID, text string) {
		tuiBridge.BroadcastEvent(provider.StreamEvent{
			Type:     provider.StreamEventSubagentDelta,
			Subagent: &provider.SubagentEvent{ID: agentID, Kind: "text", Text: text},
		})
	})
	subMgr.SetOnReasoning(func(agentID, text string) {
		tuiBridge.BroadcastEvent(provider.StreamEvent{
			Type:     provider.StreamEventSubagentDelta,
			Subagent: &provider.SubagentEvent{ID: agentID, Kind: "reasoning", Text: text},
		})
	})

	webuiSrv.SetRestartFn(func() {
		repl.InjectRestart()
	})

	trace.Mark("wire webui callbacks")

	actualAddr, webuiErr := webuiSrv.Start("127.0.0.1:0")
	if webuiErr != nil {

		debug.Log("root", "webui failed to start: %v", webuiErr)
	} else {
		defer webuiSrv.Close()

		repl.SetWebUIReadyAddr(actualAddr, webuiSrv.Token())
		// /webui tunnel 控制器:etunnel 隧道映射本 WebUI 到公网(../et SDK)
		tunnelCtl := &webui.TunnelCtl{}
		repl.SetWebUITunnelCtl(tunnelCtl)

		repl.SetWorkingDir(workingDir)
		webuiSrv.SetStatusFn(repl.RuntimeStatus)

		actualSessionID := resumeID
		if actualSessionID == "__new__" || actualSessionID == "" {
			actualSessionID = ""
		}

		repl.SetInitialSessionID(actualSessionID, cfg.DefaultMode)

		if actualSessionID != "" {
			if err := runfile.Write(runfile.PortFile{
				Addr:      actualAddr,
				Token:     webuiSrv.Token(),
				PID:       os.Getpid(),
				SessionID: actualSessionID,
				Workspace: workingDir,
				Mode:      cfg.DefaultMode,
			}); err != nil {
				debug.Log("root", "failed to write port file for session %s: %v", actualSessionID, err)
			}
		}

		repl.SetSessionCreatedCallback(repl.HandleSessionCreated)

		defer func() {
			runfile.Remove(repl.CurrentSessionID())
		}()

		repl.SetPreExecCleanup(func() {

			runfile.Remove(repl.CurrentSessionID())
			if lanchatHub != nil {
				lanchatHub.Close()
			}
		})
	}
	trace.Mark("start webui")

	if resumeID != "" && resumeID != "__new__" {
		repl.SetResumeID(resumeID)
	}
	if replPendingSessionLock != nil {
		repl.SetSessionLock(replPendingSessionLock)
	}

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

type tuiWebchatSender struct {
	repl *tui.REPL
}

func (s *tuiWebchatSender) SendWebchatMessage(text string, images []provider.ContentBlock) {
	if s.repl == nil {
		return
	}
	s.repl.InjectWebchatMessage(text, images)
}

func a2aAPIKey(cfg *config.Config) string {
	return cfg.A2A.EffectiveAPIKey()
}

func a2aExtensionsFromConfig(exts []config.A2AExtensionConfig) []a2a.AgentExtension {
	if len(exts) == 0 {
		return nil
	}
	out := make([]a2a.AgentExtension, 0, len(exts))
	for _, ext := range exts {
		if ext.URI == "" {
			continue
		}
		out = append(out, a2a.AgentExtension{
			URI:         ext.URI,
			Description: ext.Description,
			Required:    ext.Required,
			Params:      ext.Params,
		})
	}
	return out
}

func localA2ASkills(m *commands.Manager) []a2a.Skill {
	if m == nil {
		return nil
	}
	var out []a2a.Skill
	for _, cmd := range m.List() {
		if cmd.Source == commands.SourceBundled || cmd.Description == "" {
			continue
		}
		name := cmd.DisplayName
		if name == "" {
			name = cmd.Name
		}
		out = append(out, a2a.Skill{
			ID:          "skill:" + cmd.Name,
			Name:        name,
			Description: cmd.Description,
			Tags:        []string{"skill", string(cmd.Source)},
			Source:      string(cmd.Source),
		})
	}
	return out
}

func knightScoreFromParams(params map[string]any) (int, error) {
	score := 3
	if v, ok := params["score"]; ok {
		if f, ok := v.(float64); ok {
			score = int(math.Round(f))
		}
	}
	if score < 1 || score > 5 {
		return 0, fmt.Errorf("invalid score %d: must be within 1-5", score)
	}
	return score, nil
}

var a2aTimeoutWarnf = func(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format, args...)
}

func parseA2ATimeout(s string) time.Duration {
	if s == "" {
		return 5 * time.Minute
	}
	d, err := time.ParseDuration(s)
	if err != nil {

		a2aTimeoutWarnf("warning: invalid a2a.task_timeout %q (%v), using default %v\n", s, err, 5*time.Minute)
		return 5 * time.Minute
	}

	if d <= 0 {
		return 5 * time.Minute
	}
	return d
}

func startA2AServer(cfg *config.Config, ag *agent.Agent, reg *tool.Registry, workingDir string) (*a2a.Server, *a2a.Registry, *a2a.TaskHandler, func(), error) {
	a2aReg, err := a2a.NewRegistry()
	if err != nil {
		return nil, nil, nil, func() {}, fmt.Errorf("a2a registry: %w", err)
	}

	handler := a2a.NewTaskHandler(workingDir, ag, reg,
		a2a.WithMaxTasks(parseA2AMaxTasks(cfg.A2A.MaxTasks)),
		a2a.WithTimeout(parseA2ATimeout(cfg.A2A.TaskTimeout)),
	)

	srv := a2a.NewServer(a2a.ServerConfig{
		Host:       cfg.A2A.Host,
		Port:       cfg.A2A.Port,
		APIKey:     a2aAPIKey(cfg),
		APIKeys:    cfg.A2A.Auth.APIKeys,
		Extensions: a2aExtensionsFromConfig(cfg.A2A.Extensions),
		Delegation: a2a.DelegationPolicy{
			RequireOnBehalfOf: cfg.A2A.Delegation.RequireOnBehalfOf,
			MaxDepth:          cfg.A2A.Delegation.MaxDepth,
		},
	}, handler)

	if cfg.A2A.Auth.OAuth2 != nil {
		oc := cfg.A2A.Auth.OAuth2

		_, _, clientID, _, err := auth.ResolveA2AAuth(oc.Provider, oc.ClientID, oc.IssuerURL, oc.Scopes)
		if err != nil {
			srv.Stop()
			return nil, nil, nil, func() {}, fmt.Errorf("a2a oauth2: %w", err)
		}

		if clientID == "" {
			srv.Stop()
			return nil, nil, nil, func() {}, fmt.Errorf("a2a oauth2: no client_id resolved (set client_id, or a provider with a default)")
		}

		issuerURL := auth.ResolveA2AIssuerURL(oc.Provider, oc.IssuerURL)
		if issuerURL == "" {
			srv.Stop()
			return nil, nil, nil, func() {}, fmt.Errorf("a2a oauth2: no issuer available for provider %q (preset has no OIDC discovery URL); set issuer_url explicitly", oc.Provider)
		}

		// #2781: the same preset-placeholder fail-fast as the OAuth2 block
		// (#1503) - without it an auth0/azure preset without a filled
		// tenant starts the server clean and every JWKS fetch hits an
		// NXDOMAIN placeholder host: silent per-request 401s.

		// #1503: preset placeholders (AUTH0_TENANT/AZURE_TENANT) pass the
		// non-empty checks and the server starts fine - then every JWKS
		// fetch hits a NXDOMAIN and every request 401s silently. Fail fast
		// at startup instead, matching the #1174/#1175 spirit.

		if strings.Contains(issuerURL, "AUTH0_TENANT") || strings.Contains(issuerURL, "AZURE_TENANT") {
			srv.Stop()
			return nil, nil, nil, func() {}, fmt.Errorf("a2a oauth2: provider %q issuer is an unfilled preset placeholder (%s); set issuer_url/tenant and restart", oc.Provider, issuerURL)
		}
		if issuerURL != "" && clientID != "" {
			tv, err := auth.NewTokenValidator(clientID, issuerURL,
				auth.WithHMACSecret(cfg.A2A.Auth.HMACSecret),
				auth.WithValidIssuers(cfg.A2A.Auth.ValidIssuers),
			)
			if err != nil {
				srv.Stop()
				return nil, nil, nil, func() {}, fmt.Errorf("a2a oauth2: %w", err)
			}
			srv.SetTokenValidator(tv)
		}
	}
	if cfg.A2A.Auth.OIDC != nil {
		oc := cfg.A2A.Auth.OIDC

		_, _, clientID, _, err := auth.ResolveA2AAuth(oc.Provider, oc.ClientID, oc.IssuerURL, oc.Scopes)
		if err != nil {
			srv.Stop()
			return nil, nil, nil, func() {}, fmt.Errorf("a2a oidc: %w", err)
		}

		if clientID == "" {
			srv.Stop()
			return nil, nil, nil, func() {}, fmt.Errorf("a2a oidc: no client_id resolved (set client_id, or a provider with a default)")
		}
		issuerURL := auth.ResolveA2AIssuerURL(oc.Provider, oc.IssuerURL)
		if issuerURL == "" {
			srv.Stop()
			return nil, nil, nil, func() {}, fmt.Errorf("a2a oidc: no issuer available for provider %q; set issuer_url explicitly", oc.Provider)
		}

		if strings.Contains(issuerURL, "AUTH0_TENANT") || strings.Contains(issuerURL, "AZURE_TENANT") {
			srv.Stop()
			return nil, nil, nil, func() {}, fmt.Errorf("a2a oidc: provider %q issuer is an unfilled preset placeholder (%s); set issuer_url/tenant and restart", oc.Provider, issuerURL)
		}
		if issuerURL != "" && clientID != "" {
			tv, err := auth.NewTokenValidator(clientID, issuerURL,
				auth.WithHMACSecret(cfg.A2A.Auth.HMACSecret),
				auth.WithValidIssuers(cfg.A2A.Auth.ValidIssuers),
			)
			if err != nil {
				srv.Stop()
				return nil, nil, nil, func() {}, fmt.Errorf("a2a oidc: %w", err)
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
			return nil, nil, nil, func() {}, fmt.Errorf("a2a mtls: %w", err)
		}
		srv.SetTLSConfig(tlsCfg)
	}

	if err := srv.Start(); err != nil {
		return nil, nil, nil, func() {}, fmt.Errorf("a2a start: %w", err)
	}

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
		return nil, nil, nil, func() {}, fmt.Errorf("a2a register: %w", err)
	}

	bridgeClient := a2a.NewClient(srv.Endpoint(), a2aAPIKey(cfg),
		a2a.WithActivateExtensions(cfg.A2A.ActivateExtensions))
	for _, t := range a2a.MCPBridgeTools(bridgeClient) {
		_ = reg.Register(t)
	}

	remoteTool := a2a.NewRemoteTool(a2aReg, a2aAPIKey(cfg))
	_ = reg.Register(remoteTool)

	refreshCtx, refreshCancel := context.WithCancel(context.Background())
	safego.Go("root.a2a.refreshCache", func() {
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-refreshCtx.Done():
				return
			case <-ticker.C:
				remoteTool.RefreshCache()
			}
		}
	})

	debug.Log("root", "A2A server started at %s", srv.Endpoint())
	return srv, a2aReg, handler, refreshCancel, nil
}

func init() {
	debug.Init()

	util.SetDebugLogFn(debug.Log)
}

func detectGitStatus(workingDir string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	return vcs.Summary(ctx, workingDir)
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

func writerIsTerminal(w io.Writer) bool {
	fder, ok := w.(interface{ Fd() uintptr })
	if !ok {
		return false
	}
	return term.IsTerminal(int(fder.Fd()))
}

func parseA2AMaxTasks(n int) int {
	if n <= 0 {
		return 5
	}
	return n
}
