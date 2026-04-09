package commands

import "strings"

type Source string

const (
	SourceBundled Source = "bundled"
	SourceUser    Source = "user"
	SourceProject Source = "project"
	SourcePlugin  Source = "plugin"
	SourceMCP     Source = "mcp"
)

type LoadedFrom string

const (
	LoadedFromBundled  LoadedFrom = "bundled"
	LoadedFromSkills   LoadedFrom = "skills"
	LoadedFromCommands LoadedFrom = "commands"
	LoadedFromPlugin   LoadedFrom = "plugin"
	LoadedFromMCP      LoadedFrom = "mcp"
)

// Command represents a reusable slash command or skill loaded from markdown.
type Command struct {
	Name                   string
	Template               string
	Description            string
	Source                 Source
	LoadedFrom             LoadedFrom
	Path                   string
	DisplayName            string
	AllowedTools           []string
	ArgumentHint           string
	Arguments              []string
	WhenToUse              string
	UserInvocable          bool
	DisableModelInvocation bool
	Context                string
}

// Expand replaces template variables in the command template.
// Supported: $FILE, $DIR, $ARGS, plus any named variables supplied.
func (c *Command) Expand(vars map[string]string) string {
	result := c.Template
	for k, v := range vars {
		result = replaceVar(result, "$"+k, v)
	}
	return result
}

func (c *Command) SlashName() string {
	if c == nil || strings.TrimSpace(c.Name) == "" {
		return ""
	}
	return "/" + c.Name
}

func (c *Command) UserSlashVisible() bool {
	return c != nil && c.UserInvocable && c.LoadedFrom == LoadedFromCommands && c.SlashName() != ""
}

func (c *Command) Title() string {
	if c == nil {
		return ""
	}
	if trimmed := strings.TrimSpace(c.DisplayName); trimmed != "" {
		return trimmed
	}
	return c.Name
}

func replaceVar(s, key, value string) string {
	// Simple string replacement
	result := ""
	for i := 0; i < len(s); {
		if i+len(key) <= len(s) && s[i:i+len(key)] == key {
			result += value
			i += len(key)
		} else {
			result += string(s[i])
			i++
		}
	}
	return result
}
