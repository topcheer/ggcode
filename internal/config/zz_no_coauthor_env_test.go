package config

import (
	"strings"
	"testing"
)

// GGCODE_NO_COAUTHOR must strip the prompt-side Co-Authored-By directive,
// otherwise the model keeps hand-writing the trailer and the env opt-out
// for the automatic injection (internal/tool) is meaningless.
func TestBuildSystemPromptNoCoAuthorEnv(t *testing.T) {
	prompt := BuildSystemPrompt("", "/tmp/w", "en", nil, "", nil, nil)
	if !strings.Contains(prompt, "Co-Authored-By") {
		t.Fatal("baseline system prompt unexpectedly lacks the Co-Authored-By directive; test premise broken")
	}

	t.Setenv("GGCODE_NO_COAUTHOR", "1")
	optedOut := BuildSystemPrompt("", "/tmp/w", "en", nil, "", nil, nil)
	if strings.Contains(optedOut, "Co-Authored-By") {
		t.Error("system prompt still contains Co-Authored-By directive after GGCODE_NO_COAUTHOR=1")
	}
}
