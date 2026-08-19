package im

import (
	"fmt"
	"strings"

	"github.com/topcheer/ggcode/internal/permission"
)

// Shared slash-command registry for IM inbound.
//
// Both IM inbound paths dispatch from this single table:
//   - Path A: the daemon bridge (IM bound to a daemon session)
//   - Path B: the TUI remote handler (IM bound to an attached TUI session)
//
// Before this registry existed the two paths hand-enumerated commands in
// separate switches with separately maintained help texts, which is how
// commands drifted (429075b0 shipped a classifier+handler but no dispatch;
// /cost /mode landed on path A only). The registry is the structural fix:
// a command lands here once and both paths serve it, the help text is
// generated from the same entries, and the parity test fails if a one-shot
// command is not routable on either path.
//
// Scope rule: only one-shot, text-output commands get handlers. Commands
// that need interactive TUI surfaces (panels, editors, pickers) are
// registered with Interactive=true so IM answers with a clear "TUI-only"
// hint instead of "Unknown command".

// SlashDeps supplies path-specific state to registry handlers. Each inbound
// path implements it from its own live state: the daemon bridge reads the
// agent and disk stores; the TUI adapter reads the live model/session.
type SlashDeps interface {
	// SessionCostSummary renders /cost (live session view when available,
	// cross-session disk summary otherwise).
	SessionCostSummary() (string, error)
	// SessionUsageSummary renders /usage (token counts view).
	SessionUsageSummary() (string, error)
	// CurrentMode reports the current permission mode name.
	CurrentMode() string
	// SwitchMode applies a mode switch (name pre-validated by the handler).
	SwitchMode(name string) error
	// ToolList renders the registered-tool listing.
	ToolList() (string, error)
	// ModifiedFiles renders the checkpoint modified-file listing.
	ModifiedFiles() (string, error)
	// GitDiff renders `git diff <args>` output.
	GitDiff(args []string) (string, error)
}

// SlashCommand is one registry entry.
type SlashCommand struct {
	Name     string // canonical name without the leading slash
	Help     string // one-line help text (generated into /help)
	Category string // "query" (one-shot) or "tui" (interactive, TUI-only)
	// Interactive marks commands that need a TUI surface. IM replies with a
	// TUI-only hint instead of executing.
	Interactive bool
	// Handler executes the command. Nil for Interactive entries.
	Handler func(d SlashDeps, args []string) (string, error)
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
			// #743: reject invalid names instead of silently falling back to
			// the ParsePermissionMode default while reporting success.
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
	// Interactive TUI commands: surfaced so IM users get a precise hint
	// instead of "Unknown command".
	{Name: "stats", Help: "Statistics panel (TUI-only)", Category: "tui", Interactive: true},
	{Name: "status", Help: "Status inspector panel (TUI-only)", Category: "tui", Interactive: true},
	{Name: "edit", Help: "Edit last message in editor (TUI-only)", Category: "tui", Interactive: true},
	{Name: "copy", Help: "Copy last reply (TUI-only)", Category: "tui", Interactive: true},
	{Name: "context", Help: "Context budget view (TUI-only)", Category: "tui", Interactive: true},
}

// IMSlashRegistry returns the shared registry (for parity tests and help
// generation). Callers must not mutate the returned slice.
func IMSlashRegistry() []SlashCommand {
	return imSlashRegistry
}

// LookupIMSlashCommand finds a registry entry by bare name (without slash).
func LookupIMSlashCommand(name string) (SlashCommand, bool) {
	name = strings.ToLower(strings.TrimSpace(name))
	for _, c := range imSlashRegistry {
		if c.Name == name {
			return c, true
		}
	}
	return SlashCommand{}, false
}

// IMSlashHelpLines generates the /help lines for the registry commands.
// Interactive entries collapse into one summary line so the help stays
// readable on messaging platforms.
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

// ExecuteRegistrySlashCommand dispatches a slash command through the shared
// registry. Returns (response, handled); commands absent from the registry
// return handled=false so the caller's other handlers or unknown-command
// path take over.
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
