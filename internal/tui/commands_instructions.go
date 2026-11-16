package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/topcheer/ggcode/internal/session"
)

// #5698: /instructions list | /instructions put <key> <value...> |
// /instructions remove <key>. The user-path surface for the durable keyed
// session-instruction layer. Writes carry user provenance; the agent-side
// gate rejects every non-user/parent-agent provenance before mutation.

func (m *Model) handleInstructionsCommand(parts []string) tea.Cmd {
	if m.agent == nil {
		m.chatWriteSystem(nextSystemID(), "Agent is not available in this session.")
		return nil
	}
	if len(parts) < 2 {
		m.chatWriteSystem(nextSystemID(), instructionsUsage())
		return nil
	}
	actor := session.InstructionActor{Kind: session.InstructionActorUser, Source: "tui:/instructions"}
	switch parts[1] {
	case "list":
		return m.handleInstructionsListCommand()
	case "put", "set":
		if len(parts) < 4 {
			m.chatWriteSystem(nextSystemID(), "usage: /instructions put <key> <value...>")
			return nil
		}
		key := parts[2]
		value := strings.Join(parts[3:], " ")
		if err := m.agent.PutKeyedInstruction(key, value, actor); err != nil {
			m.chatWriteSystem(nextSystemID(), fmt.Sprintf("instruction put failed: %v", err))
			return nil
		}
		m.chatWriteSystem(nextSystemID(), fmt.Sprintf("Instruction %q saved. It persists across compaction and restarts, and applies from the next turn.", key))
		return nil
	case "remove", "rm", "delete":
		if len(parts) < 3 {
			m.chatWriteSystem(nextSystemID(), "usage: /instructions remove <key>")
			return nil
		}
		key := parts[2]
		if err := m.agent.RemoveKeyedInstruction(key, actor); err != nil {
			m.chatWriteSystem(nextSystemID(), fmt.Sprintf("instruction remove failed: %v", err))
			return nil
		}
		m.chatWriteSystem(nextSystemID(), fmt.Sprintf("Instruction %q removed.", key))
		return nil
	default:
		m.chatWriteSystem(nextSystemID(), instructionsUsage())
		return nil
	}
}

func (m *Model) handleInstructionsListCommand() tea.Cmd {
	entries := m.agent.KeyedInstructions()
	if len(entries) == 0 {
		m.chatWriteSystem(nextSystemID(), "No keyed session instructions. Add one with /instructions put <key> <value...>")
		return nil
	}
	var b strings.Builder
	b.WriteString(fmt.Sprintf("Keyed session instructions (%d):\n", len(entries)))
	for _, e := range entries {
		b.WriteString(fmt.Sprintf("  %s  (by %s)  %s\n", e.Key, e.Actor.Kind, truncateForChat(strings.ReplaceAll(e.Value, "\n", " ⏎ "), 120)))
	}
	b.WriteString("\nChanges: /instructions put <key> <value...>  ·  /instructions remove <key>")
	m.chatWriteSystem(nextSystemID(), b.String())
	return nil
}

func instructionsUsage() string {
	return "usage: /instructions list | /instructions put <key> <value...> | /instructions remove <key>"
}
