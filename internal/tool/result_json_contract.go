package tool

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Leading-JSON content contract (#5748).
//
// Tool results frequently carry machine-consumable payloads: Result.Content
// whose first non-whitespace byte is '{' or '[' is json.Unmarshal-ed
// downstream - both by tests and by production tool-to-tool consumers -
// either in full (json.Unmarshal([]byte(strings.TrimSpace(content)), ...))
// or as the first line (strings.SplitN(content, "\n", 2)[0]). The contract:
//
//	Content that starts with '{' or '[' must be exactly ONE JSON value
//	followed by nothing but optional whitespace.
//
// Appending human-readable text after the JSON payload
// (string(out) + "\nNote: ...") silently breaks whole-content Unmarshal
// callers while first-line parsers keep passing - this exact shape broke 4
// tests during the #5745 review cycle before the note was reworked into a
// payload field (#5746 final form). Human-facing text must live in a JSON
// field (see jsonContentWithNote) or another documented envelope, never as
// a raw suffix. Plain-text Content (not starting with '{' or '[') and
// agent-side guidance appends are out of scope: the contract binds only
// what a Tool returns.

// LeadingJSONViolation returns "" when content satisfies the leading-JSON
// contract, or a human-readable violation description when it does not.
// Content that does not lead with '{' or '[' (plain text, empty) is always
// compliant; content that leads with '{' or '[' but fails to parse as JSON
// is reported as malformed (a payload that cannot be Unmarshaled at all
// violates the same consumers).
func LeadingJSONViolation(content string) string {
	trimmed := strings.TrimLeft(content, " \t\r\n")
	if trimmed == "" {
		return ""
	}
	if trimmed[0] != '{' && trimmed[0] != '[' {
		return ""
	}
	dec := json.NewDecoder(strings.NewReader(trimmed))
	var raw json.RawMessage
	if err := dec.Decode(&raw); err != nil {
		return fmt.Sprintf("content starts with %q but is not parseable JSON: %v", string(trimmed[0]), err)
	}
	rest := strings.TrimSpace(trimmed[dec.InputOffset():])
	if rest == "" {
		return ""
	}
	tail := rest
	if len(tail) > 60 {
		tail = tail[:60] + "..."
	}
	return fmt.Sprintf("non-JSON suffix appended after the leading JSON value: %q", tail)
}

// jsonContentWithNote marshals payload (which must encode to a JSON object)
// with the human-readable note attached as a "note" field - the documented
// envelope for human suffix text under the #5748 contract. An empty note
// adds no field. The result is self-checked with LeadingJSONViolation so a
// contract regression fails loudly at the build site.
func jsonContentWithNote(payload any, note string) (string, error) {
	out, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal payload: %w", err)
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(out, &obj); err != nil {
		return "", fmt.Errorf("payload does not encode to a JSON object: %w", err)
	}
	if _, exists := obj["note"]; exists && note != "" {
		return "", fmt.Errorf("payload already carries a note field; refusing to overwrite it")
	}
	if note != "" {
		noteBytes, err := json.Marshal(note)
		if err != nil {
			return "", fmt.Errorf("marshal note: %w", err)
		}
		obj["note"] = noteBytes
	}
	merged, err := json.Marshal(obj)
	if err != nil {
		return "", fmt.Errorf("marshal merged payload: %w", err)
	}
	content := string(merged)
	if viol := LeadingJSONViolation(content); viol != "" {
		return "", fmt.Errorf("JSON content self-check failed: %s", viol)
	}
	return content, nil
}
