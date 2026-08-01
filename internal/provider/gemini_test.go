package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/config"
	"google.golang.org/genai"
)

func TestNewProvider_PassesGeminiBaseURL(t *testing.T) {
	var requestPath string
	var requestKey string
	var requestBody struct {
		Contents []struct {
			Role  string `json:"role"`
			Parts []struct {
				Text string `json:"text"`
			} `json:"parts"`
		} `json:"contents"`
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestPath = r.URL.Path
		requestKey = r.Header.Get("x-goog-api-key")
		if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"candidates":[{"content":{"role":"model","parts":[{"text":"pong"}]}}],
			"usageMetadata":{"promptTokenCount":1,"candidatesTokenCount":1}
		}`))
	}))
	defer server.Close()

	prov, err := NewProvider(&config.ResolvedEndpoint{
		Protocol:  "gemini",
		BaseURL:   server.URL,
		APIKey:    "dummy",
		Model:     "gemini-2.5-flash",
		MaxTokens: 128,
	})
	if err != nil {
		t.Fatalf("expected Gemini provider to be created, got error: %v", err)
	}

	resp, err := prov.Chat(context.Background(), []Message{
		{Role: "user", Content: []ContentBlock{TextBlock("Reply with exactly: pong")}},
	}, nil)
	if err != nil {
		t.Fatalf("expected Gemini chat to succeed against custom base URL, got error: %v", err)
	}
	if got, want := requestPath, "/v1beta/models/gemini-2.5-flash:generateContent"; got != want {
		t.Fatalf("expected Gemini request path %q, got %q", want, got)
	}
	if requestKey != "dummy" {
		t.Fatalf("expected API key to be forwarded via x-goog-api-key header, got %q", requestKey)
	}
	if len(requestBody.Contents) != 1 || len(requestBody.Contents[0].Parts) != 1 || !strings.Contains(requestBody.Contents[0].Parts[0].Text, "pong") {
		t.Fatalf("unexpected Gemini request body: %#v", requestBody)
	}
	if len(resp.Message.Content) != 1 || resp.Message.Content[0].Text != "pong" {
		t.Fatalf("unexpected Gemini response content: %#v", resp.Message.Content)
	}
}

func TestGeminiConvertResponse_IgnoresThoughtParts(t *testing.T) {
	p := &GeminiProvider{}
	blocks, usage := p.convertResponse(&genai.GenerateContentResponse{
		Candidates: []*genai.Candidate{
			{
				Content: &genai.Content{
					Parts: []*genai.Part{
						{Text: "p", Thought: true},
						{Text: "ong", Thought: true},
						{Text: "pong"},
					},
				},
			},
		},
		UsageMetadata: &genai.GenerateContentResponseUsageMetadata{
			PromptTokenCount:     3,
			CandidatesTokenCount: 1,
		},
	})
	if len(blocks) != 1 || blocks[0].Text != "pong" {
		t.Fatalf("expected only final visible text block, got %#v", blocks)
	}
	if usage.InputTokens != 3 || usage.OutputTokens != 1 {
		t.Fatalf("unexpected usage: %#v", usage)
	}
}

func TestGeminiConvertMessages_FillsMissingToolResultNameFromHistory(t *testing.T) {
	p := &GeminiProvider{}
	contents, _ := p.convertMessages([]Message{
		{
			Role: "assistant",
			Content: []ContentBlock{
				ToolUseBlock("tool-1", "search_files", json.RawMessage(`{"pattern":"qq"}`)),
			},
		},
		{
			Role: "user",
			Content: []ContentBlock{
				ToolResultBlock("tool-1", "No matches found.", false),
			},
		},
	})
	if len(contents) != 2 || len(contents[1].Parts) != 1 || contents[1].Parts[0].FunctionResponse == nil {
		t.Fatalf("expected one Gemini function response part, got %#v", contents)
	}
	if got := contents[1].Parts[0].FunctionResponse.Name; got != "search_files" {
		t.Fatalf("expected inferred function response name %q, got %q", "search_files", got)
	}
}

func TestGeminiConvertMessages_FallsBackWhenToolResultNameIsUnrecoverable(t *testing.T) {
	p := &GeminiProvider{}
	contents, _ := p.convertMessages([]Message{
		{
			Role: "user",
			Content: []ContentBlock{
				ToolResultBlock("tool-1", "No matches found.", false),
			},
		},
	})
	if len(contents) != 1 || len(contents[0].Parts) != 1 || contents[0].Parts[0].FunctionResponse == nil {
		t.Fatalf("expected one Gemini function response part, got %#v", contents)
	}
	if got := contents[0].Parts[0].FunctionResponse.Name; got != "_ggcode_unknown_tool" {
		t.Fatalf("expected fallback function response name %q, got %q", "_ggcode_unknown_tool", got)
	}
}

// TestGeminiProvider_ReasoningEffort verifies that SetReasoningEffort sets the
// thinking budget on the Gemini request config.
func TestGeminiProvider_ReasoningEffort(t *testing.T) {
	cases := []struct {
		effort    string
		maxTokens int
		wantZero  bool // true if budget should be 0 (too-small maxTokens)
	}{
		{"low", 8192, false},
		{"medium", 8192, false},
		{"high", 8192, false},
		{"low", 256, true}, // too small → budget 0
	}
	for _, tc := range cases {
		p := &GeminiProvider{maxTokens: tc.maxTokens}
		p.SetReasoningEffort(tc.effort)

		if p.ReasoningEffort() != tc.effort {
			t.Fatalf("expected effort %q, got %q", tc.effort, p.ReasoningEffort())
		}

		config := &genai.GenerateContentConfig{}
		p.applyReasoningEffort(config)
		if config.ThinkingConfig == nil {
			t.Fatalf("effort %q: expected ThinkingConfig to be set", tc.effort)
		}
		if config.ThinkingConfig.ThinkingBudget == nil {
			t.Fatalf("effort %q: expected ThinkingBudget pointer to be set", tc.effort)
		}
		budget := *config.ThinkingConfig.ThinkingBudget
		if tc.wantZero {
			if budget != 0 {
				t.Fatalf("effort %q maxTok=%d: expected budget 0, got %d", tc.effort, tc.maxTokens, budget)
			}
			continue
		}
		if budget < 512 {
			t.Fatalf("effort %q: expected budget >= 512, got %d", tc.effort, budget)
		}
		if int(budget) >= tc.maxTokens {
			t.Fatalf("effort %q: expected budget < maxTokens(%d), got %d", tc.effort, tc.maxTokens, budget)
		}
	}

	// Empty effort should not set ThinkingConfig
	p := &GeminiProvider{maxTokens: 8192}
	p.SetReasoningEffort("")
	config := &genai.GenerateContentConfig{}
	p.applyReasoningEffort(config)
	if config.ThinkingConfig != nil {
		t.Fatalf("empty effort: expected ThinkingConfig to be nil, got %+v", config.ThinkingConfig)
	}
}

// TestGeminiProvider_ToolChoice verifies that SetToolChoice maps correctly
// to Gemini's FunctionCallingConfig.Mode.
func TestGeminiProvider_ToolChoice(t *testing.T) {
	cases := []struct {
		choice string
		want   genai.FunctionCallingConfigMode
	}{
		{"auto", genai.FunctionCallingConfigModeAuto},
		{"required", genai.FunctionCallingConfigModeAny},
		{"none", genai.FunctionCallingConfigModeNone},
	}
	tools := []ToolDefinition{{Name: "test_tool", Description: "test", Parameters: json.RawMessage(`{}`)}}
	for _, tc := range cases {
		p := &GeminiProvider{}
		p.SetToolChoice(tc.choice)

		if p.ToolChoice() != tc.choice {
			t.Fatalf("expected choice %q, got %q", tc.choice, p.ToolChoice())
		}

		config := &genai.GenerateContentConfig{}
		p.applyToolChoice(config, tools)
		if config.ToolConfig == nil || config.ToolConfig.FunctionCallingConfig == nil {
			t.Fatalf("choice %q: expected ToolConfig.FunctionCallingConfig to be set", tc.choice)
		}
		if config.ToolConfig.FunctionCallingConfig.Mode != tc.want {
			t.Fatalf("choice %q: expected mode %q, got %q", tc.choice, tc.want, config.ToolConfig.FunctionCallingConfig.Mode)
		}
	}

	// Empty choice should not set ToolConfig
	p := &GeminiProvider{}
	config := &genai.GenerateContentConfig{}
	p.applyToolChoice(config, tools)
	if config.ToolConfig != nil {
		t.Fatalf("empty choice: expected ToolConfig to be nil")
	}

	// No tools should not set ToolConfig even with non-empty choice
	p = &GeminiProvider{}
	p.SetToolChoice("required")
	config = &genai.GenerateContentConfig{}
	p.applyToolChoice(config, nil)
	if config.ToolConfig != nil {
		t.Fatalf("no tools: expected ToolConfig to be nil")
	}
}

// TestGeminiCloneWithModel_PreservesSettings verifies that reasoning effort
// and tool choice survive CloneWithModel (used by named subagents).
func TestGeminiCloneWithModel_PreservesSettings(t *testing.T) {
	p := &GeminiProvider{
		model:           "gemini-2.5-flash",
		maxTokens:       8192,
		reasoningEffort: "high",
		toolChoice:      "required",
	}
	clone := p.CloneWithModel("gemini-2.5-pro").(*GeminiProvider)
	if clone.model != "gemini-2.5-pro" {
		t.Fatalf("expected cloned model gemini-2.5-pro, got %s", clone.model)
	}
	if clone.reasoningEffort != "high" {
		t.Fatalf("expected reasoningEffort preserved as high, got %s", clone.reasoningEffort)
	}
	if clone.toolChoice != "required" {
		t.Fatalf("expected toolChoice preserved as required, got %s", clone.toolChoice)
	}
}
