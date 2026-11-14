package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/topcheer/ggcode/internal/a2a"
	"github.com/topcheer/ggcode/internal/config"
)

// looksLikeEndpoint reports whether s is a URL-shaped input (http(s):// or
// host:port). Only such inputs fall back to direct-endpoint mode (#5602):
// a seat NAME that misses the roster must fail with the roster listed,
// not be silently POSTed as a bogus URL.
func looksLikeEndpoint(s string) bool {
	if strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://") {
		return true
	}
	h, _, err := splitHostPort(s)
	return err == nil && h != "" && !strings.Contains(s, " ")
}

func splitHostPort(s string) (string, string, error) {
	i := strings.LastIndex(s, ":")
	if i <= 0 || i == len(s)-1 {
		return "", "", fmt.Errorf("no port")
	}
	return s[:i], s[i+1:], nil
}

// rosterLines renders the current roster one-per-line for error output.
func rosterLines(instances []a2a.InstanceInfo) string {
	if len(instances) == 0 {
		return "  (roster empty - no peers discovered on the LAN yet)"
	}
	var b strings.Builder
	for _, inst := range instances {
		b.WriteString(fmt.Sprintf("  - %s (%s) %s\n", inst.ID, inst.Workspace, inst.Endpoint))
	}
	return b.String()
}

// newA2AIntrospectCmd builds `ggcode a2a introspect <peer>` (#5597 v3 /
// #5602 roster fix): plain one-hop runtime/snapshot - no HMAC, the
// server's roster gate and first auth factor apply.
func newA2AIntrospectCmd() *cobra.Command {
	var depth string
	var faces []string
	cmd := &cobra.Command{
		Use:   "introspect <peer|list|url>",
		Short: "Read a peer instance's current context (seven faces, verbatim)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), 20*time.Second)
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

			target := args[0]

			// #5602: a one-shot CLI run has an EMPTY roster cache (the 15s
			// background refresh never gets a chance). Browse mDNS
			// synchronously before any name resolution.
			instances, derr := reg.Discover()

			// list semantics (#5602): mirror the agent tool's target='list'.
			if target == "list" {
				fmt.Println("Discovered instances:")
				fmt.Print(rosterLines(instances))
				if derr != nil {
					return fmt.Errorf("discovery warning: %w", derr)
				}
				return nil
			}

			if looksLikeEndpoint(target) {
				endpoint := target
				if !strings.Contains(endpoint, "://") {
					endpoint = "http://" + endpoint
				}
				snap, err := a2a.IntrospectPeerV3(ctx, endpoint, apiKey, a2a.SnapshotRequest{Depth: depth, Faces: faces})
				if err != nil {
					return err
				}
				fmt.Print(a2a.FormatSnapshotV3(endpoint, "direct-endpoint", snap))
				return nil
			}

			// Name path: roster must resolve it; a miss is an ERROR with
			// the roster listed, never a silent bogus-URL fallback (#5602).
			inst, err := rt.FindInstance(target)
			if err != nil {
				return fmt.Errorf("seat %q not found in the discovered roster:\n%s\n(discovery: %v)", target, rosterLines(instances), derr)
			}
			snap, err := a2a.IntrospectPeerV3(ctx, inst.Endpoint, apiKey, a2a.SnapshotRequest{Depth: depth, Faces: faces})
			if err != nil {
				return err
			}
			fmt.Print(a2a.FormatSnapshotV3(target, inst.Workspace, snap))
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
