package main

import (
	"context"
	"fmt"
	"os"
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
		seat := ""
		if inst.Nick != "" {
			if inst.Role != "" {
				seat = fmt.Sprintf(" [Seat: %s (role=%s)]", inst.Nick, inst.Role)
			} else {
				seat = fmt.Sprintf(" [Seat: %s]", inst.Nick)
			}
		}
		b.WriteString(fmt.Sprintf("  - %s (%s) %s%s\n", inst.ID, inst.Workspace, inst.Endpoint, seat))
	}
	return b.String()
}

// newA2AIntrospectCmd builds `ggcode a2a introspect <peer>` (#5597 v3 /
// #5602 roster fix): plain one-hop runtime/snapshot - no HMAC, the
// server's roster gate and first auth factor apply.
func newA2AIntrospectCmd() *cobra.Command {
	var depth string
	var faces []string
	var category string
	cmd := &cobra.Command{
		Use:   "introspect <peer|list|url>",
		Short: "Read a peer instance's current context (eight faces, verbatim)",
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
			// #5617: hub-less one-shot has no registered self, so discovery's
			// roster-identity overlay needs the working dir explicitly.
			wd, _ := os.Getwd()
			rt.SetRosterDir(wd)

			target := args[0]

			// #5602: a one-shot CLI run has an EMPTY roster cache (the 15s
			// background refresh never gets a chance). Browse mDNS
			// synchronously before any name resolution.
			instances, derr := reg.Discover()
			// #5605(C): one-shot mDNS window can still come up empty;
			// the workspace roster file is the zero-wait warm fallback.
			if len(instances) == 0 {
				wd, _ := os.Getwd()
				instances = a2a.ReadRoster(wd, "")
			}

			// #5667: resolve the peer's workspace domain key for the
			// declared-workspace proof - advertised on the discovery roster,
			// with the own-workspace key file as the same-host fallback.
			peerKey := func(endpoint string) []byte {
				if key := a2a.DomainKeyForEndpoint(instances, endpoint); key != nil {
					return key
				}
				wd, _ := os.Getwd()
				if key, err := a2a.LoadOrCreateWorkspaceDomainKey(wd); err == nil {
					return key
				}
				return nil
			}

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
				snap, err := a2a.IntrospectPeerV3(ctx, endpoint, apiKey, func() string { wd, _ := os.Getwd(); return wd }(), peerKey(endpoint), a2a.SnapshotRequest{Depth: depth, Faces: faces, Category: category})
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
			snap, err := a2a.IntrospectPeerV3(ctx, inst.Endpoint, apiKey, func() string { wd, _ := os.Getwd(); return wd }(), peerKey(inst.Endpoint), a2a.SnapshotRequest{Depth: depth, Faces: faces, Category: category})
			if err != nil {
				return err
			}
			fmt.Print(a2a.FormatSnapshotV3(target, inst.Workspace, snap))
			return nil
		},
	}
	cmd.Flags().StringVar(&depth, "depth", a2a.DepthFull, "depth tier: brief or full")
	cmd.Flags().StringSliceVar(&faces, "faces", nil, "subset of faces (comma-separated)")
	cmd.Flags().StringVar(&category, "category", "", "category pull: mounts (env) / state (status+inflight+children+schedule+plan) / content (conversation+activity)")
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
