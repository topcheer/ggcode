package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"golang.org/x/term"

	appconfig "github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/harness"
	"github.com/topcheer/ggcode/internal/provider"
)

func chooseInitContexts(root, goal, cfgPath string, elements []string, hintContexts []string, in io.Reader, out io.Writer) ([]harness.ContextConfig, []string, error) {
	elements = normalizeInputList(elements)
	hintContexts = normalizeInputList(hintContexts)
	if isInteractiveTerminal(in, out) && elements == nil {
		line, err := promptLine(in, out, "Project elements (optional, comma-separated): ")
		if err != nil {
			return nil, nil, err
		}
		elements = normalizeInputList(harness.SplitCommaInput(line))
	}
	suggestions, err := suggestHarnessContexts(root, goal, cfgPath, elements, hintContexts)
	if err != nil {
		return nil, nil, err
	}
	if !isInteractiveTerminal(in, out) {
		return suggestions, elements, nil
	}
	selected, err := promptInitContexts(in, out, suggestions)
	if err != nil {
		return nil, nil, err
	}
	return selected, elements, nil
}

func chooseRunContext(root, goal, cfgPath string, cfg *harness.Config, in io.Reader, out io.Writer) (*harness.ContextConfig, bool, error) {
	if cfg == nil || !isInteractiveTerminal(in, out) {
		return nil, false, nil
	}
	available := harness.AugmentRunContexts(cfg.Contexts, goal)
	line, err := promptLine(in, out, buildRunContextPrompt(available, goal))
	if err != nil {
		return nil, false, err
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return nil, false, fmt.Errorf("context selection is required for interactive harness run")
	}
	if idx, ok := parseOneBasedIndex(line, len(available)); ok {
		match := available[idx]
		return &match, false, nil
	}
	custom := harness.ParseContextSpecs(line)
	if len(custom) == 0 {
		return nil, false, fmt.Errorf("invalid context input %q", line)
	}
	match := custom[0]
	if existing := resolveExistingContextInput(cfg, line, match); existing != nil {
		return existing, false, nil
	}
	persist, err := promptYesNo(in, out, "Persist this new context to .ggcode/harness.yaml? [y/N]: ")
	if err != nil {
		return nil, false, err
	}
	return &match, persist, nil
}

func resolveExistingContextInput(cfg *harness.Config, raw string, parsed harness.ContextConfig) *harness.ContextConfig {
	if cfg == nil {
		return nil
	}
	candidates := []string{strings.TrimSpace(raw), strings.TrimSpace(parsed.Name), strings.TrimSpace(parsed.Path)}
	seen := map[string]struct{}{}
	for _, candidate := range candidates {
		if candidate == "" {
			continue
		}
		if _, ok := seen[candidate]; ok {
			continue
		}
		seen[candidate] = struct{}{}
		match, err := harness.ResolveContext(cfg, candidate)
		if err == nil && match != nil {
			return match
		}
	}
	return nil
}

func suggestHarnessContexts(root, goal, cfgPath string, elements []string, hintContexts []string) ([]harness.ContextConfig, error) {
	fallback := harness.DetectContexts(root)
	prov, projectName, err := loadHarnessSuggestionProvider(cfgPath, root)
	if err != nil || prov == nil {
		fallback = harness.EnsureOperationalContexts(fallback, goal, elements, hintContexts)
		if len(fallback) > 0 {
			return fallback, nil
		}
		return nil, nil
	}
	suggested, err := harness.SuggestContexts(context.Background(), prov, harness.ContextSuggestionRequest{
		RootDir:       root,
		ProjectName:   projectName,
		Goal:          goal,
		ExtraElements: elements,
		HintContexts:  hintContexts,
	})
	if err != nil {
		fallback = harness.EnsureOperationalContexts(fallback, goal, elements, hintContexts)
		if len(fallback) > 0 {
			return fallback, nil
		}
		return nil, err
	}
	if len(suggested) == 0 {
		return fallback, nil
	}
	return suggested, nil
}

func loadHarnessSuggestionProvider(cfgPath, root string) (provider.Provider, string, error) {
	path := strings.TrimSpace(cfgPath)
	if path == "" {
		resolved, err := resolveConfigFilePath()
		if err != nil {
			return nil, "", err
		}
		path = resolved
	}
	cfg, err := appconfig.Load(path)
	if err != nil {
		return nil, filepathBase(root), err
	}
	resolved, err := cfg.ResolveActiveEndpoint()
	if err != nil {
		return nil, filepathBase(root), err
	}
	if strings.TrimSpace(resolved.APIKey) == "" {
		return nil, filepathBase(root), fmt.Errorf("missing API key for active endpoint")
	}
	prov, err := provider.NewProvider(resolved)
	if err != nil {
		return nil, filepathBase(root), err
	}
	return prov, filepathBase(root), nil
}

func promptInitContexts(in io.Reader, out io.Writer, suggestions []harness.ContextConfig) ([]harness.ContextConfig, error) {
	if len(suggestions) > 0 {
		fmt.Fprintln(out, "Suggested contexts:")
		for i, item := range suggestions {
			label := harnessFirstNonEmpty(item.Name, item.Path)
			desc := strings.TrimSpace(item.Description)
			if desc != "" {
				fmt.Fprintf(out, "  %d. %s — %s\n", i+1, label, desc)
			} else {
				fmt.Fprintf(out, "  %d. %s\n", i+1, label)
			}
		}
	}
	fmt.Fprintln(out, "Select contexts by number (comma-separated), press Enter to accept all suggestions, or type custom contexts such as \"payments, checkout=apps/checkout\".")
	line, err := promptLine(in, out, "Contexts: ")
	if err != nil {
		return nil, err
	}
	line = strings.TrimSpace(line)
	switch {
	case line == "":
		return suggestions, nil
	case looksLikeIndexSelection(line):
		selected := selectSuggestedContexts(suggestions, line)
		if len(selected) == 0 {
			return nil, fmt.Errorf("no valid contexts selected")
		}
		return selected, nil
	default:
		custom := harness.ParseContextSpecs(line)
		if len(custom) == 0 {
			return nil, fmt.Errorf("invalid context input %q", line)
		}
		return harness.NormalizeContexts(append(suggestions, custom...)), nil
	}
}

func buildRunContextPrompt(contexts []harness.ContextConfig, goal string) string {
	var b strings.Builder
	b.WriteString("Choose a context for this harness run")
	if strings.TrimSpace(goal) != "" {
		b.WriteString(" (")
		b.WriteString(goal)
		b.WriteString(")")
	}
	b.WriteString(":\n")
	for i, item := range contexts {
		label := harnessFirstNonEmpty(item.Name, item.Path)
		desc := strings.TrimSpace(item.Description)
		if desc != "" {
			fmt.Fprintf(&b, "  %d. %s — %s\n", i+1, label, desc)
		} else {
			fmt.Fprintf(&b, "  %d. %s\n", i+1, label)
		}
	}
	b.WriteString("Enter a number, or type a new context like \"payments\" or \"checkout=apps/checkout\".\n")
	return b.String()
}

func promptLine(in io.Reader, out io.Writer, prompt string) (string, error) {
	if _, err := io.WriteString(out, prompt); err != nil {
		return "", err
	}
	reader := bufio.NewReader(in)
	line, err := reader.ReadString('\n')
	if err != nil && err != io.EOF {
		return "", err
	}
	return strings.TrimSpace(line), nil
}

func promptYesNo(in io.Reader, out io.Writer, prompt string) (bool, error) {
	line, err := promptLine(in, out, prompt)
	if err != nil {
		return false, err
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true, nil
	default:
		return false, nil
	}
}

func selectSuggestedContexts(suggestions []harness.ContextConfig, raw string) []harness.ContextConfig {
	var selected []harness.ContextConfig
	seen := map[int]struct{}{}
	for _, item := range harness.SplitCommaInput(raw) {
		idx, ok := parseOneBasedIndex(item, len(suggestions))
		if !ok {
			continue
		}
		if _, exists := seen[idx]; exists {
			continue
		}
		seen[idx] = struct{}{}
		selected = append(selected, suggestions[idx])
	}
	return harness.NormalizeContexts(selected)
}

func parseOneBasedIndex(raw string, total int) (int, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, false
	}
	value := 0
	for _, r := range raw {
		if r < '0' || r > '9' {
			return 0, false
		}
		value = value*10 + int(r-'0')
	}
	if value < 1 || value > total {
		return 0, false
	}
	return value - 1, true
}

func looksLikeIndexSelection(raw string) bool {
	for _, item := range harness.SplitCommaInput(raw) {
		for _, r := range item {
			if r < '0' || r > '9' {
				return false
			}
		}
	}
	return strings.TrimSpace(raw) != ""
}

func normalizeInputList(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		key := strings.ToLower(trimmed)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, trimmed)
	}
	return out
}

func filepathBase(root string) string {
	base := strings.TrimSpace(root)
	if base == "" {
		return "project"
	}
	return strings.TrimSpace(filepath.Base(base))
}

func isInteractiveTerminal(in io.Reader, out io.Writer) bool {
	return fileDescriptorIsTerminal(in) && writerIsTerminal(out)
}

func fileDescriptorIsTerminal(v any) bool {
	fder, ok := v.(interface{ Fd() uintptr })
	if !ok {
		return false
	}
	return term.IsTerminal(int(fder.Fd()))
}
