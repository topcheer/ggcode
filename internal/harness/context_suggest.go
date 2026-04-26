package harness

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/topcheer/ggcode/internal/provider"
)

type ContextSuggestionRequest struct {
	RootDir       string
	ProjectName   string
	Goal          string
	ExtraElements []string
	HintContexts  []string
}

type suggestedContextPayload struct {
	Contexts []ContextConfig `json:"contexts"`
}

// SuggestContexts asks the configured model for bounded-context suggestions.
func SuggestContexts(ctx context.Context, prov provider.Provider, req ContextSuggestionRequest) ([]ContextConfig, error) {
	if prov == nil {
		return nil, fmt.Errorf("missing provider")
	}
	root := strings.TrimSpace(req.RootDir)
	if root == "" {
		return nil, fmt.Errorf("missing root directory")
	}
	prompt, err := buildContextSuggestionPrompt(req)
	if err != nil {
		return nil, err
	}
	resp, err := prov.Chat(ctx, []provider.Message{{
		Role:    "user",
		Content: []provider.ContentBlock{provider.TextBlock(prompt)},
	}}, nil)
	if err != nil {
		return nil, err
	}
	raw := strings.TrimSpace(firstSuggestionText(resp))
	if raw == "" {
		return nil, fmt.Errorf("empty context suggestion response")
	}
	contexts, err := parseSuggestedContexts(raw)
	if err != nil {
		return nil, err
	}
	contexts = EnsureOperationalContexts(contexts, req.Goal, req.ExtraElements, req.HintContexts)
	if len(contexts) == 0 {
		return nil, fmt.Errorf("no contexts suggested")
	}
	return contexts, nil
}

func buildContextSuggestionPrompt(req ContextSuggestionRequest) (string, error) {
	projectSummary, err := summarizeProjectForContexts(req.RootDir)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString("You are helping define bounded contexts for a software project harness.\n")
	b.WriteString("Return JSON only. No markdown fences, no commentary.\n")
	b.WriteString("Schema:\n")
	b.WriteString(`{"contexts":[{"name":"string","path":"string","description":"string","require_agent":true}]}`)
	b.WriteString("\n\nRules:\n")
	b.WriteString("- Suggest 2 to 8 contexts.\n")
	b.WriteString("- Prefer architecture / product boundaries, not language-specific folder conventions.\n")
	b.WriteString("- For existing projects, use real repository structure and current modules.\n")
	b.WriteString("- For empty or early-stage projects, it is valid for path to be empty when the module does not exist yet.\n")
	b.WriteString("- Use require_agent=true only when path is non-empty and represents a concrete subtree.\n")
	b.WriteString("- Keep names short, stable, and human-readable.\n")
	b.WriteString("- Descriptions should explain responsibility in one sentence.\n")
	b.WriteString("\nProject:\n")
	b.WriteString("- name: " + strings.TrimSpace(req.ProjectName) + "\n")
	if goal := strings.TrimSpace(req.Goal); goal != "" {
		b.WriteString("- goal: " + goal + "\n")
	}
	if len(req.ExtraElements) > 0 {
		b.WriteString("- user signals:\n")
		for _, item := range req.ExtraElements {
			if trimmed := strings.TrimSpace(item); trimmed != "" {
				b.WriteString("  - " + trimmed + "\n")
			}
		}
	}
	if len(req.HintContexts) > 0 {
		b.WriteString("- desired context directions:\n")
		for _, item := range req.HintContexts {
			if trimmed := strings.TrimSpace(item); trimmed != "" {
				b.WriteString("  - " + trimmed + "\n")
			}
		}
	}
	b.WriteString("\nRepository summary:\n")
	b.WriteString(projectSummary)
	return b.String(), nil
}

func summarizeProjectForContexts(root string) (string, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return "", fmt.Errorf("read project root: %w", err)
	}
	var topLevel []string
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, ".git") {
			continue
		}
		if entry.IsDir() {
			topLevel = append(topLevel, name+"/")
		} else {
			topLevel = append(topLevel, name)
		}
		if len(topLevel) >= 24 {
			break
		}
	}
	var important []string
	for _, rel := range []string{"README.md", "readme.md", "AGENTS.md", "docs/README.md"} {
		body, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			continue
		}
		snippet := compactSuggestionText(string(body))
		if snippet == "" {
			continue
		}
		important = append(important, fmt.Sprintf("- %s: %s", rel, truncateSuggestionText(snippet, 600)))
		if len(important) >= 2 {
			break
		}
	}
	var samplePaths []string
	var stopWalk = fmt.Errorf("stop walk")
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if path == root {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return nil
		}
		if strings.HasPrefix(rel, ".git") {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		depth := strings.Count(rel, string(filepath.Separator))
		if d.IsDir() && depth > 2 {
			return filepath.SkipDir
		}
		if d.IsDir() {
			return nil
		}
		samplePaths = append(samplePaths, rel)
		if len(samplePaths) >= 20 {
			return stopWalk
		}
		return nil
	})
	var b strings.Builder
	if len(topLevel) == 0 {
		b.WriteString("- top level: empty\n")
	} else {
		b.WriteString("- top level: " + strings.Join(topLevel, ", ") + "\n")
	}
	if len(samplePaths) > 0 {
		b.WriteString("- sample files: " + strings.Join(samplePaths, ", ") + "\n")
	}
	if len(important) > 0 {
		b.WriteString(strings.Join(important, "\n"))
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n"), nil
}

func firstSuggestionText(resp *provider.ChatResponse) string {
	if resp == nil {
		return ""
	}
	for _, block := range resp.Message.Content {
		if strings.TrimSpace(block.Text) != "" {
			return block.Text
		}
	}
	return ""
}

func parseSuggestedContexts(raw string) ([]ContextConfig, error) {
	payload := strings.TrimSpace(raw)
	if payload == "" {
		return nil, fmt.Errorf("empty context payload")
	}
	if strings.HasPrefix(payload, "```") {
		payload = strings.TrimPrefix(payload, "```json")
		payload = strings.TrimPrefix(payload, "```")
		payload = strings.TrimSuffix(payload, "```")
		payload = strings.TrimSpace(payload)
	}
	start := strings.IndexAny(payload, "[{")
	if start > 0 {
		payload = payload[start:]
	}
	if strings.HasPrefix(payload, "[") {
		var contexts []ContextConfig
		if err := json.Unmarshal([]byte(payload), &contexts); err != nil {
			return nil, fmt.Errorf("parse suggested contexts array: %w", err)
		}
		return contexts, nil
	}
	var wrapped suggestedContextPayload
	if err := json.Unmarshal([]byte(payload), &wrapped); err != nil {
		return nil, fmt.Errorf("parse suggested contexts object: %w", err)
	}
	return wrapped.Contexts, nil
}

func compactSuggestionText(raw string) string {
	lines := strings.Split(raw, "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		out = append(out, trimmed)
	}
	return strings.Join(out, " ")
}

func truncateSuggestionText(raw string, limit int) string {
	if len(raw) <= limit {
		return raw
	}
	if limit < 4 {
		return raw[:limit]
	}
	return raw[:limit-3] + "..."
}
