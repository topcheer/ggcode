package agentruntime

import (
	"context"
	"fmt"
	"math"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/mcp"
	toolpkg "github.com/topcheer/ggcode/internal/tool"
)

func newMCPElicitationHandler(registry *toolpkg.Registry) mcp.ElicitationHandler {
	return func(ctx context.Context, params mcp.ElicitationParams) (*mcp.ElicitationResult, error) {
		var askTool *toolpkg.AskUserTool
		if registry != nil {
			if t, ok := registry.Get("ask_user"); ok {
				askTool, _ = t.(*toolpkg.AskUserTool)
			}
		}
		if askTool == nil {
			return nil, fmt.Errorf("ask_user tool unavailable; cannot route MCP elicitation")
		}

		if params.EffectiveMode() == mcp.ElicitationModeURL {
			return handleURLElicitation(ctx, askTool, params)
		}

		reqID := fmt.Sprintf("elicit-%d", nextElicitCounter())
		askReq := buildElicitationAskUser(reqID, params)

		resp, err := askTool.AskDirect(ctx, askReq)
		if err != nil {
			return nil, fmt.Errorf("elicitation interrupted: %w", err)
		}

		switch resp.Status {
		case toolpkg.AskUserStatusSubmitted:
			content := buildElicitationContent(params.Schema, resp)

			if missing := missingRequiredFields(params.Schema, content); len(missing) > 0 {
				debug.Log("mcp-elicitation", "declining accept: required field(s) left empty: %v", missing)
				return &mcp.ElicitationResult{
					Action: mcp.ElicitationActionDecline,
				}, nil
			}
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
}

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

				label := mcp.FormatEnumValue(opt)
				choices = append(choices, toolpkg.AskUserChoice{ID: label, Label: label})
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

func missingRequiredFields(schema mcp.ElicitationSchema, content map[string]any) []string {
	var missing []string
	for _, name := range schema.Required {
		v, ok := content[name]
		if !ok {
			missing = append(missing, name)
			continue
		}
		if s, isStr := v.(string); isStr && s == "" {
			missing = append(missing, name)
		}
	}
	return missing
}

func buildElicitationContent(schema mcp.ElicitationSchema, resp toolpkg.AskUserResponse) map[string]any {
	content := make(map[string]any)
	for _, ans := range resp.Answers {
		field, ok := schema.Properties[ans.ID]
		if !ok {
			continue
		}
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

		if len(field.Enum) > 0 {
			if entry, ok := enumEntryForLabel(field, val); ok {

				content[ans.ID] = entry
			}
			continue
		}
		switch field.Type {
		case "boolean":
			if b, err := parseBool(val); err == nil {
				content[ans.ID] = b
			}
		case "number":
			if f, err := parseFloat(val); err == nil {
				content[ans.ID] = f
			}
		case "integer":
			if f, err := parseIntAnswer(val); err == nil {
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

	return strconv.ParseFloat(strings.TrimSpace(s), 64)
}

func parseIntAnswer(s string) (float64, error) {
	f, err := parseFloat(s)
	if err != nil {
		return 0, err
	}
	if f != math.Trunc(f) {
		return 0, fmt.Errorf("not an integer: %s", s)
	}
	return f, nil
}

func enumEntryForLabel(field mcp.ElicitationFieldSchema, label string) (any, bool) {
	for _, opt := range field.Enum {
		if mcp.FormatEnumValue(opt) == label {
			return opt, true
		}
	}
	return nil, false
}

var elicitationCounter int64

func nextElicitCounter() int64 {
	return atomic.AddInt64(&elicitationCounter, 1)
}

func handleURLElicitation(ctx context.Context, askTool *toolpkg.AskUserTool, params mcp.ElicitationParams) (*mcp.ElicitationResult, error) {
	host := displayHost(params.URL)
	req := toolpkg.AskUserRequest{
		Title: "MCP Server Request — Out-of-Band URL",
		Questions: []toolpkg.AskUserQuestion{{
			ID:     "consent",
			Title:  params.Message,
			Prompt: fmt.Sprintf("The MCP server asks you to complete an out-of-band interaction (e.g. authorization or payment) at:\n%s\n(host: %s)\nApproving only tells the server you consented to visit; it does NOT share any data through ggcode.", params.URL, host),
			Kind:   toolpkg.AskUserKindSingle,
			Choices: []toolpkg.AskUserChoice{
				{ID: "accept", Label: "I opened the URL and want to continue"},
				{ID: "decline", Label: "Decline"},
			},
			AllowFreeform: false,
		}},
	}
	resp, err := askTool.AskDirect(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("url elicitation interrupted: %w", err)
	}
	switch {
	case resp.Status == toolpkg.AskUserStatusCancelled:
		return &mcp.ElicitationResult{Action: mcp.ElicitationActionCancel}, nil
	case resp.Status == toolpkg.AskUserStatusSubmitted && urlConsented(resp):
		return &mcp.ElicitationResult{Action: mcp.ElicitationActionAccept}, nil
	default:
		return &mcp.ElicitationResult{Action: mcp.ElicitationActionDecline}, nil
	}
}

func urlConsented(resp toolpkg.AskUserResponse) bool {
	for _, ans := range resp.Answers {
		if ans.ID != "consent" {
			continue
		}
		for _, id := range ans.SelectedChoiceIDs {
			if id == "accept" {
				return true
			}
		}
		for _, id := range ans.SelectedChoices {
			if id == "accept" {
				return true
			}
		}
	}
	return false
}

func displayHost(raw string) string {
	if u, err := url.Parse(raw); err == nil {
		return u.Host
	}
	return raw
}
