package harness

import "strings"

// SplitCommaInput trims and splits a comma-separated user input string.
func SplitCommaInput(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

// ParseContextSpecs parses user-entered contexts like "payments" or "checkout=apps/checkout".
func ParseContextSpecs(raw string) []ContextConfig {
	var contexts []ContextConfig
	for _, item := range SplitCommaInput(raw) {
		trimmed := strings.TrimSpace(item)
		if trimmed == "" {
			continue
		}
		name := trimmed
		path := ""
		if before, after, ok := strings.Cut(trimmed, "="); ok {
			name = strings.TrimSpace(before)
			path = strings.TrimSpace(after)
		} else if before, after, ok := strings.Cut(trimmed, ":"); ok && !strings.Contains(after, "//") {
			name = strings.TrimSpace(before)
			path = strings.TrimSpace(after)
		}
		contexts = append(contexts, ContextConfig{
			Name:         name,
			Path:         path,
			Description:  "User-provided bounded context",
			RequireAgent: strings.TrimSpace(path) != "",
		})
	}
	return NormalizeContexts(contexts)
}
