package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/topcheer/ggcode/internal/trust"
)

// workspaceTrustGate is the interactive human-in-the-loop approval surface
// for workspace trust (r89). Cloned repositories can ship project-scoped
// skills, slash commands, MCP server manifests, and prompt-memory files
// that ggcode auto-loads — an untrusted checkout must not get them for
// free. Fail-closed: unknown folder → restricted; explicit yes → trusted
// and persisted; anything else (EOF, non-TTY, no) → restricted.
//
// Runs before the TUI takes over the terminal, so plain stdin I/O is safe.
func workspaceTrustGate(workingDir string) {
	if trust.IsTrusted(workingDir) {
		return
	}
	if !trust.NeedsTrustPrompt(workingDir) {
		return
	}
	fmt.Fprintf(os.Stderr, "[ggcode] This folder ships project-scoped skills/commands, MCP servers (.mcp.json), or memory files.\n")
	if !stdinIsTerminal() {
		fmt.Fprintf(os.Stderr, "[ggcode] Untrusted workspace: project-scoped assets stay disabled (non-interactive). Run `ggcode trust` to enable.\n")
		return
	}
	fmt.Fprintf(os.Stderr, "[ggcode] Trust the files in this folder and enable them? [y/N] ")
	answer, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil {
		fmt.Fprintf(os.Stderr, "\n[ggcode] No answer read — staying restricted. Run `ggcode trust` to enable later.\n")
		return
	}
	answer = strings.ToLower(strings.TrimSpace(answer))
	if answer != "y" && answer != "yes" {
		fmt.Fprintf(os.Stderr, "[ggcode] Staying restricted: project-scoped assets are disabled. Run `ggcode trust` to enable later.\n")
		return
	}
	if err := trust.Trust(workingDir); err != nil {
		fmt.Fprintf(os.Stderr, "[ggcode] Trust store write failed (%v) — staying restricted.\n", err)
		return
	}
	fmt.Fprintf(os.Stderr, "[ggcode] Workspace trusted: project skills/commands/MCP/memory enabled. Manage with `ggcode trust --list|--revoke`.\n")
}

func stdinIsTerminal() bool {
	info, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}
