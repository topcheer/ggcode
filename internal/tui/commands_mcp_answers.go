package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/topcheer/ggcode/internal/mcp"
)

// #5699: /mcp answers | /mcp answer <id> <text> | /mcp discard <id>.
// /tasks-equivalent surface for the Esc-pending-answer queue: lists deferred
// MCP interactions, answers them (resuming the call with the carried
// requestState), and discards stale ones.

const mcpAnswerCommandTimeout = 2 * time.Minute

func (m *Model) handleMCPAnswersListCommand() tea.Cmd {
	mgr := m.mcpManager
	if mgr == nil {
		m.chatWriteSystem(nextSystemID(), "MCP manager is not available in this session.")
		return nil
	}
	pending := mgr.PendingInputAnswers()
	if len(pending) == 0 {
		m.chatWriteSystem(nextSystemID(), "No pending MCP answers. (Esc on an MCP server question queues it here.)")
		return nil
	}
	now := time.Now()
	var b strings.Builder
	b.WriteString(fmt.Sprintf("Pending MCP answers: %d\n", len(pending)))
	for _, pa := range pending {
		tool := pa.ToolName
		if tool == "" {
			tool = pa.Method
		}
		b.WriteString(fmt.Sprintf("  %s  server='%s' tool=%s expires in %s", pa.ID, pa.Server, tool, formatPendingExpiry(pa.ExpiresIn(now))))
		if msg := strings.TrimSpace(pa.StatusMessage); msg != "" {
			b.WriteString("  — " + truncateForChat(msg, 100))
		}
		b.WriteString("\n")
	}
	b.WriteString("\nAnswer with: /mcp answer <id> <text>   (multi-field: key=value pairs, or --json '{...}')\n")
	b.WriteString("Discard with: /mcp discard <id>")
	m.chatWriteSystem(nextSystemID(), b.String())
	return nil
}

func formatPendingExpiry(d time.Duration) string {
	if d <= 0 {
		return "0s"
	}
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	return fmt.Sprintf("%dm", int(d.Minutes()))
}

func truncateForChat(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	return s[:limit] + "…"
}

// parseMCPAnswerArgs splits ["--json", "{...}"] or ["pa-1", "some", "text"]
// into (id, text, rawJSON).
func parseMCPAnswerArgs(args []string) (id, text string, rawJSON bool, err error) {
	rest := args
	if len(rest) > 0 && rest[0] == "--json" {
		rawJSON = true
		rest = rest[1:]
	}
	if len(rest) < 1 {
		return "", "", false, fmt.Errorf("usage: /mcp answer <id> <text...> (or /mcp answer --json '<id-keyed object>')")
	}
	if rawJSON {
		// With --json the whole remaining text is the JSON payload; the id
		// must come first when provided separately.
		if len(rest) >= 2 && !strings.HasPrefix(strings.TrimSpace(rest[0]), "{") {
			return rest[0], strings.Join(rest[1:], " "), true, nil
		}
		return "", strings.Join(rest, " "), true, nil
	}
	id = rest[0]
	if len(rest) < 2 {
		return "", "", false, fmt.Errorf("usage: /mcp answer <id> <text...>")
	}
	return id, strings.Join(rest[1:], " "), false, nil
}

func (m *Model) handleMCPAnswerCommand(args []string) tea.Cmd {
	mgr := m.mcpManager
	if mgr == nil {
		m.chatWriteSystem(nextSystemID(), "MCP manager is not available in this session.")
		return nil
	}
	id, text, rawJSON, err := parseMCPAnswerArgs(args)
	if err != nil {
		m.chatWriteSystem(nextSystemID(), err.Error())
		return nil
	}
	if id == "" && !rawJSON {
		m.chatWriteSystem(nextSystemID(), "missing pending id; list with /mcp answers")
		return nil
	}
	// When the id is unknown (or --json omitted it), resolve a single
	// pending entry automatically when exactly one is live.
	target := id
	pending := mgr.PendingInputAnswers()
	if target == "" {
		if len(pending) != 1 {
			m.chatWriteSystem(nextSystemID(), fmt.Sprintf("--json payload without id: %d pending answers live; pass the id first (/mcp answer <id> --json '...')", len(pending)))
			return nil
		}
		target = pending[0].ID
	} else {
		found := false
		for _, pa := range pending {
			if pa.ID == target {
				found = true
				break
			}
		}
		if !found {
			m.chatWriteSystem(nextSystemID(), fmt.Sprintf("no pending MCP answer %q; list with /mcp answers", target))
			return nil
		}
	}
	var inputRequests map[string]json.RawMessage
	for _, pa := range pending {
		if pa.ID == target {
			inputRequests = pa.InputRequests
			break
		}
	}

	responses, err := mcp.BuildPendingAnswerResponses(inputRequests, text, rawJSON)
	if err != nil {
		m.chatWriteSystem(nextSystemID(), "building answer: "+err.Error())
		return nil
	}

	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), mcpAnswerCommandTimeout)
		defer cancel()
		res, err := mgr.AnswerPendingInput(ctx, target, responses)
		if err != nil {
			return streamMsg(fmt.Sprintf("mcp answer %s failed: %v", target, err))
		}
		if res.Requeued != nil {
			return streamMsg(fmt.Sprintf("MCP answer %s accepted; the server asked a follow-up question — new pending answer %s queued (see /mcp answers).", target, res.Requeued.ID))
		}
		if res.Result != nil {
			summary := mcp.SummarizeCallToolResult(res.Result, 2000)
			if summary == "" {
				summary = "(empty tool result)"
			}
			return streamMsg(fmt.Sprintf("MCP answer %s resumed and completed:\n%s", target, summary))
		}
		return streamMsg(fmt.Sprintf("MCP answer %s resumed and completed:\n%s", target, string(res.Raw)))
	}
}

func (m *Model) handleMCPDiscardCommand(args []string) tea.Cmd {
	mgr := m.mcpManager
	if mgr == nil {
		m.chatWriteSystem(nextSystemID(), "MCP manager is not available in this session.")
		return nil
	}
	if len(args) < 1 {
		m.chatWriteSystem(nextSystemID(), "usage: /mcp discard <id>")
		return nil
	}
	if !mgr.DiscardPendingInput(args[0]) {
		m.chatWriteSystem(nextSystemID(), fmt.Sprintf("no pending MCP answer %q (it may have expired or been discarded already)", args[0]))
		return nil
	}
	m.chatWriteSystem(nextSystemID(), fmt.Sprintf("Discarded pending MCP answer %s.", args[0]))
	return nil
}
