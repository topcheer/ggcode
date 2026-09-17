package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
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

// Elicitation modes (MCP 2025-11-25). Form mode collects structured data
// in-band; URL mode directs the user to an external URL for out-of-band
// interactions (auth flows, payments) that must not pass through the client.
// For backwards compatibility servers MAY omit mode for form requests, so an
// empty mode is treated as form.
const (
	ElicitationModeForm = "form"
	ElicitationModeURL  = "url"
)

// ErrCodeURLElicitationRequired is the JSON-RPC error code (-32042) a server
// returns when a tool call cannot proceed until a URL mode elicitation is
// completed (MCP 2025-11-25). The error data carries the required
// elicitations; see URLElicitationRequiredData.
const ErrCodeURLElicitationRequired = -32042

// URLElicitationRequiredInfo is one required URL mode elicitation entry from
// a -32042 URLElicitationRequiredError's data payload.
type URLElicitationRequiredInfo struct {
	Mode          string `json:"mode"`
	ElicitationID string `json:"elicitationId"`
	URL           string `json:"url"`
	Message       string `json:"message"`
}

// URLElicitationRequiredData is the data payload of -32042 errors.
type URLElicitationRequiredData struct {
	Elicitations []URLElicitationRequiredInfo `json:"elicitations"`
}

// ElicitationParams is the parameters for an elicitation/create request.
type ElicitationParams struct {
	// Mode selects the elicitation mode: "form" (default) or "url" per
	// 2025-11-25. Servers MAY omit it for form mode (backwards compat).
	Mode    string `json:"mode,omitempty"`
	Message string `json:"message"` // prompt text shown to the user
	// Schema is the form-mode schema describing desired fields.
	Schema ElicitationSchema `json:"requestedSchema"`
	// URL is the URL mode target for the out-of-band interaction.
	URL string `json:"url,omitempty"`
	// ElicitationID uniquely identifies a URL mode elicitation; the
	// notifications/elicitation/complete notification references it.
	ElicitationID string `json:"elicitationId,omitempty"`
}

// EffectiveMode normalizes an omitted mode to form (spec: clients MUST treat
// requests without a mode field as form mode).
func (p ElicitationParams) EffectiveMode() string {
	if p.Mode == ElicitationModeURL {
		return ElicitationModeURL
	}
	return ElicitationModeForm
}

// ValidateElicitationURL checks a URL mode elicitation target (MCP
// 2025-11-25). The client only displays this URL (it never auto-opens it or
// reads data back), but we still reject non-http(s) schemes and plain http
// for non-local hosts, matching the spec's "SHOULD use HTTPS for
// non-development environments".
func ValidateElicitationURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("url is not parseable: %w", err)
	}
	if u.Host == "" {
		return fmt.Errorf("url is missing a host")
	}
	switch u.Scheme {
	case "https":
		return nil
	case "http":
		host := u.Hostname()
		if host == "localhost" || host == "127.0.0.1" || host == "::1" {
			return nil // local development servers
		}
		return fmt.Errorf("url must use https for non-local host %q", host)
	default:
		return fmt.Errorf("url scheme must be https, got %q", u.Scheme)
	}
}

// validateElicitationParams validates an elicitation/create request per its
// mode and returns an error suitable for a -32602 JSON-RPC response.
func validateElicitationParams(params ElicitationParams) error {
	if params.EffectiveMode() == ElicitationModeURL {
		if params.ElicitationID == "" {
			return fmt.Errorf("url elicitation requires elicitationId")
		}
		if params.URL == "" {
			return fmt.Errorf("url elicitation requires url")
		}
		return ValidateElicitationURL(params.URL)
	}
	return ValidateElicitationSchema(params.Schema)
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
