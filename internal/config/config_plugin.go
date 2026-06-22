package config

import (
	"fmt"
	"strings"
)

// AddGRPCPlugin adds or replaces a gRPC plugin entry in the config.
// If a plugin with the same name already exists, it is replaced.
func (c *Config) AddGRPCPlugin(name string, command []string, env map[string]string) {
	if c == nil {
		return
	}
	// Remove existing entry with the same name
	c.RemovePlugin(name)

	entry := PluginConfigEntry{
		Name:    name,
		Type:    "grpc",
		Command: command,
		Env:     env,
	}
	c.Plugins = append(c.Plugins, entry)
}

// AddCommandPlugin adds or replaces a command plugin entry in the config.
func (c *Config) AddCommandPlugin(name string, commands []PluginCommandConfig) {
	if c == nil {
		return
	}
	c.RemovePlugin(name)

	entry := PluginConfigEntry{
		Name:     name,
		Type:     "command",
		Commands: commands,
	}
	c.Plugins = append(c.Plugins, entry)
}

// RemovePlugin removes a plugin by name. Returns true if found.
func (c *Config) RemovePlugin(name string) bool {
	if c == nil {
		return false
	}
	for i, p := range c.Plugins {
		if p.Name == name {
			c.Plugins = append(c.Plugins[:i], c.Plugins[i+1:]...)
			return true
		}
	}
	return false
}

// FindPlugin returns a plugin by name, or nil if not found.
func (c *Config) FindPlugin(name string) *PluginConfigEntry {
	if c == nil {
		return nil
	}
	for i := range c.Plugins {
		if c.Plugins[i].Name == name {
			return &c.Plugins[i]
		}
	}
	return nil
}

// ListPlugins returns all configured plugins.
func (c *Config) ListPlugins() []PluginConfigEntry {
	if c == nil {
		return nil
	}
	return c.Plugins
}

// ValidateGRPCPlugin checks that a gRPC plugin config is valid.
func (e *PluginConfigEntry) ValidateGRPCPlugin() error {
	if e.Name == "" {
		return fmt.Errorf("plugin name is required")
	}
	if strings.ToLower(e.Type) != "grpc" {
		return fmt.Errorf("plugin %q is not type grpc", e.Name)
	}
	if len(e.Command) == 0 {
		return fmt.Errorf("plugin %q has no command", e.Name)
	}
	return nil
}
