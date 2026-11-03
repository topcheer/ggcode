package config

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

func (c *Config) AddGRPCPlugin(name string, command []string, env map[string]string) {
	if c == nil {
		return
	}

	c.RemovePlugin(name)

	entry := PluginConfigEntry{
		Name:    name,
		Type:    "grpc",
		Command: command,
		Env:     env,
	}
	c.Plugins = append(c.Plugins, entry)
}

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

func (c *Config) SavePlugins() error {
	if c == nil {
		return fmt.Errorf("config is nil")
	}
	return c.patchConfigFile(func(raw map[string]interface{}) {
		if len(c.Plugins) == 0 {
			delete(raw, "plugins")
			return
		}
		data, _ := yaml.Marshal(c.Plugins)
		var list []interface{}
		yaml.Unmarshal(data, &list)
		raw["plugins"] = list
	})
}

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
