package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/topcheer/ggcode/internal/agent"
	"github.com/topcheer/ggcode/internal/checkpoint"
	"github.com/topcheer/ggcode/internal/commands"
	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/mcp"
	"github.com/topcheer/ggcode/internal/memory"
	"github.com/topcheer/ggcode/internal/permission"
	"github.com/topcheer/ggcode/internal/plugin"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/session"
	"github.com/topcheer/ggcode/internal/subagent"
	"github.com/topcheer/ggcode/internal/tool"
	"github.com/topcheer/ggcode/internal/tui"
	"github.com/topcheer/ggcode/internal/update"
	"github.com/topcheer/ggcode/internal/version"
)

func NewRootCmd() *cobra.Command {
	var cfgFile string
	var resumeID string
	var pipePrompt string
	var allowedTools []string
	var allowedDirs []string
	var readOnlyAllowedDirs []string
	var bypassFlag bool
	var outputPath string
	var helperManifest string

	cmd := &cobra.Command{
		Use:              "ggcode",
		Short:            "AI coding assistant",
		Long:             "ggcode is a terminal-based AI coding agent powered by LLMs.",
		SilenceUsage:     true,
		TraverseChildren: true,
		RunE: func(cmd *cobra.Command, args []string) error {
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

			cfg, err := config.Load(cfgFile)
			if err != nil {
				return fmt.Errorf("loading config: %w", err)
			}
			if _, _, err := mcp.PersistUserClaudeServers(cfg); err != nil {
				return fmt.Errorf("persisting Claude MCP servers: %w", err)
			}

			// Pipe mode: non-interactive single execution
			if pipePrompt != "" {
				code := RunPipe(cfg, cfgFile, pipePrompt, allowedTools, allowedDirs, outputPath, bypassFlag, readOnlyAllowedDirs)
				if code != 0 {
					debug.Close()
					os.Exit(code)
				}
				return nil
			}

			return run(cfg, cfgFile, resumeID, bypassFlag)
		},
	}

	cmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file path")
	cmd.Flags().StringVar(&resumeID, "resume", "", "resume a previous session by ID, or open a picker with bare --resume")
	if flag := cmd.Flags().Lookup("resume"); flag != nil {
		flag.NoOptDefVal = resumePickerFlagValue
	}
	cmd.Flags().StringVarP(&pipePrompt, "prompt", "p", "", "pipe mode: non-interactive execution with a prompt")
	cmd.Flags().StringArrayVar(&allowedTools, "allowedTools", nil, "tools to allow in pipe mode (can be repeated)")
	cmd.Flags().StringArrayVar(&allowedDirs, "allowedDir", nil, "override writable sandbox directory for pipe mode (can be repeated)")
	_ = cmd.Flags().MarkHidden("allowedDir")
	cmd.Flags().StringArrayVar(&readOnlyAllowedDirs, "readOnlyAllowedDir", nil, "extra read-only sandbox directory for pipe mode (can be repeated)")
	_ = cmd.Flags().MarkHidden("readOnlyAllowedDir")
	cmd.Flags().BoolVar(&bypassFlag, "bypass", false, "start in bypass permission mode (auto-approve safe ops, warn on dangerous)")
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
	resolved, err := cfg.ResolveActiveEndpoint()
	if err != nil {
		return err
	}
	if resolved.APIKey == "" {
		return fmt.Errorf("no API key for vendor %q endpoint %q. Set the api_key in config or /provider", resolved.VendorID, resolved.EndpointID)
	}

	prov, err := provider.NewProvider(resolved)
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
	mergedMCPServers, _ := mcp.MergeStartupServers(workingDir, cfg.MCPServers)
	mcpMgr := plugin.NewMCPManager(mergedMCPServers, registry)
	_ = registry.Register(tool.ListMCPCapabilitiesTool{Runtime: mcpMgr})
	_ = registry.Register(tool.GetMCPPromptTool{Runtime: mcpMgr})
	_ = registry.Register(tool.ReadMCPResourceTool{Runtime: mcpMgr})

	// Load plugins
	pluginMgr := plugin.NewManager()
	pluginMgr.LoadAll(cfg.Plugins)
	if err := pluginMgr.RegisterTools(registry); err != nil {
		return err
	}

	autoMem := memory.NewAutoMemory()
	_ = registry.Register(tool.NewSaveMemoryTool(autoMem))

	autoContent, autoFiles, commandMgr := loadInteractiveStartupAssets(workingDir, autoMem)
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
	// Detect git status
	gitStatus := detectGitStatus(workingDir)

	// Collect tool names
	tools := registry.List()
	toolNames := make([]string, len(tools))
	for i, t := range tools {
		toolNames[i] = t.Name()
	}

	// Collect user-facing slash shortcuts separately from the full skill registry.
	userSlashCmds := commandMgr.UserSlashCommands()
	customCmdNames := make([]string, 0, len(userSlashCmds))
	for name := range userSlashCmds {
		customCmdNames = append(customCmdNames, name)
	}

	// Build enhanced system prompt with runtime context
	systemPrompt := config.BuildSystemPrompt(cfg.SystemPrompt, workingDir, toolNames, gitStatus, customCmdNames)
	if skillsPrompt := buildSkillsSystemPrompt(commandMgr.List()); skillsPrompt != "" {
		systemPrompt += "\n\n## Skills\n" + skillsPrompt
	}
	if mode == permission.AutopilotMode {
		systemPrompt += "\n\n## Autopilot\nDo not stop to ask the user for preferences or confirmation if a reasonable default exists. Choose the safest reversible assumption, explain it briefly if useful, and keep going until there is no meaningful work left. If progress is blocked on a user action, environment step, or missing external information that you cannot safely do yourself, call `ask_user` promptly instead of reporting that you are blocked and waiting. If you can perform the next step yourself with the available tools, do it instead of asking."
	}
	if autoContent != "" {
		systemPrompt += "\n\n## Auto Memory\n" + autoContent
	}

	// Setup sub-agent manager
	subMgr := subagent.NewManager(cfg.SubAgents)

	// Setup agent
	maxIter := cfg.MaxIterations
	ag := agent.NewAgent(prov, registry, systemPrompt, maxIter)
	if resolved.ContextWindow > 0 {
		ag.ContextManager().SetMaxTokens(resolved.ContextWindow)
	}
	if resolved.MaxTokens > 0 {
		ag.ContextManager().SetOutputReserve(resolved.MaxTokens)
	}
	ag.SetPermissionPolicy(policy)
	ag.SetHookConfig(cfg.Hooks)
	ag.SetWorkingDir(workingDir)
	ag.SetCheckpointManager(checkpoint.NewManager(50))

	// Setup session store
	store, err := session.NewDefaultStore()
	if err != nil {
		return fmt.Errorf("creating session store: %w", err)
	}
	if resumeID == resumePickerFlagValue {
		selectedID, err := pickResumeSession(store, session.CurrentWorkspacePath())
		if err != nil {
			return err
		}
		resumeID = selectedID
	}

	// Build MCP info for TUI
	mcpInfos := toTuiMCPInfos(mcpMgr.Snapshot())

	// Start TUI REPL
	repl := tui.NewREPL(ag, policy)
	if execPath, err := os.Executable(); err == nil {
		repl.SetUpdateService(update.NewService(version.Display(), execPath, cfgFile, workingDir))
	}
	repl.SetConfig(cfg)
	repl.SetSessionStore(store)
	repl.SetMCPServers(mcpInfos)
	repl.SetMCPManager(mcpMgr)
	repl.SetPluginManager(pluginMgr)
	repl.SetCommandsManager(commandMgr)
	repl.SetAutoMemory(autoMem)
	repl.SetAutoMemoryFiles(autoFiles)
	repl.SetProjectMemoryLoader(projectMemoryLoader)
	repl.SetSubAgentManager(subMgr, prov, registry)
	repl.SetAskUserTool(registry)
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
		commandMgr = commands.NewManager(workingDir)
	}()

	wg.Wait()

	if commandMgr == nil {
		commandMgr = commands.NewManager(workingDir)
	}

	return projectMem, projectFiles, autoContent, autoFiles, commandMgr
}

func loadInteractiveStartupAssets(
	workingDir string,
	autoMem *memory.AutoMemory,
) (string, []string, *commands.Manager) {
	var (
		autoContent string
		autoFiles   []string
		commandMgr  *commands.Manager
	)

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		autoContent, autoFiles, _ = autoMem.LoadAll()
	}()

	go func() {
		defer wg.Done()
		commandMgr = commands.NewManager(workingDir)
	}()

	wg.Wait()

	if commandMgr == nil {
		commandMgr = commands.NewManager(workingDir)
	}

	return autoContent, autoFiles, commandMgr
}

func buildSkillsSystemPrompt(skills []*commands.Command) string {
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
	return strings.Join(lines, "\n")
}

func prioritizedSkillsForPrompt(skills []*commands.Command) []*commands.Command {
	out := make([]*commands.Command, 0, len(skills))
	for _, skill := range skills {
		if skill == nil || skill.DisableModelInvocation || strings.TrimSpace(skill.Name) == "" {
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
		if skill != nil && !skill.DisableModelInvocation && strings.TrimSpace(skill.Name) != "" {
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
