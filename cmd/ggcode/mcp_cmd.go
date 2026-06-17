package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/mcp"
)

func newMCPCmd(cfgFile *string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "Manage MCP server configuration",
	}

	installCmd := &cobra.Command{
		Use:                "install [name] [<stdio|http|ws>] [-t <stdio|http|ws>] [--env KEY=VALUE ...] [--header KEY:VALUE ...] [-- <command...|url>]",
		Short:              "Install an MCP server into ggcode config",
		Long:               "Install an MCP server into ggcode config.\n\nExamples:\n  ggcode mcp install stdio npx -y 12306-mcp stdio\n  ggcode mcp install 12306-mcp stdio npx -y 12306-mcp stdio\n  ggcode mcp install stdio uvx wikipedia-mcp-server@latest\n  ggcode mcp install web-reader http https://mcp.example.com/api\n  ggcode mcp install z-ai --env ZAI_AI_API_KEY=xxxx -- npx -y @z_ai/mcp-server\n  ggcode mcp install web-reader -t http https://mcp.example.com/api --header \"Authorization: Bearer xxx\"",
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			args = sanitizeMCPInstallArgs(args)
			if len(args) == 1 && (args[0] == "-h" || args[0] == "--help") {
				_ = cmd.Help()
				return nil
			}
			// No args → interactive wizard
			if len(args) == 0 {
				wizardArgs, err := mcpInstallWizard(cmd.OutOrStdout())
				if err != nil {
					return err
				}
				args = wizardArgs
			}
			if len(args) < 2 {
				return fmt.Errorf("usage: %s", cmd.UseLine())
			}
			path := *cfgFile
			if path == "" {
				path = config.ConfigPath()
			}
			cfg, err := loadMCPConfig(path, true)
			if err != nil {
				return err
			}
			server, err := mcp.ParseInstallArgs(args)
			if err != nil {
				return err
			}
			replaced := cfg.UpsertMCPServer(server)
			if err := cfg.Save(); err != nil {
				return fmt.Errorf("saving config: %w", err)
			}
			action := "Installed"
			if replaced {
				action = "Updated"
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s MCP server %s in %s\n", action, server.Name, cfg.FilePath)
			return nil
		},
	}

	uninstallCmd := &cobra.Command{
		Use:   "uninstall <name>",
		Short: "Remove an MCP server from ggcode config",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := *cfgFile
			if path == "" {
				path = config.ConfigPath()
			}
			cfg, err := loadMCPConfig(path, true)
			if err != nil {
				return err
			}
			name := args[0]
			if !cfg.RemoveMCPServer(name) {
				return fmt.Errorf("mcp server %s not found", name)
			}
			if err := cfg.Save(); err != nil {
				return fmt.Errorf("saving config: %w", err)
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Uninstalled MCP server %s from %s\n", name, cfg.FilePath)
			return nil
		},
	}

	listCmd := &cobra.Command{
		Use:   "list",
		Short: "List configured MCP servers",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			path := *cfgFile
			if path == "" {
				path = config.ConfigPath()
			}
			cfg, err := loadMCPConfig(path, false)
			if err != nil {
				return err
			}
			if len(cfg.MCPServers) == 0 {
				_, _ = fmt.Fprint(cmd.OutOrStdout(), "No MCP servers configured in "+cfg.FilePath+"\r\n")
				return nil
			}
			var out strings.Builder
			for i, server := range cfg.MCPServers {
				if i > 0 {
					out.WriteString("\r\n")
				}
				out.WriteString(server.Name)
				out.WriteString(" [")
				out.WriteString(firstNonEmptyTransport(server.Type))
				out.WriteString("]\r\n")
				out.WriteString("  target: ")
				out.WriteString(formatMCPServerTarget(server))
				out.WriteString("\r\n")
			}
			_, _ = fmt.Fprint(cmd.OutOrStdout(), out.String())
			return nil
		},
	}

	cmd.AddCommand(installCmd)
	cmd.AddCommand(listCmd)
	cmd.AddCommand(uninstallCmd)
	configureHelpRendering(cmd)
	configureHelpRendering(installCmd)
	configureHelpRendering(listCmd)
	configureHelpRendering(uninstallCmd)
	return cmd
}

func firstNonEmptyTransport(transport string) string {
	if strings.TrimSpace(transport) == "" {
		return "stdio"
	}
	return strings.TrimSpace(transport)
}

func formatMCPServerTarget(server config.MCPServerConfig) string {
	switch firstNonEmptyTransport(server.Type) {
	case "http", "ws":
		return strings.TrimSpace(server.URL)
	default:
		parts := append([]string{strings.TrimSpace(server.Command)}, server.Args...)
		return strings.TrimSpace(strings.Join(parts, " "))
	}
}

func loadMCPConfig(path string, persistClaude bool) (*config.Config, error) {
	cfg, err := config.Load(path)
	if err != nil {
		return nil, fmt.Errorf("loading config: %w", err)
	}
	changed := pruneMalformedMCPServers(cfg)
	if changed {
		if err := cfg.Save(); err != nil {
			return nil, fmt.Errorf("saving repaired config: %w", err)
		}
	}
	if persistClaude {
		if _, _, err := mcp.PersistUserClaudeServers(cfg); err != nil {
			return nil, fmt.Errorf("persisting Claude MCP servers: %w", err)
		}
	}
	return cfg, nil
}

func pruneMalformedMCPServers(cfg *config.Config) bool {
	if cfg == nil || len(cfg.MCPServers) == 0 {
		return false
	}
	filtered := cfg.MCPServers[:0]
	changed := false
	for _, server := range cfg.MCPServers {
		if isMalformedMCPServer(server) {
			changed = true
			continue
		}
		filtered = append(filtered, server)
	}
	if changed {
		cfg.MCPServers = filtered
	}
	return changed
}

func isMalformedMCPServer(server config.MCPServerConfig) bool {
	transport := firstNonEmptyTransport(server.Type)
	if transport != "stdio" {
		return false
	}
	command := strings.TrimSpace(server.Command)
	return strings.HasPrefix(command, "-")
}

func sanitizeMCPInstallArgs(args []string) []string {
	if len(args) == 0 {
		return args
	}
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := strings.TrimSpace(args[i])
		if arg == "--config" {
			if i+1 < len(args) {
				i++
			}
			continue
		}
		if strings.HasPrefix(arg, "--config=") {
			continue
		}
		out = append(out, args[i])
	}
	return out
}

// mcpInstallWizard provides an interactive prompt to configure an MCP server.
// Returns the args in the same format as the CLI: [name, transport, command.../url].
func mcpInstallWizard(out io.Writer) ([]string, error) {
	reader := bufio.NewReader(os.Stdin)

	prompt := func(label, def string) string {
		suffix := ""
		if def != "" {
			suffix = fmt.Sprintf(" [%s]", def)
		}
		fmt.Fprintf(out, "%s%s: ", label, suffix)
		line, _ := reader.ReadString('\n')
		line = strings.TrimSpace(line)
		if line == "" {
			return def
		}
		return line
	}

	fmt.Fprint(out, "\n=== MCP Server Setup Wizard ===\n\n")

	// Step 1: Name
	name := prompt("Server name (e.g. github, web-reader)", "")
	if name == "" {
		return nil, fmt.Errorf("server name is required")
	}

	// Step 2: Transport type
	fmt.Fprintln(out, "\nTransport types:")
	fmt.Fprintln(out, "  1) stdio  — local command (default)")
	fmt.Fprintln(out, "  2) http   — remote HTTP endpoint")
	fmt.Fprintln(out, "  3) ws     — WebSocket endpoint")
	transportChoice := prompt("Choose transport [1]", "1")
	transport := "stdio"
	switch transportChoice {
	case "2", "http":
		transport = "http"
	case "3", "ws":
		transport = "ws"
	}

	// Step 3: Target (command or URL)
	var target string
	if transport == "stdio" {
		target = prompt("Command (e.g. npx -y @modelcontextprotocol/server-github)", "")
	} else {
		target = prompt("URL (e.g. https://mcp.example.com/api)", "")
	}
	if target == "" {
		return nil, fmt.Errorf("command or URL is required")
	}

	// Step 4: Environment variables (stdio only)
	var envPairs []string
	if transport == "stdio" {
		fmt.Fprint(out, "\nEnvironment variables (KEY=VALUE). Press Enter on empty line to skip.\n")
		envIdx := 0
		for {
			envIdx++
			env := prompt(fmt.Sprintf("Env var #%d", envIdx), "")
			if env == "" {
				break
			}
			envPairs = append(envPairs, "--env", env)
		}
	}

	// Step 5: Headers (http/ws only)
	var headerPairs []string
	if transport != "stdio" {
		fmt.Fprint(out, "\nHeaders (KEY:VALUE). Press Enter on empty line to skip.\n")
		headerIdx := 0
		for {
			headerIdx++
			header := prompt(fmt.Sprintf("Header #%d", headerIdx), "")
			if header == "" {
				break
			}
			headerPairs = append(headerPairs, "--header", header)
		}
	}

	// Assemble args — ParseInstallArgs expects:
	//   [name] -t <transport> [--env KEY=VAL ...] [--header KEY:VAL ...] -- <cmd.../url>
	args := []string{name}
	args = append(args, "-t", transport)
	args = append(args, envPairs...)
	args = append(args, headerPairs...)
	args = append(args, "--")
	// For stdio, split the command into parts; for http/ws, it's a URL
	if transport == "stdio" {
		args = append(args, splitCommand(target)...)
	} else {
		args = append(args, target)
	}

	fmt.Fprintf(out, "\nReview:\n  name: %s\n  transport: %s\n  target: %s\n", name, transport, target)
	confirm := prompt("\nConfirm install? [Y/n]", "Y")
	if confirm == "n" || confirm == "N" {
		return nil, fmt.Errorf("installation cancelled")
	}

	return args, nil
}

// splitCommand splits a command string into arguments, respecting double quotes.
// e.g. `npx -y "my server" --port 3000` → ["npx", "-y", "my server", "--port", "3000"]
func splitCommand(s string) []string {
	// Use shell-style word splitting with quote support.
	// This is intentionally simple — not a full shell parser.
	var parts []string
	var current strings.Builder
	inQuote := false
	flush := func() {
		if current.Len() > 0 {
			parts = append(parts, current.String())
			current.Reset()
		}
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '"':
			inQuote = !inQuote
		case c == ' ' || c == '\t':
			if inQuote {
				current.WriteByte(c)
			} else {
				flush()
			}
		default:
			current.WriteByte(c)
		}
	}
	flush()
	return parts
}
