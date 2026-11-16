package agent

import (
	"fmt"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/provider"
)

// MCPInteractionStatus is the #5699 pending-answer surface the MCP manager
// implements so following turns carry the deferred-answer state to the model.
type MCPInteractionStatus interface {
	// PendingMCPAnswerCount reports live deferred MCP interactions.
	PendingMCPAnswerCount() int
	// PendingMCPAnswerLines returns one line per live pending answer.
	PendingMCPAnswerLines() []string
	// TakeAnsweredMCPAnswerLines drains one-shot digests of interactions the
	// user answered (resumed results), for exactly one following turn.
	TakeAnsweredMCPAnswerLines() []string
}

// SetMCPInteractionStatus wires the MCP pending-answer surface (manager).
// Nil (headless sessions without MCP wiring) disables the notice entirely.
func (a *Agent) SetMCPInteractionStatus(src MCPInteractionStatus) {
	if a == nil {
		return
	}
	a.mu.Lock()
	a.mcpStatus = src
	a.mu.Unlock()
}

// maybeInjectPendingMCPAnswers injects the deferred-answer notice into every
// following turn while pending MCP interactions await answers, plus one-shot
// digests of answered interactions (#5699 model-notice requirement).
func (a *Agent) maybeInjectPendingMCPAnswers() {
	a.mu.RLock()
	src := a.mcpStatus
	a.mu.RUnlock()
	if src == nil {
		return
	}

	answered := src.TakeAnsweredMCPAnswerLines()
	for _, digest := range answered {
		a.contextManager.Add(provider.Message{
			Role: "user",
			Content: []provider.ContentBlock{{
				Type: "text",
				Text: digest,
			}},
		})
		debug.Log("agent", "injected answered MCP interaction digest")
	}

	n := src.PendingMCPAnswerCount()
	if n == 0 {
		return
	}
	var b strings.Builder
	if n == 1 {
		b.WriteString("[mcp] 1 pending MCP interaction awaits an answer.\n")
	} else {
		b.WriteString(fmt.Sprintf("[mcp] %d pending MCP interactions await answers.\n", n))
	}
	b.WriteString("The user pressed Esc on an MCP server question, so a tool call is suspended (not failed).\n")
	for i, line := range src.PendingMCPAnswerLines() {
		if i >= 8 {
			b.WriteString(fmt.Sprintf("  ... and %d more\n", n-i))
			break
		}
		b.WriteString("  - " + line + "\n")
	}
	b.WriteString("Do not retry the suspended tool call. The user can answer via /mcp answers and /mcp answer <id> <text>;")
	b.WriteString(" once answered, the resumed result arrives in a following turn.")

	a.contextManager.Add(provider.Message{
		Role: "user",
		Content: []provider.ContentBlock{{
			Type: "text",
			Text: b.String(),
		}},
	})
	debug.Log("agent", "injected pending MCP answer notice: %d pending", n)
}
