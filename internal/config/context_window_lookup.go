package config

import "strings"

// LookupSyncedContextWindow resolves a context window from the weekly
// models.dev-synced capability table (#5123) so the hand-maintained
// static table in internal/provider stops being the primary source and
// rotting at every new model generation. Resolution order: exact key
// (covers vendor-scoped rows like "z-ai/glm-5.3-flashx" and base rows
// like "glm-5.3"), then @region-suffix strip ("glm-5.2@eu"), then
// last-path-segment fallback for vendor-scoped input whose exact row is
// absent ("@cf/zai-org/glm-5.3-flashx" -> base row). Vendor-exact rows
// win over base rows because deployments disagree (e.g. z-ai/glm-5.3-
// flashx=1048576 vs base 1000000); base-name fallback only kicks in
// when the exact vendor deployment is not in the table. Returns 0 when
// unknown so callers can fall through to their own fallback chain.
func LookupSyncedContextWindow(model string) int {
	s := strings.ToLower(strings.TrimSpace(model))
	if s == "" {
		return 0
	}
	if cap, ok := lookupModelCapability(s); ok && cap.ContextWindow > 0 {
		return cap.ContextWindow
	}
	// region-suffixed base rows ("glm-5.2@eu"); never strips the leading
	// "@" of vendor-scoped keys ("@cf/...") because LastIndex finds the
	// LAST "@" and requires a non-empty prefix.
	if i := strings.LastIndex(s, "@"); i > 0 {
		if cap, ok := lookupModelCapability(s[:i]); ok && cap.ContextWindow > 0 {
			return cap.ContextWindow
		}
	}
	// vendor-scoped input with no exact row: base name is the last path
	// segment (the static table cannot resolve this shape at all).
	if i := strings.LastIndex(s, "/"); i >= 0 && i < len(s)-1 {
		if cap, ok := lookupModelCapability(s[i+1:]); ok && cap.ContextWindow > 0 {
			return cap.ContextWindow
		}
	}
	return 0
}
