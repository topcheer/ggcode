package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/topcheer/ggcode/internal/bom"
)

// newBOMCmd creates the "ggcode bom" subcommand: a read-only bill of materials
// for everything the agent has learned - skills, staged skills, memories, and
// trajectory insights - with content hash, source, and the promotion gate each
// artifact passed. Answers the audit question "what is the agent made of right
// now, and through which gate did each piece enter?" (sa-78).
//
// Usage:
//
//	ggcode bom            - aggregate the current workspace + user home
//	ggcode bom --json     - same, machine-readable (default output is JSON)
func newBOMCmd() *cobra.Command {
	var workspace, home string
	cmd := &cobra.Command{
		Use:   "bom",
		Short: "Print a bill of materials for the agent's learning artifacts",
		Long: `Aggregate every persisted learning artifact that currently shapes agent
behavior into one deterministic inventory:

  skill         active skills (.ggcode/skills/*/SKILL.md)          gate: knight
  skill-staged  skills awaiting promotion (skills-staging/)   gate: knight-staging
  memory        persisted memories (memory/*.md)              gate: auto|manual
  traj-insight  trajectory learnings (trajectory-learnings.jsonl) gate: holdout

Each entry carries a content hash (sha256, first 12 hex chars), size, source
path, scope (project|global), and enable state. Read-only: nothing is written.

Examples:
  ggcode bom                    Inventory for the current workspace
  ggcode bom --workspace /path  Inventory for another workspace`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if workspace == "" {
				wd, err := os.Getwd()
				if err != nil {
					return fmt.Errorf("resolve workspace: %w", err)
				}
				workspace = wd
			}
			if home == "" {
				h, err := os.UserHomeDir()
				if err != nil {
					return fmt.Errorf("resolve home: %w", err)
				}
				home = h
			}
			rep, err := bom.Build(workspace, home)
			if err != nil {
				return fmt.Errorf("build bom: %w", err)
			}
			enc := json.NewEncoder(cmd.OutOrStdout())
			enc.SetIndent("", "  ")
			return enc.Encode(rep)
		},
	}
	cmd.Flags().StringVar(&workspace, "workspace", "", "workspace directory (default: current directory)")
	cmd.Flags().StringVar(&home, "home", "", "user home directory (default: $HOME)")
	return cmd
}
