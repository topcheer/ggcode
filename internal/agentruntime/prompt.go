package agentruntime

import (
	"fmt"
	"github.com/topcheer/ggcode/internal/debug"
	"sort"
	"strings"

	"github.com/topcheer/ggcode/internal/commands"
	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/memory"
	"github.com/topcheer/ggcode/internal/permission"
	"github.com/topcheer/ggcode/internal/subagent"
	"github.com/topcheer/ggcode/internal/tool"
)

func BuildInteractiveSystemPrompt(
	cfg *config.Config,
	workingDir string,
	mode permission.PermissionMode,
	registry *tool.Registry,
	commandMgr *commands.Manager,
	globalAutoMem *memory.AutoMemory,
	projectAutoMem *memory.AutoMemory,
	gitStatus string,
	remoteAgentsInfo string,
	sharedWorkspaceAgents string,
) string {
	prompt, _ := BuildInteractiveSystemPromptWithPromptRefs(cfg, workingDir, mode, registry, commandMgr, globalAutoMem, projectAutoMem, gitStatus, remoteAgentsInfo, sharedWorkspaceAgents)
	return prompt
}

func BuildInteractiveSystemPromptWithPromptRefs(
	cfg *config.Config,
	workingDir string,
	mode permission.PermissionMode,
	registry *tool.Registry,
	commandMgr *commands.Manager,
	globalAutoMem *memory.AutoMemory,
	projectAutoMem *memory.AutoMemory,
	gitStatus string,
	remoteAgentsInfo string,
	sharedWorkspaceAgents string,
) (string, []string) {
	toolNames := sortedToolNames(registry)
	customCmdNames := make([]string, 0)
	if commandMgr != nil {
		userSlashCmds := commandMgr.UserSlashCommands()
		for name := range userSlashCmds {
			customCmdNames = append(customCmdNames, name)
		}
	}
	var availableModels []string
	if cfg != nil {
		if vc, ok := cfg.Vendors[cfg.Vendor]; ok {
			if ep, ok := vc.Endpoints[cfg.Endpoint]; ok {
				availableModels = ep.Models
			}
		}
	}
	prompt := config.BuildSystemPrompt(cfg.ExtraPrompt, workingDir, cfg.Language, toolNames, gitStatus, customCmdNames, availableModels)
	baseLen := len(prompt)
	snapLens := ""

	if styleGuidance := config.OutputStyleGuidance(cfg.OutputStyle); styleGuidance != "" {
		prompt += "\n\n" + styleGuidance
	}

	if snap, ok := GlobalSectionSnapshot(); ok {
		prompt += snap.Overview
		prompt += snap.Modified
		prompt += snap.RepoHead
		prompt += snap.Commands
		prompt += snap.Toolchain
		prompt += snap.Symbols
		prompt += snap.Deps
		snapLens = fmt.Sprintf("overview=%d modified=%d repoHead=%d commands=%d toolchain=%d SYMBOLS=%d deps=%d",
			len(snap.Overview), len(snap.Modified), len(snap.RepoHead), len(snap.Commands), len(snap.Toolchain), len(snap.Symbols), len(snap.Deps))
	} else {
		prompt += projectOverviewSection(workingDir)
		prompt += modifiedFilesSection(workingDir)
		prompt += repoHeadSection(workingDir)
		prompt += projectCommandsSection(workingDir)
		prompt += toolchainSection(workingDir)
		prompt += buildGoPackageSymbolsSection(workingDir)
		prompt += buildTSSymbolsSection(workingDir)
		prompt += buildPythonSymbolsSection(workingDir)
		prompt += buildPackageDepsSection(workingDir)
	}
	var promptSkillRefs []string
	skillsLen := 0
	if commandMgr != nil {
		skillsPrompt, refs := BuildSkillsSystemPromptWithPromptRefs(commandMgr.List())
		promptSkillRefs = refs
		skillsLen = len(skillsPrompt)
		if skillsPrompt != "" {
			prompt += "\n\n## Skills\n" + skillsPrompt
		}
	}
	if mode.IsCruiseMode() {
		prompt += "\n\n## Autopilot\n" +
			"You are in bypass (cruise) mode — work autonomously until the Goal is fully achieved.\n" +
			"\n" +
			"**Goal-directed execution:**\n" +
			"- At the start of each autopilot session, you will be asked to declare your goal. Work toward it fully autonomously.\n" +
			"- The Goal defines what 'done' looks like. All your work must serve this Goal.\n" +
			"- A separate strategist process monitors your progress and decides when the Goal is achieved. Keep working until then.\n" +
			"\n" +
			"**Staying on task:**\n" +
			"- Keep your work strictly within the scope of the Goal. Do not start tangential improvements, refactoring, or cleanup unless they are prerequisites.\n" +
			"- If you notice unrelated issues, note them but do not fix them.\n" +
			"- Before starting each step, verify it directly serves the Goal. If it does not, skip it.\n" +
			"\n" +
			"**Continuing autonomously:**\n" +
			"- Do not stop to ask the user for preferences or confirmation if a reasonable default exists.\n" +
			"- Choose the safest reversible assumption, state it briefly if useful, and keep going.\n" +
			"- If you only made partial progress, continue immediately — do not stop for a progress update.\n" +
			"\n" +
			"**When to escalate:**\n" +
			"- If progress is blocked on a user action, environment step, or missing external information that you cannot safely do yourself, call `ask_user` promptly.\n" +
			"- Do not report that you are blocked and waiting — either resolve it yourself or call `ask_user`."
	}
	if strings.TrimSpace(remoteAgentsInfo) != "" {
		prompt += "\n\n## Remote Agents\n" + strings.TrimSpace(remoteAgentsInfo)
	}

	// #5339 (shared-workspace agents): rendered as its own pre-formatted
	// section so the caller (lanchat formatter) owns determinism — a stable
	// byte layout keeps the KV-cache prefix valid across prompt rebuilds.
	if strings.TrimSpace(sharedWorkspaceAgents) != "" {
		prompt += "\n\n" + strings.TrimSpace(sharedWorkspaceAgents)
	}

	prompt = appendAutoMemory(prompt, globalAutoMem, projectAutoMem, "")

	namedAgentInfo := buildNamedAgentHint(workingDir)
	if namedAgentInfo != "" {
		prompt += "\n\n" + namedAgentInfo
	}

	// #5446 prompt-budget ledger: per-stage char sizes (~4 chars/token) so
	// slimming work cuts where the bytes actually are.
	debug.Log("prompt-budget", "total=%d (~%dtok) base=%d skills=%d remote=%d sharedws=%d automem=%d namedagents=%d snapshot{%s}",
		len(prompt), len(prompt)/4, baseLen, skillsLen,
		len(remoteAgentsInfo), len(sharedWorkspaceAgents), 0, len(namedAgentInfo), snapLens)
	return prompt, promptSkillRefs
}

func buildNamedAgentHint(workingDir string) string {
	store := subagent.NewTemplateStore(workingDir)
	templates, err := store.List()
	if err != nil || len(templates) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("## Named Subagents\n")
	sb.WriteString("The following named subagent templates are available in this workspace. ")
	sb.WriteString("Use `use_namedagent` to invoke any of them for specialized tasks. ")
	sb.WriteString("Use `create_namedagent` to define new ones, `delete_namedagent` to remove them, and `list_namedagent` to refresh this list.\n\n")
	for _, tmpl := range templates {
		modelInfo := ""
		if tmpl.Model != "" {
			modelInfo = fmt.Sprintf(" (model: %s)", tmpl.Model)
		}
		sb.WriteString(fmt.Sprintf("- **%s**: %s%s\n", tmpl.Name, tmpl.Description, modelInfo))
	}
	return sb.String()
}

type SubAgentPromptContext struct {
	Cfg              *config.Config
	WorkingDir       string
	Registry         *tool.Registry
	CommandMgr       *commands.Manager
	GlobalAutoMem    *memory.AutoMemory
	ProjectAutoMem   *memory.AutoMemory
	GitStatus        func() string
	RemoteAgentsInfo func() string
}

func BuildSubAgentSystemPrompt(ctx SubAgentPromptContext, task, agentType string) string {
	prompt := buildSharedAgentPrompt(ctx, task)

	roleLine := "You are running as a sub-agent."
	if agentType != "" {
		roleLine = fmt.Sprintf("You are running as a %s sub-agent.", agentType)
	}
	prompt += "\n\n## Sub-Agent Constraints\n"
	prompt += roleLine + "\n"
	prompt += "- Permission mode is bypass — no user confirmation is needed for any operation.\n"
	prompt += "- `ask_user` is not available — there is no interactive user. Make the best decision from available context.\n"
	prompt += "- `spawn_agent` is not available — do not attempt to spawn further sub-agents.\n"
	prompt += "- Provide a concise result when the task is complete.\n"
	prompt += "- Do not use emoji with Variation Selector-16 (VS16, U+FE0F) in your output.\n"

	if strings.EqualFold(agentType, "Explore") {
		prompt += "\n## Output Contract (Explore)\n"
		prompt += "End your result with a `## Regions` section listing every code region that matters for this task, one per line, in this exact format:\n"
		prompt += "path:startLine-endLine - why this region is relevant\n"
		prompt += "Use workspace-relative paths with real line ranges (from your reads). These lines are parsed so the parent agent can issue targeted reads; free text alone is not enough.\n"
	}

	prompt += "\n\n## Task\nComplete the following task independently:\n" + task

	return prompt
}

func BuildTeammateSystemPrompt(ctx SubAgentPromptContext, name, teamName string) string {

	prompt := buildSharedAgentPrompt(ctx, "")

	prompt += "\n\n## Teammate Constraints\n"
	prompt += fmt.Sprintf("You are a teammate named %q in team %q.\n", name, teamName)
	prompt += "Work like a professional collaborative team member.\n"
	prompt += "Use the shared task board as the source of truth for tracked work.\n"
	prompt += "If a task is assigned to you directly via inbox, start it immediately and do not re-claim it from the board first.\n"
	prompt += "If you choose unassigned work from the board, claim it before starting.\n"
	prompt += "Before creating a new follow-up task, check whether related work is already tracked so you do not duplicate effort.\n"
	prompt += "Share intermediate findings when they materially unblock another teammate, but avoid repetitive back-and-forth or message loops.\n"
	prompt += "If you need help or discover specialized follow-up work, send one targeted request or create one clear handoff task with enough context.\n"
	prompt += "Only claim tasks that match your role and capabilities.\n"
	prompt += "If a task does not match your role, hand it off cleanly instead of doing partial low-quality work.\n"
	prompt += "- Permission mode is bypass — no user confirmation is needed for any operation.\n"
	prompt += "- `ask_user` is not available — there is no interactive user. Make the best decision from available context.\n"
	prompt += "- `spawn_agent` and `teammate_spawn` are not available — do not attempt to create nested agents.\n"
	prompt += "- Report results concisely and mark tracked tasks complete when done.\n"
	prompt += "- Do not use emoji with Variation Selector-16 (VS16, U+FE0F) in your output.\n"

	return prompt
}

func buildSharedAgentPrompt(ctx SubAgentPromptContext, task string) string {

	toolNames := sortedToolNames(ctx.Registry)

	workingDir := ctx.WorkingDir
	gitStatus := ""
	if ctx.GitStatus != nil {
		gitStatus = ctx.GitStatus()
	}
	var extraPrompt, language string
	if ctx.Cfg != nil {
		extraPrompt = ctx.Cfg.ExtraPrompt
		language = ctx.Cfg.Language
	}
	var availableModels []string
	if ctx.Cfg != nil {
		if vc, ok := ctx.Cfg.Vendors[ctx.Cfg.Vendor]; ok {
			if ep, ok := vc.Endpoints[ctx.Cfg.Endpoint]; ok {
				availableModels = ep.Models
			}
		}
	}
	prompt := config.BuildSystemPrompt(extraPrompt, workingDir, language, toolNames, gitStatus, nil, availableModels)
	if alert := sharedWorkspaceAlertText(); alert != "" {
		prompt += "\n\n" + alert
	}
	if ctx.Cfg != nil {
		if styleGuidance := config.OutputStyleGuidance(ctx.Cfg.OutputStyle); styleGuidance != "" {
			prompt += "\n\n" + styleGuidance
		}
	}

	if snap, ok := GlobalSectionSnapshot(); ok {
		prompt += snap.Overview
		prompt += snap.Modified
		prompt += snap.RepoHead
		prompt += snap.Commands
		prompt += snap.Toolchain
		prompt += snap.Symbols
		prompt += snap.Deps
	} else {
		prompt += projectOverviewSection(workingDir)
		prompt += modifiedFilesSection(workingDir)
		prompt += repoHeadSection(workingDir)
		prompt += projectCommandsSection(workingDir)
		prompt += toolchainSection(workingDir)
		prompt += buildGoPackageSymbolsSection(workingDir)

		prompt += buildTSSymbolsSection(workingDir)
		prompt += buildPythonSymbolsSection(workingDir)
		prompt += buildPackageDepsSection(workingDir)
	}

	if ctx.CommandMgr != nil {
		skillsPrompt, _ := BuildSkillsSystemPromptWithPromptRefs(ctx.CommandMgr.List())
		if skillsPrompt != "" {
			prompt += "\n\n## Skills\n" + skillsPrompt
		}
	}

	if ctx.RemoteAgentsInfo != nil {
		if remoteInfo := strings.TrimSpace(ctx.RemoteAgentsInfo()); remoteInfo != "" {
			prompt += "\n\n## Remote Agents\n" + remoteInfo
		}
	}

	prompt = appendAutoMemory(prompt, ctx.GlobalAutoMem, ctx.ProjectAutoMem, task)

	return prompt
}

func sortedToolNames(registry *tool.Registry) []string {
	if registry == nil {
		return make([]string, 0)
	}
	tools := registry.List()
	names := make([]string, len(tools))
	for i, t := range tools {
		names[i] = t.Name()
	}
	sort.Strings(names)
	return names
}

// memoryRecallPointerSection is the fixed two-line Memory section — the
// ONLY memory surface in the system prompt: no memory bodies or title
// indexes are ever injected, only this pointer to the on-demand
// memory_recall tool. This keeps the prompt stable for KV-cache reuse
// regardless of how many memories exist. (v2 amendment: pointer-only; the
// legacy inline/index injection mode is intentionally removed.)
const memoryRecallPointerSection = "## Memory\n" +
	"Durable memories (global + project) are NOT injected here — search them on demand with the memory_recall tool (keyword, scope all|project|global). save_memory persists new patterns; delete_memory archives."

func appendAutoMemory(prompt string, globalAutoMem, projectAutoMem *memory.AutoMemory, task string) string {
	_, _, _ = globalAutoMem, projectAutoMem, task
	return prompt + "\n\n" + memoryRecallPointerSection
}
