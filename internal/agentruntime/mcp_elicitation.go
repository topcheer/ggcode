package agentruntime

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/mcp"
	toolpkg "github.com/topcheer/ggcode/internal/tool"
)

// Elicitation support wiring for MCP servers (protocol 2025-06-18+).
//
// When an MCP server sends an elicitation/create request, the handler routes
// it through the InteractionBroker's ask_user mechanism — the same path used
// by the agent's ask_user tool. This lets MCP servers collect structured input
// from the user without needing custom UI plumbing for each surface (TUI, IM,
// desktop, mobile).

var (
	elicitationBroker   *InteractionBroker
	elicitationBrokerMu sync.RWMutex
)

// SetElicitationBroker sets the interaction broker used to route MCP
// elicitation requests to the user. Called after the broker is created
// (typically during session setup).
func SetElicitationBroker(b *InteractionBroker) {
	elicitationBrokerMu.Lock()
	elicitationBroker = b
	elicitationBrokerMu.Unlock()
	debug.Log("mcp-elicitation", "elicitation broker set")
}

// mcpElicitationHandler implements mcp.ElicitationHandler.
//
// It converts the elicitation request into an AskUserRequest (which has
// first-class routing through all UI surfaces) and waits for the user's
// response. If no broker is available, it returns an error so the server
// knows elicitation is not possible in this context.
func mcpElicitationHandler(ctx context.Context, params mcp.ElicitationParams) (*mcp.ElicitationResult, error) {
	elicitationBrokerMu.RLock()
	broker := elicitationBroker
	elicitationBrokerMu.RUnlock()

	if broker == nil {
		return nil, fmt.Errorf("no interaction broker available for elicitation")
	}

	reqID := fmt.Sprintf("elicit-%d", nextElicitCounter())
	askReq := buildElicitationAskUser(reqID, params)

	resp, err := broker.AwaitAskUser(ctx, AskUserRequest{
		ID:      reqID,
		Request: askReq,
	})
	if err != nil {
		return nil, fmt.Errorf("elicitation interrupted: %w", err)
	}

	switch resp.Status {
	case toolpkg.AskUserStatusSubmitted:
		content := buildElicitationContent(params.Schema, resp)
		return &mcp.ElicitationResult{
			Action:  mcp.ElicitationActionAccept,
			Content: content,
		}, nil
	case toolpkg.AskUserStatusCancelled:
		return &mcp.ElicitationResult{
			Action: mcp.ElicitationActionCancel,
		}, nil
	default:
		return &mcp.ElicitationResult{
			Action: mcp.ElicitationActionDecline,
		}, nil
	}
}

// buildElicitationAskUser converts an MCP elicitation request into an
// AskUserRequest that can be routed through the existing interaction broker.
func buildElicitationAskUser(id string, params mcp.ElicitationParams) toolpkg.AskUserRequest {
	questions := make([]toolpkg.AskUserQuestion, 0, len(params.Schema.Properties))

	required := make(map[string]bool, len(params.Schema.Required))
	for _, r := range params.Schema.Required {
		required[r] = true
	}

	for _, name := range orderedFields(params.Schema.Properties, required) {
		field := params.Schema.Properties[name]

		kind := toolpkg.AskUserKindText
		choices := []toolpkg.AskUserChoice{}
		allowFreeform := false

		if len(field.Enum) > 0 {
			kind = toolpkg.AskUserKindSingle
			for _, opt := range field.Enum {
				choices = append(choices, toolpkg.AskUserChoice{ID: opt, Label: opt})
			}
			allowFreeform = false
		} else if field.Type == "boolean" {
			kind = toolpkg.AskUserKindSingle
			choices = []toolpkg.AskUserChoice{
				{ID: "true", Label: "Yes"},
				{ID: "false", Label: "No"},
			}
		}

		prompt := field.Description
		if prompt == "" {
			prompt = name
		}

		questions = append(questions, toolpkg.AskUserQuestion{
			ID:            name,
			Title:         params.Message,
			Prompt:        prompt,
			Kind:          kind,
			Choices:       choices,
			AllowFreeform: allowFreeform,
			Placeholder:   field.Format,
		})
	}

	return toolpkg.AskUserRequest{
		Title:     "MCP Server Request",
		Questions: questions,
	}
}

// buildElicitationContent maps the AskUserResponse answers back into the
// content map expected by the MCP elicitation result.
func buildElicitationContent(schema mcp.ElicitationSchema, resp toolpkg.AskUserResponse) map[string]any {
	content := make(map[string]any)
	for _, ans := range resp.Answers {
		field, ok := schema.Properties[ans.ID]
		if !ok {
			continue
		}
		// Extract the answer value
		var val string
		if ans.FreeformText != "" {
			val = ans.FreeformText
		} else if len(ans.SelectedChoices) > 0 {
			val = ans.SelectedChoices[0]
		} else if len(ans.SelectedChoiceIDs) > 0 {
			val = ans.SelectedChoiceIDs[0]
		}
		if val == "" {
			continue
		}
		switch field.Type {
		case "boolean":
			if b, err := parseBool(val); err == nil {
				content[ans.ID] = b
			}
		case "number", "integer":
			if f, err := parseFloat(val); err == nil {
				content[ans.ID] = f
			}
		default:
			content[ans.ID] = val
		}
	}
	return content
}

func orderedFields(props map[string]mcp.ElicitationFieldSchema, required map[string]bool) []string {
	var reqFirst, rest []string
	for name := range props {
		if required[name] {
			reqFirst = append(reqFirst, name)
		} else {
			rest = append(rest, name)
		}
	}
	for i := 1; i < len(reqFirst); i++ {
		for j := i; j > 0 && reqFirst[j] < reqFirst[j-1]; j-- {
			reqFirst[j], reqFirst[j-1] = reqFirst[j-1], reqFirst[j]
		}
	}
	for i := 1; i < len(rest); i++ {
		for j := i; j > 0 && rest[j] < rest[j-1]; j-- {
			rest[j], rest[j-1] = rest[j-1], rest[j]
		}
	}
	return append(reqFirst, rest...)
}

func parseBool(s string) (bool, error) {
	switch s {
	case "true", "yes", "1", "on", "Yes":
		return true, nil
	case "false", "no", "0", "off", "No":
		return false, nil
	}
	return false, fmt.Errorf("not a boolean: %s", s)
}

func parseFloat(s string) (float64, error) {
	var f float64
	_, err := fmt.Sscanf(s, "%g", &f)
	return f, err
}

var elicitationCounter int64

// nextElicitCounter is called from up to 8 concurrent elicitation workers
// (serverReqSem cap, safego.Go handlers) - the old non-atomic increment
// let two workers read the same value, mint the same "elicit-N" ID, and
// the second map write orphaned the first waiter's channel (its user
// answer never arrived; only the 5-minute timeout fired) (#1589-A).
func nextElicitCounter() int64 {
	return atomic.AddInt64(&elicitationCounter, 1)
}
