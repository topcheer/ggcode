package tool

import (
	"encoding/json"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
)

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
			continue
		}

		var provided string
		if err := json.Unmarshal(val, &provided); err != nil {
			continue
		}
		provided = strings.TrimSpace(provided)
		if provided == "" {
			continue
		}

		if enumContains(spec.Enum, val) {
			continue
		}

		corrected := findBestEnumMatch(provided, spec.Enum)
		if corrected == "" {
			continue
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

func findBestEnumMatch(provided string, enum []json.RawMessage) string {
	provided = strings.ToLower(provided)

	for _, e := range enum {
		candidate := strings.TrimSpace(strings.Trim(string(e), `"`))
		if strings.ToLower(candidate) == provided {
			return candidate
		}
	}

	var closeCandidates []string
	minDist := 3
	for _, e := range enum {
		candidate := strings.TrimSpace(strings.Trim(string(e), `"`))
		lower := strings.ToLower(candidate)
		dist := levenshtein(provided, lower)
		maxDist := max(1, len(lower)/4)
		if len(lower) <= 3 {

			if len(lower) == 0 || dist != 1 || len(provided) == 0 || lower[0] != provided[0] {
				continue
			}
		} else if dist > maxDist {
			continue
		}
		if dist < minDist {
			minDist = dist
			closeCandidates = []string{candidate}
		} else if dist == minDist && dist <= 2 {
			closeCandidates = append(closeCandidates, candidate)
		}
	}

	if len(closeCandidates) == 1 {
		return closeCandidates[0]
	}

	return ""
}

func suggestClosestEnum(provided string, enum []json.RawMessage) string {
	providedLower := strings.ToLower(provided)
	bestDist := 3
	bestMatch := ""

	for _, e := range enum {
		candidate := strings.TrimSpace(strings.Trim(string(e), `"`))
		dist := levenshtein(providedLower, strings.ToLower(candidate))
		if dist < bestDist {
			bestDist = dist
			bestMatch = candidate
		}
	}

	if bestDist <= 2 && bestMatch != "" {
		return bestMatch
	}
	return ""
}
