package harness

import (
	"path/filepath"
	"sort"
	"strings"
)

// NormalizeContexts trims, deduplicates, and canonicalizes context definitions.
func NormalizeContexts(contexts []ContextConfig) []ContextConfig {
	if len(contexts) == 0 {
		return nil
	}
	out := make([]ContextConfig, 0, len(contexts))
	seen := make(map[string]struct{}, len(contexts))
	for _, contextCfg := range contexts {
		name := strings.TrimSpace(contextCfg.Name)
		path := normalizeContextPath(contextCfg.Path)
		desc := strings.TrimSpace(contextCfg.Description)
		owner := strings.TrimSpace(contextCfg.Owner)
		if name == "" && path == "" {
			continue
		}
		if name == "" {
			name = strings.ReplaceAll(path, string(filepath.Separator), "-")
		}
		key := strings.ToLower(firstNonEmptyText(path, name))
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, ContextConfig{
			Name:         name,
			Path:         path,
			Description:  desc,
			Owner:        owner,
			RequireAgent: contextCfg.RequireAgent && path != "",
			Commands:     append([]CommandCheck(nil), contextCfg.Commands...),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		return firstNonEmptyText(out[i].Path, out[i].Name) < firstNonEmptyText(out[j].Path, out[j].Name)
	})
	return out
}

func normalizeContextPath(path string) string {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return ""
	}
	cleaned := filepath.Clean(trimmed)
	if cleaned == "." {
		return ""
	}
	return cleaned
}
