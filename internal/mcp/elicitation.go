package mcp

import (
	"context"
	"encoding/json"
	"fmt"
)

// Elicitation support — MCP protocol 2025-06-18+.
//
// Elicitation allows an MCP server to request structured information from the
// user via the client. Unlike sampling (which asks the LLM), elicitation asks
// the human directly. This is critical for:
//   - API keys and credentials (server can't hardcode them)
//   - Configuration choices (deploy target, environment, etc.)
//   - Confirmation prompts for sensitive operations
//   - Free-form text input ("describe the bug you're seeing")
//
// The server sends an elicitation/create request with a JSON schema describing
// the desired input. The client presents this to the user (via TUI prompt,
// desktop dialog, or IM approval flow) and returns the collected data.
//
// Security considerations:
//   - The server-provided schema is validated to prevent excessively complex
//     schemas that could confuse users or overwhelm the UI.
//   - The handler decides whether to actually show the prompt based on the
//     current permission mode (e.g., reject in fully autonomous mode).
//   - The server-provided message is treated as untrusted content.

// ElicitationFieldSchema describes a single field in an elicitation request.
// It is a subset of JSON Schema tailored for form-style input.
type ElicitationFieldSchema struct {
	Type        string `json:"type"`                  // "string", "number", "boolean"
	Description string `json:"description,omitempty"` // human-readable label/explanation
	// Optional constraints for string fields
	Format string `json:"format,omitempty"` // e.g. "email", "uri", "date-time"
	// Optional enum for constrained choices
	Enum []string `json:"enum,omitempty"`
}

// ElicitationSchema is the schema sent by the server to describe what input
// it needs from the user. Maps field names to their schema.
type ElicitationSchema struct {
	Type       string                            `json:"type"` // always "object"
	Properties map[string]ElicitationFieldSchema `json:"properties"`
	Required   []string                          `json:"required,omitempty"`
}

// ElicitationParams is the parameters for an elicitation/create request.
type ElicitationParams struct {
	Message string            `json:"message"`         // prompt text shown to the user
	Schema  ElicitationSchema `json:"requestedSchema"` // schema describing desired fields
}

// ElicitationAction is the user's response action.
type ElicitationAction string

const (
	ElicitationActionAccept  ElicitationAction = "accept"  // user provided the requested data
	ElicitationActionDecline ElicitationAction = "decline" // user declined to provide data
	ElicitationActionCancel  ElicitationAction = "cancel"  // user dismissed the prompt
)

// ElicitationResult is the response sent back to the server.
type ElicitationResult struct {
	Action  ElicitationAction `json:"action"`
	Content map[string]any    `json:"content,omitempty"` // field values when action=accept
}

// ElicitationHandler processes an elicitation/create request from an MCP server.
// The handler should present the message and schema to the user, collect their
// input, and return the result. If elicitation is not permitted (e.g. running
// in non-interactive mode), return an error.
type ElicitationHandler func(ctx context.Context, params ElicitationParams) (*ElicitationResult, error)

// ParseElicitationParams extracts elicitation parameters from a JSON-RPC request.
func ParseElicitationParams(raw json.RawMessage) (ElicitationParams, error) {
	var p ElicitationParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return p, err
	}
	return p, nil
}

// MaxElicitationFields caps the number of fields in an elicitation schema to
// prevent servers from overwhelming the user with a giant form.
const MaxElicitationFields = 20

// ValidateElicitationSchema checks that a server-provided elicitation schema
// is well-formed and within reasonable bounds. Returns an error if the schema
// is invalid or too complex.
func ValidateElicitationSchema(s ElicitationSchema) error {
	if s.Type != "" && s.Type != "object" {
		return fmt.Errorf("elicitation schema type must be \"object\", got %q", s.Type)
	}
	if len(s.Properties) == 0 {
		return fmt.Errorf("elicitation schema must have at least one property")
	}
	if len(s.Properties) > MaxElicitationFields {
		return fmt.Errorf("elicitation schema has %d properties, max is %d", len(s.Properties), MaxElicitationFields)
	}

	allowedTypes := map[string]bool{
		"string":  true,
		"number":  true,
		"integer": true,
		"boolean": true,
	}

	for name, field := range s.Properties {
		if name == "" {
			return fmt.Errorf("elicitation schema has empty property name")
		}
		if !allowedTypes[field.Type] {
			return fmt.Errorf("elicitation field %q has unsupported type %q (allowed: string, number, integer, boolean)", name, field.Type)
		}
	}

	// Verify all required fields exist in properties.
	propSet := make(map[string]bool, len(s.Properties))
	for k := range s.Properties {
		propSet[k] = true
	}
	for _, req := range s.Required {
		if !propSet[req] {
			return fmt.Errorf("elicitation required field %q not found in properties", req)
		}
	}

	return nil
}
