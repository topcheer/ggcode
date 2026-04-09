package mcp

import "github.com/topcheer/ggcode/internal/config"

const BrowserAutomationInstallSpec = "playwright stdio npx -y @playwright/mcp"

func BrowserAutomationPreset() config.MCPServerConfig {
	return config.MCPServerConfig{
		Name:    "playwright",
		Type:    "stdio",
		Command: "npx",
		Args: []string{
			"-y",
			"@playwright/mcp",
		},
	}
}
