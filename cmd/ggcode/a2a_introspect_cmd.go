package main

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/topcheer/ggcode/internal/a2a"
	"github.com/topcheer/ggcode/internal/config"
)

// newA2AIntrospectCmd builds `ggcode a2a introspect <peer>` (#5597 v3):
// plain one-hop runtime/snapshot - no HMAC, the server's roster gate and
// first auth factor apply.
func newA2AIntrospectCmd() *cobra.Command {
	var depth string
	var faces []string
	cmd := &cobra.Command{
		Use:   "introspect <peer>",
		Short: "Read a peer instance's current context (seven faces, verbatim)",
		Args:  cobra.ExactArgs(1),
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
				inst = &a2a.InstanceInfo{Endpoint: args[0], Workspace: "direct-endpoint"}
			}
			snap, err := a2a.IntrospectPeerV3(ctx, inst.Endpoint, apiKey, a2a.SnapshotRequest{Depth: depth, Faces: faces})
			if err != nil {
				return err
			}
			fmt.Print(a2a.FormatSnapshotV3(args[0], inst.Workspace, snap))
			return nil
		},
	}
	cmd.Flags().StringVar(&depth, "depth", a2a.DepthFull, "depth tier: brief or full")
	cmd.Flags().StringSliceVar(&faces, "faces", nil, "subset of faces (comma-separated)")
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
