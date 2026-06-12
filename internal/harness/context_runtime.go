package harness

import "strings"

const crossCuttingContextName = "cross-cutting"

func defaultCrossCuttingContext() ContextConfig {
	return ContextConfig{
		Name:        crossCuttingContextName,
		Description: "Global E2E, build, release, deployment, and other work that spans multiple product contexts.",
	}
}

func hasOperationalContext(contexts []ContextConfig) bool {
	for _, contextCfg := range contexts {
		name := strings.ToLower(strings.TrimSpace(contextCfg.Name))
		desc := strings.ToLower(strings.TrimSpace(contextCfg.Description))
		if name == crossCuttingContextName {
			return true
		}
		if strings.Contains(name, "qa") || strings.Contains(name, "release") || strings.Contains(name, "deploy") || strings.Contains(name, "ops") {
			return true
		}
		if strings.Contains(desc, "cross-cutting") || strings.Contains(desc, "release") || strings.Contains(desc, "deployment") || strings.Contains(desc, "e2e") {
			return true
		}
	}
	return false
}

func shouldSuggestOperationalContext(goal string, extraElements []string, hintContexts []string, existingCount int) bool {
	if existingCount >= 3 {
		return true
	}
	joined := strings.ToLower(strings.Join(append(append([]string{goal}, extraElements...), hintContexts...), " "))
	for _, keyword := range []string{"e2e", "end-to-end", "package", "packaging", "release", "deploy", "deployment", "qa", "ci", "cd", "ops", "platform", "observability"} {
		if strings.Contains(joined, keyword) {
			return true
		}
	}
	return false
}

func EnsureOperationalContexts(contexts []ContextConfig, goal string, extraElements []string, hintContexts []string) []ContextConfig {
	if len(contexts) == 0 || hasOperationalContext(contexts) {
		return NormalizeContexts(contexts)
	}
	if !shouldSuggestOperationalContext(goal, extraElements, hintContexts, len(contexts)) {
		return NormalizeContexts(contexts)
	}
	return NormalizeContexts(append(append([]ContextConfig(nil), contexts...), defaultCrossCuttingContext()))
}

func AugmentRunContexts(contexts []ContextConfig, goal string) []ContextConfig {
	return EnsureOperationalContexts(contexts, goal, nil, nil)
}
