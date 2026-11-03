package config

import "strings"

type StatusLineConfig struct {
	Command   string `yaml:"command,omitempty" json:"command,omitempty"`
	TimeoutMS int    `yaml:"timeout_ms,omitempty" json:"timeout_ms,omitempty"`
	OSC7501   bool   `yaml:"osc_7501,omitempty" json:"osc_7501,omitempty"`
}

func (c StatusLineConfig) Enabled() bool { return strings.TrimSpace(c.Command) != "" }
