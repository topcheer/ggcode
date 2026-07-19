package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/topcheer/ggcode/internal/config"
)

func newPluginCmd(cfgFile *string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "plugin",
		Short: "Manage gRPC and command plugins",
	}

	cmd.AddCommand(newPluginInstallCmd(cfgFile))
	cmd.AddCommand(newPluginUninstallCmd(cfgFile))
	cmd.AddCommand(newPluginListCmd(cfgFile))
	cmd.AddCommand(newPluginTestCmd(cfgFile))

	return cmd
}

func newPluginInstallCmd(cfgFile *string) *cobra.Command {
	var envVars []string
	var pluginType string

	cmd := &cobra.Command{
		Use:   "install <name> <command...> [-- env KEY=VALUE ...]",
		Short: "Install a plugin into ggcode config",
		Long: `Install a plugin into ggcode config.

The plugin type is auto-detected:
  - If the first argument is an executable path, it's treated as a gRPC plugin
  - Use --type to force a specific type

For gRPC plugins, the command is the executable and its arguments:
  ggcode plugin install jira-tools ./bin/jira-plugin
  ggcode plugin install jira-tools python -m my_jira_plugin
  ggcode plugin install jira-tools node ./jira-plugin.js --env JIRA_TOKEN=xxx

For command plugins:
  ggcode plugin install my-tools --type command -- ./deploy.sh

Examples:
  # Install a Go-compiled gRPC plugin
  ggcode plugin install my-plugin ./bin/my-plugin

  # Install a Python gRPC plugin
  ggcode plugin install jira-tools python -m ggcode_plugin_jira

  # Install with environment variables
  ggcode plugin install api-tools ./bin/api-tool --env API_KEY=secret --env DEBUG=true

  # Install a Node.js gRPC plugin
  ggcode plugin install slack-tools node ./slack-plugin.js --env SLACK_TOKEN=xoxb-...`,
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 && (args[0] == "-h" || args[0] == "--help") {
				_ = cmd.Help()
				return nil
			}

			// Parse flags manually (since DisableFlagParsing is true)
			positionals, envVars, pluginType, err := parsePluginInstallArgs(args)
			if err != nil {
				return err
			}
			if len(positionals) < 2 {
				return fmt.Errorf("usage: ggcode plugin install <name> <command...>")
			}

			name := positionals[0]
			command := positionals[1:]

			// Validate command exists for gRPC plugins
			if pluginType == "grpc" {
				if _, err := exec.LookPath(command[0]); err != nil {
					if _, statErr := os.Stat(command[0]); statErr != nil {
						return fmt.Errorf("command not found: %s", command[0])
					}
				}
			}

			// Build env map
			envMap := make(map[string]string)
			for _, e := range envVars {
				parts := strings.SplitN(e, "=", 2)
				if len(parts) != 2 {
					return fmt.Errorf("invalid env var (expected KEY=VALUE): %s", e)
				}
				envMap[parts[0]] = parts[1]
			}

			path := *cfgFile
			if path == "" {
				path = config.ConfigPath()
			}
			cfg, err := config.Load(path)
			if err != nil {
				return fmt.Errorf("loading config: %w", err)
			}

			// Check if replacing
			existing := cfg.FindPlugin(name)
			action := "Installed"
			if existing != nil {
				action = "Updated"
			}

			if pluginType == "command" {
				cfg.AddCommandPlugin(name, nil) // command plugins need sub-commands defined in yaml
			} else {
				cfg.AddGRPCPlugin(name, command, envMap)
			}

			if err := cfg.Save(); err != nil {
				return fmt.Errorf("saving config: %w", err)
			}

			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s plugin %q in %s\n", action, name, cfg.FilePath)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "  type:    %s\n", pluginType)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "  command: %s\n", strings.Join(command, " "))
			if len(envMap) > 0 {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "  env:     %d variables\n", len(envMap))
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "\nRestart ggcode for the plugin to take effect.\n")
			return nil
		},
	}

	cmd.Flags().StringSliceVarP(&envVars, "env", "e", nil, "Environment variables (KEY=VALUE)")
	cmd.Flags().StringVar(&pluginType, "type", "grpc", "Plugin type: grpc or command")

	return cmd
}

func newPluginUninstallCmd(cfgFile *string) *cobra.Command {
	return &cobra.Command{
		Use:   "uninstall <name>",
		Short: "Remove a plugin from ggcode config",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			path := *cfgFile
			if path == "" {
				path = config.ConfigPath()
			}
			cfg, err := config.Load(path)
			if err != nil {
				return fmt.Errorf("loading config: %w", err)
			}
			if !cfg.RemovePlugin(name) {
				return fmt.Errorf("plugin %q not found in config", name)
			}
			if err := cfg.SavePlugins(); err != nil {
				return fmt.Errorf("saving config: %w", err)
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Uninstalled plugin %q from %s\n", name, cfg.FilePath)
			return nil
		},
	}
}

func newPluginListCmd(cfgFile *string) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List configured plugins",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			path := *cfgFile
			if path == "" {
				path = config.ConfigPath()
			}
			cfg, err := config.Load(path)
			if err != nil {
				return err
			}
			if len(cfg.Plugins) == 0 {
				_, _ = fmt.Fprint(cmd.OutOrStdout(), "No plugins configured in "+cfg.FilePath+"\n")
				return nil
			}
			var out strings.Builder
			for i, p := range cfg.Plugins {
				if i > 0 {
					out.WriteString("\n")
				}
				pType := p.Type
				if pType == "" {
					pType = "command"
				}
				out.WriteString(fmt.Sprintf("%s [%s]", p.Name, pType))
				if len(p.Command) > 0 {
					out.WriteString("  cmd: " + strings.Join(p.Command, " "))
				}
				if len(p.Commands) > 0 {
					names := make([]string, len(p.Commands))
					for j, c := range p.Commands {
						names[j] = c.Name
					}
					out.WriteString("  tools: " + strings.Join(names, ", "))
				}
				if len(p.Env) > 0 {
					out.WriteString(fmt.Sprintf("  env: %d vars", len(p.Env)))
				}
			}
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), out.String())
			return nil
		},
	}
}

func newPluginTestCmd(cfgFile *string) *cobra.Command {
	return &cobra.Command{
		Use:   "test <name>",
		Short: "Test a gRPC plugin by connecting and listing its tools",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			path := *cfgFile
			if path == "" {
				path = config.ConfigPath()
			}
			cfg, err := config.Load(path)
			if err != nil {
				return err
			}
			p := cfg.FindPlugin(name)
			if p == nil {
				return fmt.Errorf("plugin %q not found in config", name)
			}
			if err := p.ValidateGRPCPlugin(); err != nil {
				return err
			}

			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Connecting to plugin %q...\n", name)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "  command: %s\n", strings.Join(p.Command, " "))

			// Check command exists
			binary := p.Command[0]
			if _, err := exec.LookPath(binary); err != nil {
				if _, statErr := os.Stat(binary); statErr != nil {
					return fmt.Errorf("plugin binary not found: %s", binary)
				}
			}

			// Start the plugin process with a pipe on stdout to read the handshake line.
			// The plugin blocks forever (it's a server), so we read the first line then kill it.
			ctx, cancel := context.WithTimeout(cmd.Context(), 10*time.Second)
			defer cancel()

			testCmd := exec.CommandContext(ctx, p.Command[0], p.Command[1:]...)
			testCmd.Env = append(os.Environ(), "GGCODE_PLUGIN=ggcode-grpc-plugin-v1")
			for k, v := range p.Env {
				testCmd.Env = append(testCmd.Env, k+"="+v)
			}

			stdout, err := testCmd.StdoutPipe()
			if err != nil {
				return fmt.Errorf("creating stdout pipe: %w", err)
			}
			testCmd.Stderr = os.Stderr

			if err := testCmd.Start(); err != nil {
				return fmt.Errorf("starting plugin: %w", err)
			}

			// Read the first line from stdout — this is the go-plugin handshake line.
			reader := bufio.NewReader(stdout)
			handshakeLine, err := reader.ReadString('\n')
			if err != nil {
				_ = testCmd.Process.Kill()
				return fmt.Errorf("reading handshake from plugin stdout: %w", err)
			}
			handshakeLine = strings.TrimSpace(handshakeLine)

			// Kill the plugin process — we only needed the handshake
			_ = testCmd.Process.Kill()
			_, _ = testCmd.Process.Wait()

			// Validate handshake line format: core_version|app_version|network|address|protocol|cert
			parts := strings.Split(handshakeLine, "|")
			if len(parts) < 5 {
				return fmt.Errorf("invalid handshake line (expected 5+ fields): %s", handshakeLine)
			}
			network := parts[2]
			address := parts[3]
			protocol := parts[4]

			if network != "unix" {
				return fmt.Errorf("expected unix socket, got network %q", network)
			}
			if protocol != "grpc" {
				return fmt.Errorf("expected grpc protocol, got %q", protocol)
			}

			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "\nHandshake OK!\n")
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "  network:  %s\n", network)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "  address:  %s\n", address)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "  protocol: %s\n", protocol)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "\nPlugin %q is a valid gRPC plugin. Tools are discovered at ggcode startup.\n", name)
			return nil
		},
	}
}

// parsePluginInstallArgs separates positional args from flags.
// Returns: positionals, envVars, pluginType, error
func parsePluginInstallArgs(args []string) ([]string, []string, string, error) {
	var positionals []string
	var envVars []string
	pluginType := "grpc"

	i := 0
	for i < len(args) {
		switch {
		case args[i] == "--env" || args[i] == "-e":
			i++
			for i < len(args) && !strings.HasPrefix(args[i], "-") {
				envVars = append(envVars, args[i])
				i++
			}
		case args[i] == "--type":
			i++
			if i < len(args) {
				pluginType = args[i]
				i++
			}
		case args[i] == "--":
			i++
			positionals = append(positionals, args[i:]...)
			i = len(args)
		case strings.HasPrefix(args[i], "--env="):
			envVars = append(envVars, strings.TrimPrefix(args[i], "--env="))
			i++
		case strings.HasPrefix(args[i], "--type="):
			pluginType = strings.TrimPrefix(args[i], "--type=")
			i++
		default:
			positionals = append(positionals, args[i])
			i++
		}
	}

	return positionals, envVars, pluginType, nil
}
