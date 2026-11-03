package agent

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
)

const (
	silentErrorThreshold  = 3
	silentErrorMaxTracked = 10
)

type silentErrorState struct {
	unresolvedErrors []unresolvedError

	silentAdvancementCount int

	fired bool

	lastErrorResource string
}

const silentErrorStaleIterations = 10

type unresolvedError struct {
	toolName     string
	resourceKey  string
	errorSnippet string
	iteration    int
}

func newSilentErrorState() *silentErrorState {
	return &silentErrorState{}
}

func (s *silentErrorState) reset() {
	s.unresolvedErrors = nil
	s.silentAdvancementCount = 0
	s.fired = false
	s.lastErrorResource = ""
}

func (s *silentErrorState) recordToolError(toolName, resourceKey, errorContent string, iteration int) {
	if len(s.unresolvedErrors) >= silentErrorMaxTracked {
		return
	}

	if isEnvironmentalCommandKey(resourceKey) {
		return
	}
	snippet := errorContent
	if len(snippet) > 120 {
		snippet = snippet[:120] + "..."
	}
	s.unresolvedErrors = append(s.unresolvedErrors, unresolvedError{
		toolName:     toolName,
		resourceKey:  resourceKey,
		errorSnippet: snippet,
		iteration:    iteration,
	})
	s.lastErrorResource = resourceKey
}

func (s *silentErrorState) recordToolAction(toolName, resourceKey string) string {
	if s.fired || len(s.unresolvedErrors) == 0 {
		return ""
	}

	// #4990 mode 3: age out stale entries. An error recorded many
	// iterations ago that the run has since moved past (with no matching
	// correction action) should not keep resurfacing in the nudge.
	{
		n := len(s.unresolvedErrors)
		newest := s.unresolvedErrors[n-1].iteration
		filtered := s.unresolvedErrors[:0]
		for _, ue := range s.unresolvedErrors {
			if newest-ue.iteration > silentErrorStaleIterations {
				continue
			}
			filtered = append(filtered, ue)
		}
		s.unresolvedErrors = filtered
		if len(s.unresolvedErrors) == 0 {
			return ""
		}
	}

	if resourceKey == "" {
		return ""
	}

	if s.actionAddressesError(resourceKey, toolName) {

		s.clearAddressedErrors(resourceKey)
		s.lastErrorResource = ""
		return ""
	}

	s.silentAdvancementCount++
	debug.Log("agent", "Silent error advancement #%d (tool=%s, resource=%s, unresolved=%d)",
		s.silentAdvancementCount, toolName, resourceKey, len(s.unresolvedErrors))

	if s.silentAdvancementCount >= silentErrorThreshold {
		return s.buildGuidance()
	}

	return ""
}

func (s *silentErrorState) actionAddressesError(resourceKey string, toolName string) bool {
	if resourceKey == "" {
		return false
	}
	resourceKey = normalizeResourceKey(resourceKey)
	for _, ue := range s.unresolvedErrors {
		ueKey := normalizeResourceKey(ue.resourceKey)
		if ueKey == "" {
			continue
		}

		if isCommandKey(ueKey) && isEditTool(toolName) {
			return true
		}

		if resourceKey == ueKey {
			return true
		}

		if strings.HasPrefix(resourceKey, ueKey) || strings.HasPrefix(ueKey, resourceKey) {
			return true
		}
	}
	return false
}

func (s *silentErrorState) clearAddressedErrors(resourceKey string) {
	resourceKey = normalizeResourceKey(resourceKey)
	filtered := s.unresolvedErrors[:0]
	for _, ue := range s.unresolvedErrors {
		ueKey := normalizeResourceKey(ue.resourceKey)

		if ueKey == "" {
			filtered = append(filtered, ue)
			continue
		}
		if ueKey == resourceKey ||
			pathPrefixMatch(resourceKey, ueKey) ||
			pathPrefixMatch(ueKey, resourceKey) {
			continue
		}
		filtered = append(filtered, ue)
	}
	s.unresolvedErrors = filtered
}

func pathPrefixMatch(longer, shorter string) bool {
	if !strings.HasPrefix(longer, shorter) {
		return false
	}
	if len(longer) == len(shorter) {
		return true
	}

	if strings.HasSuffix(shorter, "/") {
		return true
	}
	return len(longer) > len(shorter) && (longer[len(shorter)] == '/' || longer[len(shorter)] == filepath.Separator)
}

func isCommandKey(key string) bool {
	fields := strings.Fields(key)
	if len(fields) != 2 {
		return false
	}
	switch fields[0] {
	case "go", "npm", "cargo", "python", "python3", "yarn", "pnpm", "make", "pytest":
		return true
	}
	return false
}

func isEnvironmentalCommandKey(key string) bool {
	fields := strings.Fields(strings.ToLower(key))
	if len(fields) < 2 || fields[0] != "git" {
		return false
	}
	switch fields[1] {
	case "fetch", "ls-remote", "clone":
		return true
	}
	return false
}

func (s *silentErrorState) buildGuidance() string {
	s.fired = true

	var examples []string
	for i, ue := range s.unresolvedErrors {
		if i >= 3 {
			break
		}
		examples = append(examples, fmt.Sprintf("  %d. [%s] %s", i+1, ue.toolName, ue.errorSnippet))
	}

	return fmt.Sprintf("[silent-error] %d unaddressed tool error(s) moved past. Fix before continuing:\n%s",
		len(s.unresolvedErrors), strings.Join(examples, "\n"))
}

func extractErrorResourceKey(toolName string, args json.RawMessage) string {
	switch toolName {
	case "edit_file", "write_file", "multi_edit_file", "read_file":
		return extractFilePathFromArgs(toolName, args)
	case "run_command", "start_command":
		return normalizeResourceKey(extractCommandFromArgs(args))
	case "grep", "search_files":
		return extractJSONStringField(args, "pattern")
	case "glob":
		return extractJSONStringField(args, "pattern")
	default:
		return ""
	}
}

func normalizeResourceKey(key string) string {
	key = strings.TrimSpace(key)

	if strings.Contains(key, " ") {
		parts := strings.Fields(key)
		if len(parts) > 1 && (parts[0] == "go" || parts[0] == "npm" || parts[0] == "cargo" || strings.HasPrefix(parts[0], "python")) {

			if len(parts) >= 2 {
				return strings.ToLower(parts[0] + " " + parts[1])
			}
		}
	}
	return strings.ToLower(key)
}
