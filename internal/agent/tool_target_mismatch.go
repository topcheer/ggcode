package agent

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/topcheer/ggcode/internal/provider"
)

const (
	toolTargetMaxWarnings = 2

	toolTargetMinConfidence = 2
)

var toolTargetIntentRe = regexp.MustCompile(
	`(?i)(?:i(?:'ll|\s+will|\s+need\s+to)?\s+(?:read|edit|check|look\s+at|open|modify|update|examine|inspect|review|search|find|grep|scan)\s+(?:for\s+)?|let\s+me\s+(?:read|edit|check|look\s+at|open|modify|update|examine|inspect|review|search\s+for|find|grep|scan)\s+(?:for\s+)?)([^\s.,;:!?\n]+(?:/[^\s.,;:!?\n]+)*\.?[^\s.,;:!?\n]*)`,
)

var toolTargetSearchIntentRe = regexp.MustCompile(
	"(?i)(?:i(?:'ll|\\s+will|\\s+need\\s+to)?\\s+(?:search|grep|look\\s+for|find)\\s+for\\s+|let\\s+me\\s+(?:search|grep|look\\s+for|find)\\s+for\\s+)(?:[\"'`])([^\"'`]+)(?:[\"'`])",
)

type statedTarget struct {
	intentType string
	value      string
}

type toolTarget struct {
	toolName string
	targets  []string
}

type toolTargetState struct {
	warnings int
	curIter  int
}

func newToolTargetState() *toolTargetState {
	return &toolTargetState{}
}

func extractStatedTargets(text string) []statedTarget {
	if len(text) == 0 {
		return nil
	}

	var targets []statedTarget
	seen := make(map[string]bool)

	matches := toolTargetIntentRe.FindAllStringSubmatch(text, -1)
	for _, m := range matches {
		if len(m) < 2 {
			continue
		}
		raw := strings.TrimSpace(m[1])
		if raw == "" || len(raw) < 3 {
			continue
		}

		lower := strings.ToLower(raw)
		if isFalsePositiveTarget(lower) {
			continue
		}

		if classifyIntent(m[0]) != "search" && !targetLooksLikeFile(raw) {
			continue
		}

		raw = strings.TrimRight(raw, ".,;:!?")
		if raw == "" {
			continue
		}
		key := lower
		if seen[key] {
			continue
		}
		seen[key] = true

		intentType := classifyIntent(m[0])
		targets = append(targets, statedTarget{intentType: intentType, value: raw})
	}

	searchMatches := toolTargetSearchIntentRe.FindAllStringSubmatch(text, -1)
	for _, m := range searchMatches {
		if len(m) < 2 {
			continue
		}
		raw := strings.TrimSpace(m[1])
		if raw == "" || len(raw) < 2 {
			continue
		}
		lower := strings.ToLower(raw)
		key := "search:" + lower
		if seen[key] {
			continue
		}
		seen[key] = true
		targets = append(targets, statedTarget{intentType: "search", value: raw})
	}

	return targets
}

func isFalsePositiveTarget(s string) bool {
	falsePositives := map[string]bool{
		"the": true, "a": true, "an": true, "this": true, "that": true,
		"it": true, "them": true, "those": true, "these": true,
		"all": true, "any": true, "some": true, "each": true,
	}
	return falsePositives[s]
}

func targetLooksLikeFile(s string) bool {
	if strings.ContainsAny(s, "/\\") {
		return true
	}
	dot := strings.LastIndex(s, ".")
	if dot <= 0 || dot == len(s)-1 {
		return false
	}
	ext := s[dot+1:]
	if len(ext) > 6 {
		return false
	}
	for i := 0; i < len(ext); i++ {
		b := ext[i]
		if !(b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9') {
			return false
		}
	}
	return true
}

func classifyIntent(phrase string) string {
	lower := strings.ToLower(phrase)
	if strings.Contains(lower, "search") || strings.Contains(lower, "grep") ||
		strings.Contains(lower, "find") || strings.Contains(lower, "look for") {
		return "search"
	}
	return "file"
}

func extractActualToolTargets(toolCalls []provider.ToolCallDelta) []toolTarget {
	var targets []toolTarget

	for _, tc := range toolCalls {
		if len(tc.Arguments) == 0 {
			continue
		}

		var args map[string]interface{}
		if err := json.Unmarshal(tc.Arguments, &args); err != nil {
			continue
		}

		tt := toolTarget{toolName: tc.Name}

		for _, key := range []string{"path", "file_path", "directory", "glob_pattern", "pattern"} {
			if v, ok := args[key]; ok {
				if s, ok := v.(string); ok && s != "" {
					tt.targets = append(tt.targets, s)
				}
			}
		}

		if v, ok := args["old_text"]; ok {
			if s, ok := v.(string); ok && len(s) > 10 {
				tt.targets = append(tt.targets, s)
			}
		}

		if len(tt.targets) > 0 {
			targets = append(targets, tt)
		}
	}

	return targets
}

func (s *toolTargetState) checkMismatch(statedTargets []statedTarget, actualTargets []toolTarget) string {
	if len(statedTargets) == 0 || len(actualTargets) == 0 {
		return ""
	}

	actualSet := make(map[string]bool)
	for _, at := range actualTargets {
		for _, t := range at.targets {
			normal := normalizePathTarget(t)
			if normal != "" {
				actualSet[normal] = true
			}
		}
	}

	var mismatches []statedTarget
	for _, st := range statedTargets {
		normalStated := normalizePathTarget(st.value)
		if normalStated == "" {
			continue
		}

		found := false
		for actual := range actualSet {
			if targetsMatch(normalStated, actual) {
				found = true
				break
			}
		}
		if !found {
			mismatches = append(mismatches, st)
		}
	}

	if len(mismatches) < toolTargetMinConfidence {
		return ""
	}

	var parts []string
	for _, m := range mismatches {
		if len(parts) >= 3 {
			break
		}
		excerpt := m.value
		if len(excerpt) > 60 {
			excerpt = excerpt[:60] + "..."
		}
		parts = append(parts, excerpt)
	}

	return fmt.Sprintf(
		"[intent-action mismatch] Your stated target(s) (%s) do not match the "+
			"actual tool call target(s). Verify you are operating on the correct "+
			"file/path/search term before proceeding. Intent-action misalignment is "+
			"a common source of agent errors.",
		strings.Join(parts, ", "),
	)
}

func normalizePathTarget(s string) string {
	s = strings.TrimSpace(s)
	s = strings.Trim(s, "\"'`")
	s = strings.ToLower(s)
	return s
}

func targetsMatch(stated, actual string) bool {
	if stated == actual {
		return true
	}

	statedBase := stated
	if idx := strings.LastIndex(stated, "/"); idx >= 0 {
		statedBase = stated[idx+1:]
	}
	actualBase := actual
	if idx := strings.LastIndex(actual, "/"); idx >= 0 {
		actualBase = actual[idx+1:]
	}
	if statedBase != "" && statedBase == actualBase {
		return true
	}

	if len(stated) >= 4 && boundaryContains(actual, stated) {
		return true
	}
	if len(actual) >= 4 && boundaryContains(stated, actual) {
		return true
	}
	return false
}

func boundaryContains(haystack, needle string) bool {
	searchStart := 0
	for {
		idx := strings.Index(haystack[searchStart:], needle)
		if idx < 0 {
			return false
		}
		idx += searchStart
		beforeOK := idx == 0 || isPathDelimiter(haystack[idx-1])
		afterIdx := idx + len(needle)
		afterOK := afterIdx >= len(haystack) || isPathDelimiter(haystack[afterIdx])
		if beforeOK && afterOK {
			return true
		}
		searchStart = idx + 1
	}
}

func isPathDelimiter(ch byte) bool {
	return ch == '/' || ch == '_' || ch == '.' || ch == '-'
}

func (a *Agent) maybeWarnToolTargetMismatch(assistantText string, toolCalls []provider.ToolCallDelta) string {
	if a.toolTargetMismatch == nil {
		return ""
	}
	if a.toolTargetMismatch.warnings >= toolTargetMaxWarnings {
		return ""
	}

	statedTargets := extractStatedTargets(assistantText)
	if len(statedTargets) == 0 {
		return ""
	}

	actualTargets := extractActualToolTargets(toolCalls)
	if len(actualTargets) == 0 {
		return ""
	}

	hint := a.toolTargetMismatch.checkMismatch(statedTargets, actualTargets)
	if hint != "" {
		a.toolTargetMismatch.warnings++
	}
	return hint
}
