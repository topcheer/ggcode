package tool

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/topcheer/ggcode/internal/commands"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/subagent"
)

type stubSkillLookup map[string]*commands.Command

func (s stubSkillLookup) Get(name string) (*commands.Command, bool) {
	cmd, ok := s[name]
	return cmd, ok
}

type fakeRunner struct {
	output string
}

type fakeProvider struct{}

func (fakeProvider) Name() string { return "fake" }

func (fakeProvider) Chat(ctx context.Context, messages []provider.Message, tools []provider.ToolDefinition) (*provider.ChatResponse, error) {
	return nil, nil
}

func (fakeProvider) ChatStream(ctx context.Context, messages []provider.Message, tools []provider.ToolDefinition) (<-chan provider.StreamEvent, error) {
	return nil, nil
}

func (fakeProvider) CountTokens(ctx context.Context, messages []provider.Message) (int, error) {
	return 0, nil
}

type fakeSkillMCPRuntime struct{}

func (fakeSkillMCPRuntime) SnapshotMCP() []MCPServerSnapshot { return nil }

func (fakeSkillMCPRuntime) GetPrompt(ctx context.Context, server, name string, args map[string]interface{}) (*MCPPromptResult, error) {
	return &MCPPromptResult{
		Description: "Prompt description",
		Messages: []MCPPromptMessage{{
			Role: "user",
			Text: "Prompt body",
		}},
	}, nil
}

func (fakeSkillMCPRuntime) ReadResource(ctx context.Context, server, uri string) (*MCPResourceResult, error) {
	return nil, nil
}

func (r fakeRunner) RunStream(ctx context.Context, prompt string, onEvent func(provider.StreamEvent)) error {
	onEvent(provider.StreamEvent{Type: provider.StreamEventText, Text: r.output})
	return nil
}

func TestSkillToolExecute(t *testing.T) {
	var callback SkillExecutionEvent
	tool := SkillTool{
		Skills: stubSkillLookup{
			"deploy": {
				Name:        "deploy",
				Template:    "Run from $DIR with $ARGS",
				Description: "Deploy the current build",
				Enabled:     true,
			},
		},
		OnSkillCompleted: func(event SkillExecutionEvent) {
			callback = event
		},
	}
	input, err := json.Marshal(map[string]string{
		"skill": "deploy",
		"args":  "prod",
	})
	if err != nil {
		t.Fatal(err)
	}

	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %+v", result)
	}
	if result.Content == "" {
		t.Fatal("expected expanded skill content")
	}
	if callback.Name != "deploy" || callback.Ref != "deploy" || callback.Mode != SkillExecutionModeInline || callback.Result.IsError {
		t.Fatalf("unexpected callback: %+v", callback)
	}
}

func TestSkillToolExecuteRejectsModelDisabledSkill(t *testing.T) {
	tool := SkillTool{
		Skills: stubSkillLookup{
			"deploy": {
				Name:                   "deploy",
				Template:               "Run deploy",
				DisableModelInvocation: true,
				Enabled:                true,
			},
		},
	}
	input := json.RawMessage(`{"skill":"deploy"}`)

	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}
	if !result.IsError {
		t.Fatalf("expected tool-level error, got %+v", result)
	}
}

func TestSkillToolExecuteForkedSkill(t *testing.T) {
	registry := NewRegistry()
	var callback SkillExecutionEvent
	tool := SkillTool{
		Skills: stubSkillLookup{
			"deploy": {
				Name:        "deploy",
				Template:    "Deploy with $ARGS",
				Context:     "fork",
				Description: "Deploy the app",
				Enabled:     true,
			},
		},
		Provider: fakeProvider{},
		Tools:    registry,
		AgentFactory: func(prov provider.Provider, tools interface{}, systemPrompt string, maxTurns int) subagent.AgentRunner {
			return fakeRunner{output: "forked result"}
		},
		OnSkillCompleted: func(event SkillExecutionEvent) {
			callback = event
		},
	}
	input := json.RawMessage(`{"skill":"deploy","args":"prod"}`)

	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %+v", result)
	}
	if result.Content != "forked result" {
		t.Fatalf("result.Content = %q", result.Content)
	}
	if callback.Name != "deploy" || callback.Ref != "deploy" || callback.Mode != SkillExecutionModeFork || callback.Result.IsError {
		t.Fatalf("unexpected callback: %+v", callback)
	}
}

func TestSkillToolUsesScopedProjectSkillRef(t *testing.T) {
	var usedRef string
	var callback SkillExecutionEvent
	tool := SkillTool{
		Skills: stubSkillLookup{
			"deploy": {
				Name:        "deploy",
				Template:    "Run deploy",
				Description: "Deploy the app",
				Enabled:     true,
				Source:      commands.SourceProject,
				LoadedFrom:  commands.LoadedFromSkills,
			},
		},
		OnSkillUsed: func(ref string) {
			usedRef = ref
		},
		OnSkillCompleted: func(event SkillExecutionEvent) {
			callback = event
		},
	}

	result, err := tool.Execute(context.Background(), json.RawMessage(`{"skill":"deploy"}`))
	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %+v", result)
	}
	if usedRef != "project:deploy" {
		t.Fatalf("expected scoped usage ref, got %q", usedRef)
	}
	if callback.Ref != "project:deploy" || callback.Scope != "project" {
		t.Fatalf("expected scoped callback, got %+v", callback)
	}
}

func TestSkillToolExecuteMCPPromptSkill(t *testing.T) {
	tool := SkillTool{
		Skills:  stubSkillLookup{},
		Runtime: fakeSkillMCPRuntime{},
	}

	result, err := tool.Execute(context.Background(), json.RawMessage(`{"skill":"docs:summarize"}`))
	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected tool error: %+v", result)
	}
	if result.Content == "" {
		t.Fatal("expected MCP prompt content")
	}
}
