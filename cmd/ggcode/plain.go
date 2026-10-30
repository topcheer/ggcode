package main

// Linear frontend for dumb/non-interactive terminals (#4604 Track 1).
//
// The interactive frontend is a Bubble Tea program: alternate-screen raw
// mode with high-frequency frame refresh. On terminals that cannot carry
// those escape sequences (TERM=dumb, screen sessions, limited SSH) that is
// exactly the garbled-output / repeated-warning / junk-character failure the
// issue reports, and --plain alone only stripped COLOR while still running
// the full TUI.
//
// This frontend runs NO Bubble Tea at all: a plain line-buffered stdin loop
// (no raw mode - dumb terminals are line-mode by nature) plus linear
// streaming print. It deliberately rides the SAME interactive runtime core
// the TUI uses (same agent, registry, session, config hot-swap), so every
// capability except the visual panels keeps working.
//
// Interactive-shaped tools degrade explicitly:
//   - ask_user is unregistered: there is no questionnaire UI to host it;
//     the agent instead ends its turn and the user answers by typing the
//     next line (multi-turn conversation is the native pause point).
//   - approvals default to Allow (GGCODE_PLAIN_APPROVE=deny switches to
//     Deny for read-only-style runs); there is no approval dialog either.

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/topcheer/ggcode/internal/agent"
	"github.com/topcheer/ggcode/internal/agentruntime"
	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/permission"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/tool"
)

// isDumbTerminal reports whether the environment cannot host the full TUI:
// TERM=dumb (or unset TERM on a tty). The check is centralized here so the
// piecemeal probes scattered across the TUI package (e.g. model.go's
// terminal-title writer) can stay point fixes while entry-level routing
// lives at the frontend fork itself.
func isDumbTerminal() bool {
	term := strings.TrimSpace(os.Getenv("TERM"))
	return term == "" || strings.EqualFold(term, "dumb")
}

// plainApproval is the linear frontend's interactive-approval stand-in:
// default Accept, configurable to Deny via GGCODE_PLAIN_APPROVE=deny.
func plainApproval(_ context.Context, toolName, _ string) permission.Decision {
	if strings.EqualFold(strings.TrimSpace(os.Getenv("GGCODE_PLAIN_APPROVE")), "deny") {
		debug.Log("plain", "approval denied (GGCODE_PLAIN_APPROVE=deny): tool=%s", toolName)
		return permission.Deny
	}
	return permission.Allow
}

// RunLinearREPL drives the interactive agent from a plain line-oriented
// loop. It returns a process exit code.
func RunLinearREPL(ag *agent.Agent, registry *tool.Registry, cfg *config.Config, cfgPath string, startupNotices []string) int {
	_ = cfgPath // cfg.Save() persists to the scope the config was loaded from

	// No questionnaire UI: the agent ends its turn and waits for the next
	// typed line instead of calling ask_user (#4604 design decision).
	if registry != nil && registry.Unregister("ask_user") {
		fmt.Println("[plain] ask_user disabled - the agent ends its turn and you answer by typing the next line")
	}
	if ag != nil {
		ag.SetApprovalHandler(plainApproval)
	}

	fmt.Println("ggcode linear frontend (no TUI) - /help for commands, Ctrl-D to exit")
	for _, n := range startupNotices {
		fmt.Println("[notice]", n)
	}

	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	ctx := context.Background()
	for {
		fmt.Print("ggcode> ")
		if !sc.Scan() {
			fmt.Println()
			return 0
		}
		line := strings.TrimSpace(sc.Text())
		switch {
		case line == "":
			continue
		case strings.HasPrefix(line, "/"):
			if exit := handlePlainCommand(line, ag, cfg); exit {
				return 0
			}
		default:
			runLinearTurn(ctx, ag, line)
		}
	}
}

// handlePlainCommand executes the linear command subset. It returns true
// when the REPL should exit.
func handlePlainCommand(line string, ag *agent.Agent, cfg *config.Config) bool {
	fields := strings.Fields(line)
	cmd := strings.ToLower(fields[0])
	args := fields[1:]
	switch cmd {
	case "/exit", "/quit", "/q":
		return true
	case "/help", "/?":
		fmt.Println("commands:")
		fmt.Println("  /status                     show active vendor/endpoint/model")
		fmt.Println("  /model <name>               hot-switch model (persists)")
		fmt.Println("  /provider <vendor> [ep]     switch vendor (optionally endpoint)")
		fmt.Println("  /endpoint <id>              switch endpoint on current vendor")
		fmt.Println("  /exit                       leave the REPL (also Ctrl-D)")
		fmt.Println("anything else is sent to the agent; approvals default to Accept")
		fmt.Println("(GGCODE_PLAIN_APPROVE=deny rejects instead); ask_user is disabled.")
	case "/status":
		if cfg == nil {
			fmt.Println("[plain] config unavailable")
			return false
		}
		if resolved, _, err := agentruntime.ResolveCurrentSelection(cfg); err != nil {
			fmt.Println("[plain] resolve failed:", err)
		} else {
			fmt.Printf("vendor=%s endpoint=%s model=%s\n", resolved.VendorID, resolved.EndpointID, resolved.Model)
		}
	case "/model":
		if len(args) != 1 {
			fmt.Println("usage: /model <name>")
			return false
		}
		switchPlainSelection(ag, cfg, "", "", args[0])
	case "/provider":
		if len(args) < 1 || len(args) > 2 {
			fmt.Println("usage: /provider <vendor> [endpoint]")
			return false
		}
		ep := ""
		if len(args) == 2 {
			ep = args[1]
		}
		switchPlainSelection(ag, cfg, args[0], ep, "")
	case "/endpoint":
		if len(args) != 1 {
			fmt.Println("usage: /endpoint <id>")
			return false
		}
		switchPlainSelection(ag, cfg, "", args[0], "")
	default:
		fmt.Println("[plain] unknown command:", cmd, "- /help for the list")
	}
	return false
}

// switchPlainSelection applies a vendor/endpoint/model switch through the
// same ActivateCurrentSelection + ApplyProviderToAgent funnel the TUI, IM,
// and daemon switchers use (empty fields inherit the current selection),
// then persists the config so the switch survives restarts.
func switchPlainSelection(ag *agent.Agent, cfg *config.Config, vendor, endpoint, model string) {
	if cfg == nil {
		fmt.Println("[plain] config unavailable")
		return
	}
	resolved, prov, err := agentruntime.ActivateCurrentSelection(cfg, vendor, endpoint, model)
	if err != nil {
		fmt.Println("[plain] switch failed:", err)
		return
	}
	if ag != nil {
		agentruntime.ApplyProviderToAgent(ag, prov, resolved)
	}
	if err := cfg.Save(); err != nil {
		fmt.Println("[plain] switch applied but not persisted:", err)
		return
	}
	fmt.Printf("[plain] now on vendor=%s endpoint=%s model=%s\n", resolved.VendorID, resolved.EndpointID, resolved.Model)
}

// runLinearTurn streams one agent turn as plain linear output: text deltas
// print as they arrive, tool boundaries get one bracketed line each, and
// errors surface as single lines - no refresh, no cursor movement, no
// ANSI beyond what NO_COLOR already disabled.
func runLinearTurn(ctx context.Context, ag *agent.Agent, line string) {
	if ag == nil {
		fmt.Println("[plain] agent unavailable")
		return
	}
	err := ag.RunStream(ctx, line, func(event provider.StreamEvent) {
		switch event.Type {
		case provider.StreamEventText:
			fmt.Print(event.Text)
		case provider.StreamEventToolCallDone:
			if event.Tool.Name != "" {
				fmt.Printf("\n[tool] %s\n", event.Tool.Name)
			}
		case provider.StreamEventError:
			fmt.Printf("\n[error] %v\n", event.Error)
		case provider.StreamEventDone:
			fmt.Println()
		}
	})
	if err != nil {
		fmt.Printf("\n[error] turn failed: %v\n", err)
	}
}
