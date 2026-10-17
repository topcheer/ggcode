package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/topcheer/ggcode/internal/bom"
	"github.com/topcheer/ggcode/internal/config"
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

plus the runtime execution components (AgBOM axis, OWASP Agent Control
Standard 2026-09):

  model         resolved LLM models (model + aux_model, deduped)  gate: config
  mcp-server    configured MCP server connections                 gate: config
  plugin        configured plugin entries                          gate: config
  config        the config file itself (provenance anchor)         gate: config

Runtime components are read from the default user config; when the config
cannot be loaded the report degrades to the learning-artifact inventory with
a warning.

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
			// AgBOM axis (r553): map the loaded config into runtime components.
			// A nil rc degrades to the plain learning-artifact inventory.
			var rc *bom.RuntimeConfig
			if cfgPath := config.ConfigPath(); cfgPath != "" {
				if cfg, err := config.Load(cfgPath); err == nil {
					rc = &bom.RuntimeConfig{ConfigPath: cfgPath}
					for _, m := range []string{cfg.Model, cfg.AuxModel} {
						if m != "" {
							rc.Models = append(rc.Models, m)
						}
					}
					dead := make(map[string]bool, len(cfg.DeletedMCPServers))
					for _, n := range cfg.DeletedMCPServers {
						dead[n] = true
					}
					for _, s := range cfg.MCPServers {
						rc.MCPServers = append(rc.MCPServers, bom.RuntimeMCPServer{
							Name: s.Name, Enabled: !dead[s.Name],
						})
					}
					for _, p := range cfg.Plugins {
						rc.Plugins = append(rc.Plugins, bom.RuntimePlugin{Name: p.Name, Path: p.Path})
					}
				}
			}
			rep, err := bom.BuildWithRuntime(workspace, home, rc)
			if err != nil {
				return fmt.Errorf("build bom: %w", err)
			}
			if rc == nil {
				rep.Warnings = append(rep.Warnings, "runtime components unavailable: user config not loaded")
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
