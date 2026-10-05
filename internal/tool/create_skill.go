package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/topcheer/ggcode/internal/commands"
	"github.com/topcheer/ggcode/internal/util"

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
			"requires_tools": {
				"type": "array",
				"items": {"type": "string"},
				"description": "External CLI tools that must be on PATH for this skill to work (e.g. ['docker', 'kubectl']). Validated at load time."
			},
			"dependencies": {
				"type": "array",
				"items": {"type": "string"},
				"description": "Prerequisite skill names that should be loaded before this skill. Advised to the agent at load time."
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
			}
		},
		"required": ["name", "description", "content"]
	}`)
}

// #3128 V1 note: "description_label" used to be declared required in the
// Parameters schema above, but the args struct never received it
// (json.Unmarshal silently dropped it) and no consumer reads such a
// frontmatter field. Removed to make the schema truthful; description
// is the label.

func (t CreateSkillTool) Execute(ctx context.Context, input json.RawMessage) (Result, error) {
	var args struct {
		Name          string   `json:"name"`
		Description   string   `json:"description"`
		Content       string   `json:"content"`
		WhenToUse     string   `json:"when_to_use"`
		AllowedTools  []string `json:"allowed_tools"`
		RequiresTools []string `json:"requires_tools"`
		Dependencies  []string `json:"dependencies"`
		Scope         string   `json:"scope"`
		Context       string   `json:"context"`
		// NLAH-style contracts + failure taxonomy (r466).
		Precondition  string `json:"precondition"`
		Postcondition string `json:"postcondition"`
		StateContract string `json:"state_contract"`
		FailureModes  string `json:"failure_modes"` // JSON array [{"name","detect","recover"}]
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
	// #822: checkDuplicate only queries CommandMgr (nil-tolerant, blind to
	// files skipped at load e.g. malformed frontmatter). A disk check is the
	// real guard — without it a user-authored SKILL.md was silently
	// destroyed on create.
	if _, err := os.Stat(skillFile); err == nil {
		return Result{IsError: true, Content: fmt.Sprintf("skill %q already exists on disk. Use a different name or delete the existing skill first.", name)}, nil
	}
	contract := skillContract{
		Precondition:  strings.TrimSpace(args.Precondition),
		Postcondition: strings.TrimSpace(args.Postcondition),
		StateContract: strings.TrimSpace(args.StateContract),
	}
	if raw := strings.TrimSpace(args.FailureModes); raw != "" {
		var modes []commands.SkillFailureMode
		if err := json.Unmarshal([]byte(raw), &modes); err != nil {
			return Result{IsError: true, Content: fmt.Sprintf("invalid failure_modes JSON (expect [{\"name\",\"detect\",\"recover\"}]): %v", err)}, nil
		}
		contract.FailureModes = modes
	}

	markdown := buildSkillMarkdown(name, desc, args.WhenToUse, args.AllowedTools, args.RequiresTools, args.Dependencies, args.Context, contract, body)
	if err := os.MkdirAll(filepath.Dir(skillFile), 0o755); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("cannot create skill directory: %v", err)}, nil
	}
	// #3128 V2: atomic tmp+rename write (same helper as #3100) - a direct
	// os.WriteFile could leave a truncated SKILL.md on crash/full disk,
	// which #822's malformed-frontmatter skip path then silently swallows
	// (skill unusable AND blocked from re-creation by the disk check above).
	//
	// #3163: the Stat check above and this write are a classic check-then-act
	// TOCTOU - two agents creating the same-named skill concurrently both
	// pass Stat and the second AtomicWriteFile rename silently clobbers the
	// first, defeating #822's guard (instances are registered per-process in
	// cmd/ggcode and desktop, so an in-process mutex is not enough). An
	// O_CREATE|O_EXCL placeholder is the atomic cross-process gate: the
	// loser gets EEXIST (placeholder visible even at 0 bytes, mid-race) and
	// is rejected; the winner's rename then atomically replaces its own
	// placeholder. Go maps O_EXCL to CREATE_NEW on Windows, so this needs
	// no platform split.
	ph, err := os.OpenFile(skillFile, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if os.IsExist(err) {
		return Result{IsError: true, Content: fmt.Sprintf("skill %q already exists on disk (or is concurrently being created). Use a different name or delete the existing skill first.", name)}, nil
	}
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("cannot create skill file: %v", err)}, nil
	}
	ph.Close()
	if err := util.AtomicWriteFile(skillFile, []byte(markdown), 0o644); err != nil {
		// Best-effort cleanup: a leftover 0-byte placeholder would block
		// re-creation until manually removed. Concurrent creators see the
		// placeholder and are rejected (correct); we only remove our own.
		os.Remove(skillFile)
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
// skillContract carries a skill's NLAH-style declarations through
// buildSkillMarkdown (r466).
type skillContract struct {
	Precondition  string
	Postcondition string
	StateContract string
	FailureModes  []commands.SkillFailureMode
}

func buildSkillMarkdown(name, description, whenToUse string, allowedTools, requiresTools, dependencies []string, execMode string, contract skillContract, body string) string {
	type frontmatter struct {
		Name                   string                      `yaml:"name"`
		Description            string                      `yaml:"description"`
		WhenToUse              string                      `yaml:"when_to_use,omitempty"`
		AllowedTools           []string                    `yaml:"allowed-tools,omitempty"`
		RequiresTools          []string                    `yaml:"requires-tools,omitempty"`
		Dependencies           []string                    `yaml:"dependencies,omitempty"`
		Context                string                      `yaml:"context,omitempty"`
		DisableModelInvocation bool                        `yaml:"disable-model-invocation,omitempty"`
		Precondition           string                      `yaml:"precondition,omitempty"`
		Postcondition          string                      `yaml:"postcondition,omitempty"`
		StateContract          string                      `yaml:"state-contract,omitempty"`
		FailureModes           []commands.SkillFailureMode `yaml:"failure-modes,omitempty"`
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
	if len(requiresTools) > 0 {
		fm.RequiresTools = requiresTools
	}
	if len(dependencies) > 0 {
		fm.Dependencies = dependencies
	}
	if mode := strings.TrimSpace(execMode); mode == "fork" || mode == "inline" {
		fm.Context = mode
	}
	if contract.Precondition != "" {
		fm.Precondition = contract.Precondition
	}
	if contract.Postcondition != "" {
		fm.Postcondition = contract.Postcondition
	}
	if contract.StateContract != "" {
		fm.StateContract = contract.StateContract
	}
	if len(contract.FailureModes) > 0 {
		fm.FailureModes = contract.FailureModes
	}

	fmBytes, err := yaml.Marshal(fm)
	if err != nil {
		// Fallback: minimal frontmatter
		return fmt.Sprintf("---\nname: %s\ndescription: %s\n---\n\n%s", name, description, body)
	}

	return "---\n" + string(fmBytes) + "---\n\n" + body
}
