package config

import (
	"strings"
	"testing"
)

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
