package main

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/mcpserve"
	"github.com/topcheer/ggcode/internal/version"
)

func newMCPServeCmd(cfgFile *string) *cobra.Command {
	return &cobra.Command{
		Use:   "serve",
		Short: "Expose ggcode as an MCP server over stdio",
		Long: `Run ggcode as a Model Context Protocol (MCP) server speaking JSON-RPC 2.0 over stdio.

Other MCP-capable agents and clients (Claude Code, Cursor, VS Code, ...) can
register ggcode as a tool provider and drive it as a coding sub-agent:

  claude mcp add ggcode -- ggcode mcp serve

Exposed tools:
  ggcode_run           Run one headless agent turn (ggcode -p) and return the answer
  ggcode_session_list  List recent local sessions
  ggcode_session_read  Read one session transcript

Each ggcode_run invocation spawns an isolated headless process that reuses the
local ggcode configuration (model, vendor, permission policy). Diagnostics go
to stderr; only JSON-RPC frames are written to stdout.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			debug.Init()
			path := *cfgFile
			if path == "" {
				path = config.ConfigPath()
			}
			exe, err := os.Executable()
			if err != nil {
				return fmt.Errorf("resolving executable: %w", err)
			}
			srv := mcpserve.New(mcpserve.Options{
				Version:    version.Display(),
				ExecPath:   exe,
				ConfigPath: path,
			})
			return srv.Serve(context.Background(), os.Stdin, os.Stdout)
		},
	}
}
