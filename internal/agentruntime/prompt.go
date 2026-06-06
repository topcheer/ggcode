package agentruntime

import (
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
) string {
	prompt, _ := BuildInteractiveSystemPromptWithPromptRefs(cfg, workingDir, mode, registry, commandMgr, globalAutoMem, projectAutoMem, gitStatus)
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
