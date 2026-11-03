package commands

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/topcheer/ggcode/internal/config"
)

type loadTarget struct {
	Dir        string
	Source     Source
	LoadedFrom LoadedFrom
}

type frontmatter struct {
	Name                   string             `yaml:"name"`
	Description            string             `yaml:"description"`
	AllowedTools           []string           `yaml:"allowed-tools,omitempty"`
	ArgumentHint           string             `yaml:"argument-hint,omitempty"`
	Arguments              []string           `yaml:"arguments,omitempty"`
	WhenToUse              string             `yaml:"when_to_use,omitempty"`
	RequiresTools          []string           `yaml:"requires-tools,omitempty"`
	Dependencies           []string           `yaml:"dependencies,omitempty"`
	Version                string             `yaml:"version,omitempty"`
	UserInvocable          *bool              `yaml:"user-invocable,omitempty"`
	DisableModelInvocation bool               `yaml:"disable-model-invocation,omitempty"`
	Context                string             `yaml:"context,omitempty"`
	Precondition           string             `yaml:"precondition,omitempty"`
	Postcondition          string             `yaml:"postcondition,omitempty"`
	StateContract          string             `yaml:"state-contract,omitempty"`
	FailureModes           []SkillFailureMode `yaml:"failure-modes,omitempty"`
}

type SkillFailureMode struct {
	Name    string `yaml:"name"`
	Detect  string `yaml:"detect"`
	Recover string `yaml:"recover"`
}

type Loader struct {
	targets []loadTarget
}

func NewLoader(projectDir string) *Loader {
	home := config.HomeDir()
	native := []loadTarget{
		{Dir: filepath.Join(home, ".agents", "skills"), Source: SourceUser, LoadedFrom: LoadedFromSkills},
		{Dir: filepath.Join(home, ".ggcode", "skills"), Source: SourceUser, LoadedFrom: LoadedFromSkills},
		{Dir: filepath.Join(home, ".ggcode", "commands"), Source: SourceUser, LoadedFrom: LoadedFromCommands},
		{Dir: filepath.Join(projectDir, ".agents", "skills"), Source: SourceProject, LoadedFrom: LoadedFromSkills},
		{Dir: filepath.Join(projectDir, ".ggcode", "skills"), Source: SourceProject, LoadedFrom: LoadedFromSkills},
		{Dir: filepath.Join(projectDir, ".ggcode", "commands"), Source: SourceProject, LoadedFrom: LoadedFromCommands},
	}
	targets := dedupeLoadTargets(append(agentPluginSkillTargets(projectDir), native...))
	return &Loader{
		targets: targets,
	}
}

func dedupeLoadTargets(targets []loadTarget) []loadTarget {
	out := make([]loadTarget, 0, len(targets))
	seen := make(map[string]struct{}, len(targets))
	for _, target := range targets {
		key := canonicalTargetDir(target.Dir)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, target)
	}
	return out
}

func canonicalTargetDir(dir string) string {
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		dir = resolved
	} else if abs, absErr := filepath.Abs(dir); absErr == nil {
		dir = abs
	}
	return filepath.Clean(dir)
}

func (l *Loader) Load() map[string]*Command {
	cmds := make(map[string]*Command)
	for _, target := range l.targets {
		loadFromDir(target, cmds)
	}
	return cmds
}

func (l *Loader) CommandDirs() []string {
	out := make([]string, 0, len(l.targets))
	for _, target := range l.targets {
		out = append(out, target.Dir)
	}
	return out
}

func (l *Loader) List() []*Command {
	cmds := l.Load()
	out := make([]*Command, 0, len(cmds))
	for _, cmd := range cmds {
		out = append(out, cmd)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Source != out[j].Source {
			return out[i].Source < out[j].Source
		}
		if out[i].LoadedFrom != out[j].LoadedFrom {
			return out[i].LoadedFrom < out[j].LoadedFrom
		}
		return out[i].Name < out[j].Name
	})
	return out
}

func loadFromDir(target loadTarget, cmds map[string]*Command) {
	entries, err := os.ReadDir(target.Dir)
	if err != nil {
		return
	}
	if target.LoadedFrom == LoadedFromSkills {
		loadSkillsFromDir(target, entries, cmds)
		return
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".md")
		if name == "" {
			continue
		}
		if cmd, ok := loadCommandFile(filepath.Join(target.Dir, e.Name()), name, target); ok {
			cmds[name] = cmd
		}
	}
}

func loadSkillsFromDir(target loadTarget, entries []os.DirEntry, cmds map[string]*Command) {
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := strings.TrimSpace(entry.Name())
		if name == "" {
			continue
		}
		path := filepath.Join(target.Dir, name, "SKILL.md")
		if cmd, ok := loadCommandFile(path, name, target); ok {
			cmds[name] = cmd
		}
	}
}

func loadCommandFile(path, name string, target loadTarget) (*Command, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	template, meta := parseCommandMarkdown(string(data))
	cmd := &Command{
		Name:                   name,
		Template:               template,
		Description:            firstNonEmptyMarkdownLine(meta.Description, template),
		Source:                 target.Source,
		LoadedFrom:             target.LoadedFrom,
		Path:                   path,
		DisplayName:            strings.TrimSpace(meta.Name),
		AllowedTools:           append([]string(nil), meta.AllowedTools...),
		ArgumentHint:           strings.TrimSpace(meta.ArgumentHint),
		Arguments:              append([]string(nil), meta.Arguments...),
		WhenToUse:              strings.TrimSpace(meta.WhenToUse),
		RequiresTools:          append([]string(nil), meta.RequiresTools...),
		Dependencies:           append([]string(nil), meta.Dependencies...),
		Version:                strings.TrimSpace(meta.Version),
		DisableModelInvocation: meta.DisableModelInvocation,
		Context:                strings.TrimSpace(meta.Context),
		Precondition:           strings.TrimSpace(meta.Precondition),
		Postcondition:          strings.TrimSpace(meta.Postcondition),
		StateContract:          strings.TrimSpace(meta.StateContract),
		FailureModes:           append([]SkillFailureMode(nil), meta.FailureModes...),
		UserInvocable:          true,
		Enabled:                true,
	}
	if meta.UserInvocable != nil {
		cmd.UserInvocable = *meta.UserInvocable
	}
	return cmd, true
}

func parseCommandMarkdown(content string) (string, frontmatter) {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	if !strings.HasPrefix(content, "---\n") {
		return content, frontmatter{}
	}
	rest := strings.TrimPrefix(content, "---\n")
	idx := strings.Index(rest, "\n---\n")
	if idx < 0 {
		return content, frontmatter{}
	}
	rawFrontmatter := rest[:idx]
	body := strings.TrimLeft(rest[idx+5:], "\n")
	var meta frontmatter
	if err := yaml.Unmarshal([]byte(rawFrontmatter), &meta); err != nil {
		return content, frontmatter{}
	}
	return body, meta
}

func firstNonEmptyMarkdownLine(values ...string) string {
	for _, value := range values {
		for _, line := range strings.Split(value, "\n") {
			line = strings.TrimSpace(line)
			if line != "" {
				return line
			}
		}
	}
	return ""
}
