package config

// Exported model-capability lookups for cross-package consumers (system
// prompt rendering, vision fallback model selection). These run the same
// inference chain used for endpoint resolution: exact capability-table match
// first, then name heuristics. Endpoint-level supports_vision overrides are
// NOT applied here - callers holding a resolved endpoint should prefer
// ResolvedEndpoint.SupportsVision.

// ModelSupportsVision reports whether a model is inferred to accept image
// input.
func ModelSupportsVision(model string) bool {
	return inferVisionSupport(model, "")
}

// ModelContextWindow returns the inferred context window for a model.
// When nothing is known it returns the 128k default, mirroring
// inferContextWindow's protocol fallback - treat it as a conservative
// estimate, not a hard fact.
func ModelContextWindow(model string) int {
	return inferContextWindow(model, "")
}

// annotateVisionFlags appends a "[vision]" marker to models inferred to
// support image input. Used when rendering the available-models list into
// the system prompt so the agent can pick vision-capable models for
// sub-agents without guessing from naming conventions.
func annotateVisionFlags(models []string) []string {
	out := make([]string, 0, len(models))
	for _, m := range models {
		if ModelSupportsVision(m) {
			out = append(out, m+" [vision]")
		} else {
			out = append(out, m)
		}
	}
	return out
}
