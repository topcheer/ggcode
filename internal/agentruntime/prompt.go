package agentruntime

import (
	"fmt"
	"strings"

	"github.com/topcheer/ggcode/internal/commands"
	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/memory"
	"github.com/topcheer/ggcode/internal/permission"
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
) string {
	prompt, _ := BuildInteractiveSystemPromptWithPromptRefs(cfg, workingDir, mode, registry, commandMgr, globalAutoMem, projectAutoMem, gitStatus, remoteAgentsInfo)
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
) (string, []string) {
	toolNames := make([]string, 0)
	if registry != nil {
		tools := registry.List()
		toolNames = make([]string, len(tools))
		for i, t := range tools {
			toolNames[i] = t.Name()
		}
	}
	customCmdNames := make([]string, 0)
	if commandMgr != nil {
		userSlashCmds := commandMgr.UserSlashCommands()
		for name := range userSlashCmds {
			customCmdNames = append(customCmdNames, name)
		}
	}
	prompt := config.BuildSystemPrompt(cfg.ExtraPrompt, workingDir, cfg.Language, toolNames, gitStatus, customCmdNames)
	var promptSkillRefs []string
	if commandMgr != nil {
		skillsPrompt, refs := BuildSkillsSystemPromptWithPromptRefs(commandMgr.List())
		promptSkillRefs = refs
		if skillsPrompt != "" {
			prompt += "\n\n## Skills\n" + skillsPrompt
		}
	}
	if mode == permission.AutopilotMode {
		prompt += "\n\n## Autopilot\nDo not stop to ask the user for preferences or confirmation if a reasonable default exists. Choose the safest reversible assumption, explain it briefly if useful, and keep going until there is no meaningful work left. If progress is blocked on a user action, environment step, or missing external information that you cannot safely do yourself, call `ask_user` promptly instead of reporting that you are blocked and waiting. If you can perform the next step yourself with the available tools, do it instead of asking."
	}
	if strings.TrimSpace(remoteAgentsInfo) != "" {
		prompt += "\n\n## Remote Agents\n" + strings.TrimSpace(remoteAgentsInfo)
	}
	if globalAutoMem != nil {
		if globalAutoContent, _, _ := globalAutoMem.LoadAll(); globalAutoContent != "" {
			prompt += "\n\n## Auto Memory (Global)\n" + strings.TrimSpace(globalAutoContent)
		}
	}
	if projectAutoMem != nil {
		if projContent, _, _ := projectAutoMem.LoadAll(); projContent != "" {
			prompt += "\n\n## Auto Memory (Project)\n" + strings.TrimSpace(projContent)
		}
	}
	return prompt, promptSkillRefs
}

// SubAgentPromptContext holds the context needed to build a sub-agent system prompt.
// It is captured once at setup time and reused for every sub-agent spawn.
type SubAgentPromptContext struct {
	Cfg              *config.Config
	WorkingDir       string
	Registry         *tool.Registry
	CommandMgr       *commands.Manager // used for skills only; slash commands are excluded
	GlobalAutoMem    *memory.AutoMemory
	ProjectAutoMem   *memory.AutoMemory
	GitStatus        func() string // lazy, called at prompt-build time
	RemoteAgentsInfo func() string // lazy, called at prompt-build time
}

// BuildSubAgentSystemPrompt builds a system prompt for a sub-agent that mirrors
// the main agent's prompt (tools, environment, memory, LSP guidance, skills,
// remote agents, git status) but with these differences:
//   - No custom slash commands list
//   - No autopilot section (permission mode is always bypass)
//   - No ask_user tool (sub-agents cannot interact with the user)
//   - Cannot spawn further sub-agents
func BuildSubAgentSystemPrompt(ctx SubAgentPromptContext, task, agentType string) string {
	// 1. Gather tool names from the registry
	toolNames := make([]string, 0)
	if ctx.Registry != nil {
		tools := ctx.Registry.List()
		toolNames = make([]string, len(tools))
		for i, t := range tools {
			toolNames[i] = t.Name()
		}
	}

	// 2. Build base prompt (same as main agent but with NO custom slash commands)
	gitStatus := ""
	if ctx.GitStatus != nil {
		gitStatus = ctx.GitStatus()
	}
	prompt := config.BuildSystemPrompt(ctx.Cfg.ExtraPrompt, ctx.WorkingDir, ctx.Cfg.Language, toolNames, gitStatus, nil)

	// 3. Add skills (same as main agent)
	if ctx.CommandMgr != nil {
		skillsPrompt, _ := BuildSkillsSystemPromptWithPromptRefs(ctx.CommandMgr.List())
		if skillsPrompt != "" {
			prompt += "\n\n## Skills\n" + skillsPrompt
		}
	}

	// 4. Add remote agents info (same as main agent)
	if ctx.RemoteAgentsInfo != nil {
		if remoteInfo := strings.TrimSpace(ctx.RemoteAgentsInfo()); remoteInfo != "" {
			prompt += "\n\n## Remote Agents\n" + remoteInfo
		}
	}

	// 5. Add auto memory (same as main agent)
	if ctx.GlobalAutoMem != nil {
		if globalAutoContent, _, _ := ctx.GlobalAutoMem.LoadAll(); globalAutoContent != "" {
			prompt += "\n\n## Auto Memory (Global)\n" + strings.TrimSpace(globalAutoContent)
		}
	}
	if ctx.ProjectAutoMem != nil {
		if projContent, _, _ := ctx.ProjectAutoMem.LoadAll(); projContent != "" {
			prompt += "\n\n## Auto Memory (Project)\n" + strings.TrimSpace(projContent)
		}
	}

	// 6. Append sub-agent specific constraints
	roleLine := "You are running as a sub-agent."
	if agentType != "" {
		roleLine = fmt.Sprintf("You are running as a %s sub-agent.", agentType)
	}
	prompt += "\n\n## Sub-Agent Constraints\n"
	prompt += roleLine + "\n"
	prompt += "- Permission mode is bypass — no user confirmation is needed for any operation.\n"
	prompt += "- Do not use `ask_user` — there is no interactive user to answer questions. Make the best decision from available context.\n"
	prompt += "- Do not spawn further sub-agents (`spawn_agent`).\n"
	prompt += "- Provide a concise result when the task is complete.\n"
	prompt += "- Do not use emoji with Variation Selector-16 (VS16, U+FE0F) in your output.\n"

	// 7. Append the task
	prompt += "\n\n## Task\nComplete the following task independently:\n" + task

	return prompt
}
