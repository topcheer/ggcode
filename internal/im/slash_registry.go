package im

import (
	"fmt"
	"strings"

	"github.com/topcheer/ggcode/internal/lanchat"
	"github.com/topcheer/ggcode/internal/permission"
)

type SlashDeps interface {
	SessionCostSummary() (string, error)
	SessionUsageSummary() (string, error)
	CurrentMode() string
	SwitchMode(name string) error
	ToolList() (string, error)
	ModifiedFiles() (string, error)
	GitDiff(args []string) (string, error)
}

type SlashCommand struct {
	Name        string
	Help        string
	Category    string
	Interactive bool
	Handler     func(d SlashDeps, args []string) (string, error)
}

var imSlashRegistry = []SlashCommand{
	{
		Name:     "cost",
		Help:     "Session and cross-session cost summary",
		Category: "query",
		Handler: func(d SlashDeps, args []string) (string, error) {
			return d.SessionCostSummary()
		},
	},
	{
		Name:     "usage",
		Help:     "Token usage summary (input/output/cache)",
		Category: "query",
		Handler: func(d SlashDeps, args []string) (string, error) {
			return d.SessionUsageSummary()
		},
	},
	{
		Name:     "mode",
		Help:     "[name] Show or switch permission mode (supervised|plan|auto|bypass|autopilot)",
		Category: "query",
		Handler: func(d SlashDeps, args []string) (string, error) {
			if len(args) == 0 {
				return "Current permission mode: " + d.CurrentMode(), nil
			}

			name := strings.ToLower(strings.TrimSpace(args[0]))
			if !permission.IsValidPermissionMode(name) {
				return "", fmt.Errorf("unknown mode %q (usage: /mode supervised|plan|auto|bypass|autopilot)", args[0])
			}
			if err := d.SwitchMode(name); err != nil {
				return "", err
			}
			return "Permission mode switched to: " + name, nil
		},
	},
	{
		Name:     "tools",
		Help:     "List registered tools",
		Category: "query",
		Handler: func(d SlashDeps, args []string) (string, error) {
			return d.ToolList()
		},
	},
	{
		Name:     "files",
		Help:     "List files modified this session (checkpoint view)",
		Category: "query",
		Handler: func(d SlashDeps, args []string) (string, error) {
			return d.ModifiedFiles()
		},
	},
	{
		Name:     "diff",
		Help:     "[args] Show git diff (e.g. /diff --stat, /diff <file>)",
		Category: "query",
		Handler: func(d SlashDeps, args []string) (string, error) {
			return d.GitDiff(args)
		},
	},
	{
		Name:     "nick",
		Help:     "[name[@role][@team]] Show or set LAN chat identity",
		Category: "query",

		Handler: func(d SlashDeps, args []string) (string, error) {
			setter, ok := d.(interface {
				SetLanIdentity(nick, role, team string) (string, error)
			})
			if !ok {
				return "", fmt.Errorf("LAN chat identity is not available in this frontend (use the TUI /nick command)")
			}
			if len(args) == 0 {
				return setter.SetLanIdentity("", "", "")
			}
			nick, role, team := lanchat.ParseNickRoleTeam(strings.Join(args, " "))
			if strings.TrimSpace(nick) == "" {
				return "", fmt.Errorf("nickname cannot be empty (format: /nick name[@role][@team])")
			}
			return setter.SetLanIdentity(nick, role, team)
		},
	},

	{Name: "stats", Help: "Statistics panel (TUI-only)", Category: "tui", Interactive: true},
	{Name: "status", Help: "Status inspector panel (TUI-only)", Category: "tui", Interactive: true},
	{Name: "edit", Help: "Edit last message in editor (TUI-only)", Category: "tui", Interactive: true},
	{Name: "copy", Help: "Copy last reply (TUI-only)", Category: "tui", Interactive: true},
	{Name: "context", Help: "Context budget view (TUI-only)", Category: "tui", Interactive: true},
}

func IMSlashRegistry() []SlashCommand {
	return imSlashRegistry
}

func LookupIMSlashCommand(name string) (SlashCommand, bool) {
	name = strings.ToLower(strings.TrimSpace(name))
	for _, c := range imSlashRegistry {
		if c.Name == name {
			return c, true
		}
	}
	return SlashCommand{}, false
}

func IMSlashHelpLines() []string {
	var lines []string
	var interactive []string
	for _, c := range imSlashRegistry {
		if c.Interactive {
			interactive = append(interactive, "/"+c.Name)
			continue
		}
		lines = append(lines, "/"+c.Name+" "+c.Help)
	}
	if len(interactive) > 0 {
		lines = append(lines, "TUI-only (not available over IM): "+strings.Join(interactive, " "))
	}
	return lines
}

func ExecuteRegistrySlashCommand(d SlashDeps, text string) (string, bool) {
	parts := strings.Fields(strings.TrimSpace(text))
	if len(parts) == 0 || !strings.HasPrefix(parts[0], "/") {
		return "", false
	}
	cmd, ok := LookupIMSlashCommand(strings.TrimPrefix(parts[0], "/"))
	if !ok {
		return "", false
	}
	if cmd.Interactive {
		return fmt.Sprintf("/%s is an interactive TUI command - it needs the terminal UI and is not available over IM.", cmd.Name), true
	}
	resp, err := cmd.Handler(d, parts[1:])
	if err != nil {
		return fmt.Sprintf("%s failed: %v", cmd.Name, err), true
	}
	return resp, true
}
