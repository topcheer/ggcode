package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"github.com/topcheer/ggcode/internal/acp"
	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/permission"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/tool"
)

func newACPCommand(cfgFile *string) *cobra.Command {
	var acpVendor string
	var acpEndpoint string
	var acpModel string

	cmd := &cobra.Command{
		Use:   "acp",
		Short: "Start ggcode as an ACP agent (stdio JSON-RPC)",
		Long: "Start ggcode in Agent Client Protocol mode, communicating via stdin/stdout JSON-RPC 2.0.\n" +
			"All log output goes to stderr; stdout is reserved exclusively for JSON-RPC messages.\n\n" +
			"Multiple instances can run simultaneously — each IDE window starts its own process.\n" +
			"Use --config to specify per-workspace configuration files.",
		RunE: func(cmd *cobra.Command, args []string) error {

			resolvedCfg := *cfgFile
			if resolvedCfg == "" {
				path, err := resolveConfigFilePath()
				if err != nil {
					return fmt.Errorf("resolving config path: %w", err)
				}
				resolvedCfg = path
			}

			cfg, err := config.Load(resolvedCfg)
			if err != nil {
				return fmt.Errorf("loading config: %w", err)
			}

			if acpVendor != "" {
				cfg.Vendor = acpVendor
			}
			if acpEndpoint != "" {
				cfg.Endpoint = acpEndpoint
			}
			if acpModel != "" {
				cfg.Model = acpModel
			}

			resolved, err := cfg.ResolveActiveEndpoint()
			if err != nil {
				return fmt.Errorf("resolving endpoint: %w", err)
			}
			if resolved.APIKey == "" {
				return fmt.Errorf("missing API key for vendor %s endpoint %s", resolved.VendorID, resolved.EndpointID)
			}

			prov, err := provider.NewProvider(resolved)
			if err != nil {
				return fmt.Errorf("creating provider: %w", err)
			}

			workingDir, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("resolving working directory: %w", err)
			}

			registry := tool.NewRegistry()
			policy := permission.NewConfigPolicyWithMode(nil, cfg.AllowedDirs, permission.BypassMode)
			if err := tool.RegisterBuiltinTools(registry, policy, workingDir, cfg.ProtectedPaths, tool.NewSandboxPolicy(cfg.Sandbox.Enabled, cfg.Sandbox.AllowNetwork, cfg.Sandbox.ExtraWritePaths)); err != nil {
				return fmt.Errorf("registering tools: %w", err)
			}

			transport := acp.NewTransport(os.Stdin, os.Stdout)
			handler := acp.NewHandler(cfg, registry, transport, prov)

			ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()

			runErr := handler.Run(ctx)
			if jm := registry.JobManager(); jm != nil {
				jm.ShutdownAll(2 * time.Second)
			}
			return runErr
		},
	}

	cmd.Flags().StringVar(&acpVendor, "vendor", "", "override vendor (e.g., openai, anthropic)")
	cmd.Flags().StringVar(&acpEndpoint, "endpoint", "", "override endpoint name")
	cmd.Flags().StringVar(&acpModel, "model", "", "override model name")

	return cmd
}
