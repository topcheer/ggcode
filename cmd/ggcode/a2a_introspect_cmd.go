package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/topcheer/ggcode/internal/a2a"
	"github.com/topcheer/ggcode/internal/config"
)

// newA2AIntrospectCmd builds `ggcode a2a introspect <peer>` (#5593): a
// one-hop runtime snapshot of a same-workspace peer over the runtime/*
// namespace, authenticated by the shared domain key.
func newA2AIntrospectCmd() *cobra.Command {
	var mode string
	cmd := &cobra.Command{
		Use:   "introspect <peer>",
		Short: "Read a same-workspace peer instance's runtime snapshot (five faces)",
		Long: "Fetch the runtime/snapshot (busy / todos / crons / recent traces / conversation digest) from a peer ggcode instance " +
			"on the LAN. Authentication is the workspace domain key (HMAC proof): works only between instances sharing the " +
			"same workspace family (same .git root); a foreign key fails closed.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), 15*time.Second)
			defer cancel()

			cfgPath, err := resolveConfigFilePath()
			if err != nil {
				return fmt.Errorf("resolving config path: %w", err)
			}
			cfg, err := config.Load(cfgPath)
			if err != nil {
				return fmt.Errorf("config: %w", err)
			}
			reg, err := a2a.NewRegistry()
			if err != nil {
				return fmt.Errorf("a2a registry: %w", err)
			}
			apiKey := a2aAPIKey(cfg)
			rt := a2a.NewRemoteTool(reg, apiKey)
			inst, err := rt.FindInstance(args[0])
			if err != nil {
				// fall back: treat the argument as a raw endpoint URL
				inst = &a2a.InstanceInfo{Endpoint: args[0], Workspace: "direct-endpoint"}
			}
			wd, _ := os.Getwd()
			key, err := a2a.LoadOrCreateDomainKey(wd)
			if err != nil {
				return fmt.Errorf("domain key (run inside a git workspace): %w", err)
			}
			snap, err := a2a.IntrospectPeer(ctx, inst.Endpoint, apiKey, key, mode)
			if err != nil {
				return err
			}
			fmt.Print(a2a.FormatSnapshot(args[0], inst.Workspace, snap))
			return nil
		},
	}
	cmd.Flags().StringVar(&mode, "mode", a2a.IntrospectionFull, "snapshot mode: full or metadata")
	return cmd
}

func newA2ACmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "a2a",
		Short: "A2A peer utilities",
	}
	cmd.AddCommand(newA2AIntrospectCmd())
	return cmd
}
