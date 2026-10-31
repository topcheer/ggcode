package config

import "strings"

// StatusLineConfig mirrors the 2026 terminal-agent convention (Claude Code
// `statusLine`): an external command whose stdout renders a persistent,
// user-scripted status line. ggcode pipes a JSON session snapshot to the
// command's stdin and renders the first stdout line as a bar above the
// composer (see internal/tui/statusline_script.go).
type StatusLineConfig struct {
	// Command is the shell command to run. Empty disables the feature.
	Command string `yaml:"command,omitempty" json:"command,omitempty"`
	// TimeoutMS caps each invocation. Zero uses the 2s default. The last
	// good output stays on screen while a refresh runs or times out.
	TimeoutMS int `yaml:"timeout_ms,omitempty" json:"timeout_ms,omitempty"`
	// OSC7501 opts into the OSC 7501 Program Status Protocol
	// (https://mitchellh.com/writing/program-status-osc7501): ggcode emits
	// state=idle/working/blocked/done/error escape records so terminals and
	// agent-inbox tools can show whether it is busy or waiting on the user.
	// Independent of Command; only emitted when stdout is a TTY.
	OSC7501 bool `yaml:"osc_7501,omitempty" json:"osc_7501,omitempty"`
}

// Enabled reports whether a statusline command is configured.
func (c StatusLineConfig) Enabled() bool { return strings.TrimSpace(c.Command) != "" }
