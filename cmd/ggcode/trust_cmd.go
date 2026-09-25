package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/topcheer/ggcode/internal/trust"
)

// newTrustCmd builds `ggcode trust`: the explicit, scriptable approval
// surface for the workspace trust gate. It lets headless/CI users grant
// trust without an interactive prompt.
func newTrustCmd() *cobra.Command {
	var (
		list   bool
		revoke bool
		forget bool
	)
	cmd := &cobra.Command{
		Use:   "trust [dir]",
		Short: "Manage workspace trust (project skills/commands/MCP/memory)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := ""
			if len(args) > 0 {
				dir = args[0]
			} else {
				dir, _ = os.Getwd()
			}
			switch {
			case list:
				entries, err := trust.Entries()
				if err != nil {
					return err
				}
				if len(entries) == 0 {
					fmt.Println("no trust decisions recorded")
					return nil
				}
				for _, e := range entries {
					status := "trusted"
					if !e.Trusted {
						status = "revoked"
					}
					fmt.Printf("%-9s %s (host: %s)\n", status, e.Path, e.Host)
				}
				return nil
			case revoke:
				if err := trust.Untrust(dir); err != nil {
					return err
				}
				fmt.Printf("revoked: %s\nproject skills/commands, .mcp.json servers, and project memory are disabled for this folder.\n", trust.Normalize(dir))
				return nil
			case forget:
				if err := trust.Forget(dir); err != nil {
					return err
				}
				fmt.Printf("forgot: %s (inherits the nearest ancestor decision again)\n", trust.Normalize(dir))
				return nil
			}
			if err := trust.Trust(dir); err != nil {
				return err
			}
			fmt.Printf("trusted: %s\nproject skills/commands, .mcp.json servers, and project memory are now enabled for this folder.\n", trust.Normalize(dir))
			return nil
		},
	}
	cmd.Flags().BoolVar(&list, "list", false, "list recorded trust decisions")
	cmd.Flags().BoolVar(&revoke, "revoke", false, "revoke trust for the folder (explicit restriction)")
	cmd.Flags().BoolVar(&forget, "forget", false, "remove the decision so the folder inherits its nearest ancestor again")
	return cmd
}
