package tool

import (
	"encoding/json"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
)

// Enum Value Correction - Fault-Tolerant Tool Argument Handling
//
// Research basis: Modern AI agents (Claude Code, Cursor, Windsurf) all face
// the problem of weak models (local models, smaller open-weight models, models
// behind third-party endpoints) producing near-miss enum values in tool calls.
// Common patterns:
//
//   - Case mismatch: "JSON" instead of "json", "TRUE" instead of "true"
//   - Typo: "concise" instead of "compact", "overwite" instead of "overwrite"
//   - Abbreviation: "dir" instead of "directory"
//
// Without correction, each invalid enum value wastes a full agent loop
// iteration: the model sends the value, gets an error, retries. With many
// enum parameters across many tools, this compounds into significant waste.
//
// Two-layer approach:
//
//  1. Auto-correction (CoerceEnumValues): for unambiguous corrections
//     (case-insensitive match, or a single closest match within edit distance
//     2), silently fix the value. This is safe because the correction is
//     deterministic and unambiguous.
//
//  2. Suggestion (suggestClosestEnum): for ambiguous cases (multiple close
//     matches), include "did you mean 'X'?" in the validation error message
//     to guide the model to the right value without guessing.

// CoerceEnumValues auto-corrects enum parameter values in tool arguments.
// It handles:
//   - Case-insensitive exact match: "JSON" → "json" (always auto-corrected)
//   - Single closest match within edit distance 2: "concise" → "compact"
//     (auto-corrected only when exactly one candidate is close enough)
//
// Returns the (possibly corrected) arguments JSON. No-op when the schema
// can't be parsed or no enum fields exist.
func CoerceEnumValues(schema json.RawMessage, args json.RawMessage) json.RawMessage {
	if len(schema) == 0 || len(args) == 0 {
		return args
	}

	var props map[string]json.RawMessage
	if err := json.Unmarshal(schema, &props); err != nil {
		return args
	}
	propertiesRaw, ok := props["properties"]
	if !ok {
		return args
	}
	var properties map[string]json.RawMessage
	if err := json.Unmarshal(propertiesRaw, &properties); err != nil {
		return args
	}

	var argMap map[string]json.RawMessage
	if err := json.Unmarshal(args, &argMap); err != nil {
		return args
	}

	changed := false
	for fieldName, fieldSchema := range properties {
		val, exists := argMap[fieldName]
		if !exists || isEmptyValue(val) {
			continue
		}

		var spec struct {
			Enum []json.RawMessage `json:"enum"`
			Type string            `json:"type"`
		}
		if err := json.Unmarshal(fieldSchema, &spec); err != nil {
			continue
		}
		if len(spec.Enum) == 0 || spec.Type != "string" {
			continue // only correct string enums
		}

		// Extract the provided string value.
		provided := strings.TrimSpace(strings.Trim(string(val), `"`))
		if provided == "" {
			continue
		}

		// Check if it already matches exactly (no correction needed).
		if enumContains(spec.Enum, val) {
			continue
		}

		corrected := findBestEnumMatch(provided, spec.Enum)
		if corrected == "" {
			continue // no good match
		}

		debug.Log("agent", "enum correction: %s %q → %q", fieldName, provided, corrected)
		argMap[fieldName] = json.RawMessage(`"` + corrected + `"`)
		changed = true
	}

	if !changed {
		return args
	}

	out, err := json.Marshal(argMap)
	if err != nil {
		return args
	}
	return out
}

// findBestEnumMatch finds the best correction for a provided enum value.
// Returns the corrected value, or "" if no good correction exists.
//
// Correction priority:
//  1. Case-insensitive exact match (e.g., "JSON" → "json")
//  2. Single closest match within Levenshtein distance 2 (e.g., "overwite" → "overwrite")
//
// If multiple candidates are equally close, no correction is made (ambiguous).
func findBestEnumMatch(provided string, enum []json.RawMessage) string {
	provided = strings.ToLower(provided)

	// Layer 1: case-insensitive exact match.
	for _, e := range enum {
		candidate := strings.TrimSpace(strings.Trim(string(e), `"`))
		if strings.ToLower(candidate) == provided {
			return candidate
		}
	}

	// Layer 2: closest match within edit distance 2.
	// Collect all candidates within distance 2.
	var closeCandidates []string
	minDist := 3 // only accept distance <= 2
	for _, e := range enum {
		candidate := strings.TrimSpace(strings.Trim(string(e), `"`))
		dist := levenshtein(provided, strings.ToLower(candidate))
		if dist <= 2 && dist < minDist {
			minDist = dist
			closeCandidates = []string{candidate}
		} else if dist == minDist && dist <= 2 {
			closeCandidates = append(closeCandidates, candidate)
		}
	}

	// Only auto-correct when there's exactly one closest match.
	if len(closeCandidates) == 1 {
		return closeCandidates[0]
	}

	return ""
}

// suggestClosestEnum returns the closest valid enum value for a provided
// invalid value, or "" if no close match exists. Used to enhance error
// messages with "did you mean?" hints. Unlike findBestEnumMatch, this
// returns a suggestion even when multiple candidates are close (picks
// the single best).
func suggestClosestEnum(provided string, enum []json.RawMessage) string {
	providedLower := strings.ToLower(provided)
	bestDist := 3 // max distance to suggest
	bestMatch := ""

	for _, e := range enum {
		candidate := strings.TrimSpace(strings.Trim(string(e), `"`))
		dist := levenshtein(providedLower, strings.ToLower(candidate))
		if dist < bestDist {
			bestDist = dist
			bestMatch = candidate
		}
	}

	// Only suggest if within reasonable distance.
	if bestDist <= 2 && bestMatch != "" {
		return bestMatch
	}
	return ""
}
