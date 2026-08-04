package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/topcheer/ggcode/internal/commands"

	"gopkg.in/yaml.v3"
)

// SkillReloader reloads the command manager after a skill is created.
type SkillReloader interface {
	Reload() bool
	Get(name string) (*commands.Command, bool)
}

// CreateSkillTool lets the agent create reusable skill files that persist
// across sessions and can be invoked via the skill tool.
type CreateSkillTool struct {
	CommandMgr SkillReloader
	WorkingDir string
}

func (t CreateSkillTool) Name() string { return "create_skill" }

func (t CreateSkillTool) Description() string {
	return "Create a reusable skill (prompted workflow) that persists across sessions. " +
		"The skill becomes immediately available via the skill tool and /skills list. " +
		"Use when you've established a repeatable workflow that should be reusable."
}

func (t CreateSkillTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"name": {
				"type": "string",
				"description": "Skill name (lowercase, hyphens only, e.g. 'deploy-to-vercel'). Must be unique."
			},
			"description": {
				"type": "string",
				"description": "Short description of what the skill does."
			},
			"content": {
				"type": "string",
				"description": "Full skill body content (the prompt/instructions). This is what gets loaded when the skill is invoked."
			},
			"when_to_use": {
				"type": "string",
				"description": "When this skill should be used (shown in skill list and search)."
			},
			"allowed_tools": {
				"type": "array",
				"items": {"type": "string"},
				"description": "Tools this skill is allowed to use when executed in fork mode. If empty, all tools are available."
			},
			"scope": {
				"type": "string",
				"enum": ["project", "global"],
				"description": "Where to save: 'project' (default, in .ggcode/skills/) or 'global' (in ~/.ggcode/skills/)."
			},
			"context": {
				"type": "string",
				"enum": ["inline", "fork"],
				"description": "Execution mode: 'inline' (default, injects into current conversation) or 'fork' (runs as sub-agent)."
			},
			"description_label": {
				"type": "string",
				"description": "REQUIRED. Brief activity label shown in the UI."
			}
		},
		"required": ["name", "description", "content", "description_label"]
	}`)
}

func (t CreateSkillTool) Execute(ctx context.Context, input json.RawMessage) (Result, error) {
	var args struct {
		Name         string   `json:"name"`
		Description  string   `json:"description"`
		Content      string   `json:"content"`
		WhenToUse    string   `json:"when_to_use"`
		AllowedTools []string `json:"allowed_tools"`
		Scope        string   `json:"scope"`
		Context      string   `json:"context"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("invalid input: %v", err)}, nil
	}

	name, err := validateCreateSkillArgs(args.Name, args.Description, args.Content, args.Scope)
	if err != nil {
		return Result{IsError: true, Content: err.Error()}, nil
	}

	desc := strings.TrimSpace(args.Description)
	body := strings.TrimSpace(args.Content)
	scope := normalizeScope(args.Scope)

	if err := t.checkDuplicate(name); err != nil {
		return Result{IsError: true, Content: err.Error()}, nil
	}

	skillsDir, err := resolveSkillsDir(scope, t.WorkingDir)
	if err != nil {
		return Result{IsError: true, Content: err.Error()}, nil
	}

	skillFile := filepath.Join(skillsDir, name, "SKILL.md")
	markdown := buildSkillMarkdown(name, desc, args.WhenToUse, args.AllowedTools, args.Context, body)
	if err := os.MkdirAll(filepath.Dir(skillFile), 0o755); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("cannot create skill directory: %v", err)}, nil
	}
	if err := os.WriteFile(skillFile, []byte(markdown), 0o644); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("cannot write skill file: %v", err)}, nil
	}

	if t.CommandMgr != nil {
		t.CommandMgr.Reload()
	}

	return Result{Content: fmt.Sprintf(
		"Skill %q created successfully at %s\nIt is now available via: skill: %q\n"+
			"Or invoke with the skill tool using name: %s",
		name, scopeDirLabel(scope), name, name)}, nil
}

// validateCreateSkillArgs validates name/description/content/scope fields.
func validateCreateSkillArgs(name, description, content, scope string) (string, error) {
	name = strings.TrimSpace(name)
	if err := validateSkillName(name); err != nil {
		return "", err
	}
	if strings.TrimSpace(description) == "" {
		return "", fmt.Errorf("description is required")
	}
	if strings.TrimSpace(content) == "" {
		return "", fmt.Errorf("content is required")
	}
	s := normalizeScope(scope)
	if s != "project" && s != "global" {
		return "", fmt.Errorf("scope must be 'project' or 'global'")
	}
	return name, nil
}

// normalizeScope defaults empty scope to "project".
func normalizeScope(scope string) string {
	scope = strings.TrimSpace(scope)
	if scope == "" {
		return "project"
	}
	return scope
}

// checkDuplicate returns an error if the skill name already exists.
func (t CreateSkillTool) checkDuplicate(name string) error {
	if t.CommandMgr == nil {
		return nil
	}
	if _, exists := t.CommandMgr.Get(name); exists {
		return fmt.Errorf("skill %q already exists. Use a different name or delete the existing skill first.", name)
	}
	return nil
}

// resolveSkillsDir returns the target skills directory for the given scope.
func resolveSkillsDir(scope, workingDir string) (string, error) {
	if scope == "global" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("cannot determine home directory: %v", err)
		}
		return filepath.Join(home, ".ggcode", "skills"), nil
	}
	wd := workingDir
	if wd == "" {
		var err error
		wd, err = os.Getwd()
		if err != nil {
			return "", fmt.Errorf("cannot determine working directory: %v", err)
		}
	}
	return filepath.Join(wd, ".ggcode", "skills"), nil
}

// scopeDirLabel returns a human-readable label for the scope.
func scopeDirLabel(scope string) string {
	if scope == "global" {
		return "global (~/.ggcode/skills/)"
	}
	return "project (.ggcode/skills/)"
}

// Clone returns an independent copy for use by a different agent.
// CommandMgr and WorkingDir are agent-specific.
func (t CreateSkillTool) Clone() Tool {
	return CreateSkillTool{
		CommandMgr: t.CommandMgr,
		WorkingDir: t.WorkingDir,
	}
}

// validateSkillName ensures the name is safe for filesystem use.
func validateSkillName(name string) error {
	if name == "" {
		return fmt.Errorf("skill name is required")
	}
	if len(name) > 80 {
		return fmt.Errorf("skill name must be 80 characters or fewer")
	}
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			continue
		}
		return fmt.Errorf("skill name must be lowercase letters, digits, hyphens, or underscores (got %q)", string(r))
	}
	// Prevent path traversal
	if strings.Contains(name, "..") {
		return fmt.Errorf("skill name must not contain '..'")
	}
	return nil
}

// buildSkillMarkdown creates the SKILL.md file content with YAML frontmatter.
func buildSkillMarkdown(name, description, whenToUse string, allowedTools []string, execMode, body string) string {
	type frontmatter struct {
		Name                   string   `yaml:"name"`
		Description            string   `yaml:"description"`
		WhenToUse              string   `yaml:"when_to_use,omitempty"`
		AllowedTools           []string `yaml:"allowed-tools,omitempty"`
		Context                string   `yaml:"context,omitempty"`
		DisableModelInvocation bool     `yaml:"disable-model-invocation,omitempty"`
	}

	fm := frontmatter{
		Name:        name,
		Description: description,
	}
	if strings.TrimSpace(whenToUse) != "" {
		fm.WhenToUse = strings.TrimSpace(whenToUse)
	}
	if len(allowedTools) > 0 {
		fm.AllowedTools = allowedTools
	}
	if mode := strings.TrimSpace(execMode); mode == "fork" || mode == "inline" {
		fm.Context = mode
	}

	fmBytes, err := yaml.Marshal(fm)
	if err != nil {
		// Fallback: minimal frontmatter
		return fmt.Sprintf("---\nname: %s\ndescription: %s\n---\n\n%s", name, description, body)
	}

	return "---\n" + string(fmBytes) + "---\n\n" + body
}
