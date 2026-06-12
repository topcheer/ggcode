package harness

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
)

type suggestionProvider struct {
	text string
}

func (s suggestionProvider) Name() string { return "test" }

func (s suggestionProvider) Chat(ctx context.Context, messages []provider.Message, tools []provider.ToolDefinition) (*provider.ChatResponse, error) {
	return &provider.ChatResponse{
		Message: provider.Message{
			Role:    "assistant",
			Content: []provider.ContentBlock{provider.TextBlock(s.text)},
		},
	}, nil
}

func (s suggestionProvider) ChatStream(ctx context.Context, messages []provider.Message, tools []provider.ToolDefinition) (<-chan provider.StreamEvent, error) {
	return nil, fmt.Errorf("not implemented")
}

func (s suggestionProvider) CountTokens(ctx context.Context, messages []provider.Message) (int, error) {
	return 0, nil
}

func TestNormalizeContextsAllowsNameOnlyContexts(t *testing.T) {
	contexts := NormalizeContexts([]ContextConfig{
		{Name: "Checkout", Description: "Owns checkout flow", RequireAgent: true},
		{Name: "checkout", Description: "duplicate"},
	})
	if len(contexts) != 1 {
		t.Fatalf("expected one context, got %#v", contexts)
	}
	if contexts[0].Path != "" {
		t.Fatalf("expected name-only context path to stay empty, got %#v", contexts[0])
	}
	if contexts[0].RequireAgent {
		t.Fatalf("expected name-only context to disable require_agent, got %#v", contexts[0])
	}
}

func TestSuggestContextsParsesStructuredResponse(t *testing.T) {
	root := t.TempDir()
	resp := `{"contexts":[{"name":"checkout","path":"services/checkout","description":"Checkout lifecycle","require_agent":true},{"name":"payments","path":"","description":"Future payments domain","require_agent":true}]}`
	contexts, err := SuggestContexts(context.Background(), suggestionProvider{text: resp}, ContextSuggestionRequest{
		RootDir:     root,
		ProjectName: "shop",
		Goal:        "Build commerce flows",
	})
	if err != nil {
		t.Fatalf("SuggestContexts() error = %v", err)
	}
	if len(contexts) != 2 {
		t.Fatalf("expected two contexts, got %#v", contexts)
	}
	if contexts[0].Path == "" && contexts[1].Path == "" {
		t.Fatalf("expected at least one concrete path in %#v", contexts)
	}
	for _, contextCfg := range contexts {
		if strings.EqualFold(contextCfg.Name, "payments") && contextCfg.RequireAgent {
			t.Fatalf("expected empty-path context to disable require_agent, got %#v", contextCfg)
		}
	}
}

func TestBuildContextSuggestionPromptIncludesHintContexts(t *testing.T) {
	root := t.TempDir()
	prompt, err := buildContextSuggestionPrompt(ContextSuggestionRequest{
		RootDir:      root,
		ProjectName:  "shop",
		Goal:         "Ship the platform",
		HintContexts: []string{"qa-e2e", "release"},
	})
	if err != nil {
		t.Fatalf("buildContextSuggestionPrompt() error = %v", err)
	}
	if !strings.Contains(prompt, "desired context directions") || !strings.Contains(prompt, "qa-e2e") || !strings.Contains(prompt, "release") {
		t.Fatalf("expected prompt to include hint contexts, got %q", prompt)
	}
}

func TestCheckProjectAllowsNameOnlyContext(t *testing.T) {
	root := t.TempDir()
	result, err := Init(root, InitOptions{
		Goal: "Design a new platform",
		Contexts: []ContextConfig{
			{Name: "platform-core", Description: "Core domain before folders exist"},
		},
	})
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	report, err := CheckProject(context.Background(), result.Project, result.Config, CheckOptions{RunCommands: false})
	if err != nil {
		t.Fatalf("CheckProject() error = %v", err)
	}
	if !report.Passed {
		t.Fatalf("expected check report to pass for name-only context, got %+v", report.Issues)
	}
}
