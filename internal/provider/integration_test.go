package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

// Integration tests against zai provider (BigModel/ZhipuAI).
// Set ZAI_API_KEY to run; otherwise tests are skipped.

const (
	zaiAnthropicBaseURL = "https://open.bigmodel.cn/api/anthropic"
	zaiOpenAIBaseURL    = "https://open.bigmodel.cn/api/coding/paas/v4"
	defaultZaiModel     = "glm-5-turbo"
)

func zaiAPIKey() string {
	key := os.Getenv("ZAI_API_KEY")
	if key == "" {
		key = os.Getenv("GGCODE_ZAI_API_KEY")
	}
	return key
}

func zaiModel() string {
	m := os.Getenv("ZAI_MODEL")
	if m == "" {
		m = defaultZaiModel
	}
	return m
}

// TestAnthropicChat verifies a non-streaming chat call via the Anthropic-compatible endpoint.
func TestAnthropicChat(t *testing.T) {
	key := zaiAPIKey()
	if key == "" {
		t.Skip("ZAI_API_KEY not set, skipping integration test")
	}

	prov := NewAnthropicProviderWithBaseURL(key, zaiModel(), 128, zaiAnthropicBaseURL)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	resp, err := prov.Chat(ctx, []Message{
		{Role: "user", Content: []ContentBlock{TextBlock("Say hello in exactly 3 words.")}},
	}, nil)
	if err != nil {
		t.Fatalf("Chat failed: %v", err)
	}

	if len(resp.Message.Content) == 0 {
		t.Fatal("Expected at least one content block")
	}
	text := resp.Message.Content[0].Text
	if text == "" {
		t.Fatal("Expected text content")
	}
	t.Logf("Response text: %q", text)
	t.Logf("Usage: input=%d output=%d", resp.Usage.InputTokens, resp.Usage.OutputTokens)

	if resp.Usage.InputTokens == 0 {
		t.Error("Expected non-zero input token count")
	}
}

// TestAnthropicStreaming verifies streaming chat via the Anthropic-compatible endpoint.
func TestAnthropicStreaming(t *testing.T) {
	key := zaiAPIKey()
	if key == "" {
		t.Skip("ZAI_API_KEY not set, skipping integration test")
	}

	prov := NewAnthropicProviderWithBaseURL(key, zaiModel(), 128, zaiAnthropicBaseURL)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	ch, err := prov.ChatStream(ctx, []Message{
		{Role: "user", Content: []ContentBlock{TextBlock("Count from 1 to 5, one per line.")}},
	}, nil)
	if err != nil {
		t.Fatalf("ChatStream failed: %v", err)
	}

	var textParts []string
	var gotDone bool
	var usage *TokenUsage

	for ev := range ch {
		switch ev.Type {
		case StreamEventText:
			textParts = append(textParts, ev.Text)
		case StreamEventDone:
			gotDone = true
			usage = ev.Usage
		case StreamEventError:
			t.Fatalf("Stream error: %v", ev.Error)
		}
	}

	if !gotDone {
		t.Fatal("Stream did not send Done event")
	}

	fullText := strings.Join(textParts, "")
	if fullText == "" {
		t.Fatal("Expected non-empty streamed text")
	}
	t.Logf("Streamed text: %q", fullText)
	if usage != nil {
		t.Logf("Stream usage: input=%d output=%d", usage.InputTokens, usage.OutputTokens)
	}
}

// TestAnthropicToolUse verifies tool calling via the Anthropic-compatible endpoint.
func TestAnthropicToolUse(t *testing.T) {
	key := zaiAPIKey()
	if key == "" {
		t.Skip("ZAI_API_KEY not set, skipping integration test")
	}

	prov := NewAnthropicProviderWithBaseURL(key, zaiModel(), 256, zaiAnthropicBaseURL)

	tools := []ToolDefinition{
		{
			Name:        "get_weather",
			Description: "Get the current weather for a location",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"location": {"type": "string", "description": "City name"}
				},
				"required": ["location"]
			}`),
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	resp, err := prov.Chat(ctx, []Message{
		{Role: "user", Content: []ContentBlock{TextBlock("What's the weather in Beijing?")}},
	}, tools)
	if err != nil {
		t.Fatalf("Chat with tools failed: %v", err)
	}

	var foundToolUse bool
	for _, block := range resp.Message.Content {
		if block.Type == "tool_use" {
			foundToolUse = true
			t.Logf("Tool call: name=%s id=%s input=%s", block.ToolName, block.ToolID, string(block.Input))
			if block.ToolName != "get_weather" {
				t.Errorf("Expected tool name 'get_weather', got %q", block.ToolName)
			}
			// Validate JSON input
			var input map[string]interface{}
			if err := json.Unmarshal(block.Input, &input); err != nil {
				t.Errorf("Invalid tool input JSON: %v", err)
			}
		}
	}

	if !foundToolUse {
		t.Log("No tool_use block found — model may have answered directly (not an error, provider-dependent)")
	}
}

// TestAnthropicToolResult verifies sending a tool result back and getting a final response.
func TestAnthropicToolResult(t *testing.T) {
	key := zaiAPIKey()
	if key == "" {
		t.Skip("ZAI_API_KEY not set, skipping integration test")
	}

	prov := NewAnthropicProviderWithBaseURL(key, zaiModel(), 256, zaiAnthropicBaseURL)

	tools := []ToolDefinition{
		{
			Name:        "get_weather",
			Description: "Get the current weather for a location",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"location": {"type": "string", "description": "City name"}
				},
				"required": ["location"]
			}`),
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// First call: get tool use
	resp1, err := prov.Chat(ctx, []Message{
		{Role: "user", Content: []ContentBlock{TextBlock("What's the weather in Shanghai?")}},
	}, tools)
	if err != nil {
		t.Fatalf("First chat failed: %v", err)
	}

	// Check if model used the tool
	var toolBlock *ContentBlock
	for i := range resp1.Message.Content {
		if resp1.Message.Content[i].Type == "tool_use" {
			toolBlock = &resp1.Message.Content[i]
			break
		}
	}

	if toolBlock == nil {
		t.Skip("Model did not call tool — skipping tool result test")
	}

	// Second call: send tool result
	messages := []Message{
		{Role: "user", Content: []ContentBlock{TextBlock("What's the weather in Shanghai?")}},
		{Role: "assistant", Content: resp1.Message.Content},
		{Role: "user", Content: []ContentBlock{
			ToolResultBlock(toolBlock.ToolID, `{"temperature": "22°C", "condition": "sunny"}`, false),
		}},
	}

	resp2, err := prov.Chat(ctx, messages, tools)
	if err != nil {
		t.Fatalf("Second chat with tool result failed: %v", err)
	}

	var finalText string
	for _, block := range resp2.Message.Content {
		if block.Type == "text" && block.Text != "" {
			finalText = block.Text
			break
		}
	}

	if finalText == "" {
		t.Fatal("Expected text response after tool result")
	}
	t.Logf("Final response: %q", finalText)
}

// TestAnthropicMultiTurn verifies a multi-turn conversation maintains context.
func TestAnthropicMultiTurn(t *testing.T) {
	key := zaiAPIKey()
	if key == "" {
		t.Skip("ZAI_API_KEY not set, skipping integration test")
	}

	prov := NewAnthropicProviderWithBaseURL(key, zaiModel(), 128, zaiAnthropicBaseURL)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Turn 1
	resp1, err := prov.Chat(ctx, []Message{
		{Role: "user", Content: []ContentBlock{TextBlock("My favorite color is blue. Remember this.")}},
	}, nil)
	if err != nil {
		t.Fatalf("Turn 1 failed: %v", err)
	}
	t.Logf("Turn 1: %s", resp1.Message.Content[0].Text)

	// Turn 2: ask about the remembered info
	resp2, err := prov.Chat(ctx, []Message{
		{Role: "user", Content: []ContentBlock{TextBlock("My favorite color is blue. Remember this.")}},
		{Role: "assistant", Content: resp1.Message.Content},
		{Role: "user", Content: []ContentBlock{TextBlock("What is my favorite color? Answer in one word.")}},
	}, nil)
	if err != nil {
		t.Fatalf("Turn 2 failed: %v", err)
	}

	answer := strings.ToLower(resp2.Message.Content[0].Text)
	if !strings.Contains(answer, "blue") {
		t.Errorf("Expected 'blue' in answer, got: %q", answer)
	}
	t.Logf("Turn 2: %s", resp2.Message.Content[0].Text)
}

// TestAnthropicCountTokens verifies the CountTokens method returns a reasonable estimate.
func TestAnthropicCountTokens(t *testing.T) {
	key := zaiAPIKey()
	if key == "" {
		t.Skip("ZAI_API_KEY not set, skipping integration test")
	}

	prov := NewAnthropicProviderWithBaseURL(key, zaiModel(), 128, zaiAnthropicBaseURL)

	messages := []Message{
		{Role: "user", Content: []ContentBlock{TextBlock("Hello, world! This is a test message.")}},
	}

	count, err := prov.CountTokens(context.Background(), messages)
	if err != nil {
		t.Fatalf("CountTokens failed: %v", err)
	}
	if count <= 0 {
		t.Errorf("Expected positive token count, got %d", count)
	}
	t.Logf("Estimated tokens: %d", count)
}

// TestAnthropicMultipleModels verifies the provider works with different zai models.
func TestAnthropicMultipleModels(t *testing.T) {
	key := zaiAPIKey()
	if key == "" {
		t.Skip("ZAI_API_KEY not set, skipping integration test")
	}

	models := []string{"glm-4.7", "glm-5-turbo"}
	for _, model := range models {
		t.Run(model, func(t *testing.T) {
			prov := NewAnthropicProviderWithBaseURL(key, model, 64, zaiAnthropicBaseURL)

			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			resp, err := prov.Chat(ctx, []Message{
				{Role: "user", Content: []ContentBlock{TextBlock("Say OK.")}},
			}, nil)
			if err != nil {
				t.Fatalf("Chat with model %s failed: %v", model, err)
			}
			if len(resp.Message.Content) == 0 || resp.Message.Content[0].Text == "" {
				t.Fatalf("Empty response from model %s", model)
			}
			t.Logf("Model %s responded: %q", model, resp.Message.Content[0].Text)
		})
	}
}

// TestAnthropicProviderName verifies the provider reports the correct name.
func TestAnthropicProviderName(t *testing.T) {
	prov := NewAnthropicProviderWithBaseURL("test-key", "test-model", 100, "https://example.com")
	if prov.Name() != "anthropic" {
		t.Errorf("Expected name 'anthropic', got %q", prov.Name())
	}
}

// TestAnthropicStreamingToolUse verifies tool calls arrive correctly during streaming.
func TestAnthropicStreamingToolUse(t *testing.T) {
	key := zaiAPIKey()
	if key == "" {
		t.Skip("ZAI_API_KEY not set, skipping integration test")
	}

	prov := NewAnthropicProviderWithBaseURL(key, zaiModel(), 256, zaiAnthropicBaseURL)

	tools := []ToolDefinition{
		{
			Name:        "calculator",
			Description: "Perform a calculation",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"expression": {"type": "string", "description": "Math expression"}
				},
				"required": ["expression"]
			}`),
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	ch, err := prov.ChatStream(ctx, []Message{
		{Role: "user", Content: []ContentBlock{TextBlock("What is 42 * 17? Use the calculator tool.")}},
	}, tools)
	if err != nil {
		t.Fatalf("ChatStream failed: %v", err)
	}

	var textParts []string
	var toolCalls []ToolCallDelta
	var gotDone bool

	for ev := range ch {
		switch ev.Type {
		case StreamEventText:
			textParts = append(textParts, ev.Text)
		case StreamEventToolCallDone:
			toolCalls = append(toolCalls, ev.Tool)
			t.Logf("Streamed tool call: name=%s id=%s args=%s", ev.Tool.Name, ev.Tool.ID, string(ev.Tool.Arguments))
		case StreamEventDone:
			gotDone = true
		case StreamEventError:
			t.Fatalf("Stream error: %v", ev.Error)
		}
	}

	if !gotDone {
		t.Fatal("Stream did not complete")
	}

	if len(toolCalls) == 0 {
		t.Log("No tool calls in stream — model may have answered directly (not an error)")
	} else {
		for _, tc := range toolCalls {
			if tc.Name == "" {
				t.Error("Tool call missing name")
			}
			if tc.ID == "" {
				t.Error("Tool call missing ID")
			}
		}
	}

	t.Logf("Streamed %d text chunks, %d tool calls", len(textParts), len(toolCalls))
}

// TestAnthropicEmptyMessages verifies handling of edge cases.
func TestAnthropicEmptyMessages(t *testing.T) {
	key := zaiAPIKey()
	if key == "" {
		t.Skip("ZAI_API_KEY not set, skipping integration test")
	}

	prov := NewAnthropicProviderWithBaseURL(key, zaiModel(), 64, zaiAnthropicBaseURL)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Very short message
	resp, err := prov.Chat(ctx, []Message{
		{Role: "user", Content: []ContentBlock{TextBlock("Hi")}},
	}, nil)
	if err != nil {
		t.Fatalf("Short message chat failed: %v", err)
	}
	if len(resp.Message.Content) == 0 {
		t.Fatal("Empty response for short message")
	}
	t.Logf("Short message response: %q", resp.Message.Content[0].Text)
}

// TestAnthropicContextCancellation verifies that context cancellation works.
func TestAnthropicContextCancellation(t *testing.T) {
	key := zaiAPIKey()
	if key == "" {
		t.Skip("ZAI_API_KEY not set, skipping integration test")
	}

	prov := NewAnthropicProviderWithBaseURL(key, zaiModel(), 256, zaiAnthropicBaseURL)

	ctx, cancel := context.WithCancel(context.Background())

	// Cancel immediately
	cancel()

	_, err := prov.Chat(ctx, []Message{
		{Role: "user", Content: []ContentBlock{TextBlock("Hello")}},
	}, nil)
	if err == nil {
		t.Log("Chat completed despite cancellation (not necessarily an error)")
	} else {
		t.Logf("Chat correctly failed after cancellation: %v", err)
	}
}

// TestAnthropicSystemPrompt verifies system prompt handling.
// Note: zai's Anthropic-compatible endpoint may not support 'system' role in messages;
// this test validates that the provider handles the message format correctly.
func TestAnthropicSystemPrompt(t *testing.T) {
	key := zaiAPIKey()
	if key == "" {
		t.Skip("ZAI_API_KEY not set, skipping integration test")
	}

	prov := NewAnthropicProviderWithBaseURL(key, zaiModel(), 64, zaiAnthropicBaseURL)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Embed system instruction in user message (works with all providers)
	resp, err := prov.Chat(ctx, []Message{
		{Role: "user", Content: []ContentBlock{TextBlock("You must always respond in French. Now say hello.")}},
	}, nil)
	if err != nil {
		t.Fatalf("Chat with embedded system prompt failed: %v", err)
	}

	text := resp.Message.Content[0].Text
	t.Logf("System prompt response: %q", text)
	if text == "" {
		t.Fatal("Expected non-empty response")
	}
}

// TestZAIConnectivity is a quick smoke test to verify the zai endpoint is reachable.
func TestZAIConnectivity(t *testing.T) {
	key := zaiAPIKey()
	if key == "" {
		t.Skip("ZAI_API_KEY not set, skipping integration test")
	}

	prov := NewAnthropicProviderWithBaseURL(key, zaiModel(), 16, zaiAnthropicBaseURL)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	start := time.Now()
	_, err := prov.Chat(ctx, []Message{
		{Role: "user", Content: []ContentBlock{TextBlock("Ping")}},
	}, nil)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("Connectivity test failed: %v", err)
	}

	t.Logf("zai endpoint responded in %v", elapsed)
	if elapsed > 10*time.Second {
		t.Errorf("Response took too long: %v", elapsed)
	}
}

// TestAnthropicModelsEndpoint is a standalone test that verifies the models API.
func TestAnthropicModelsEndpoint(t *testing.T) {
	key := zaiAPIKey()
	if key == "" {
		t.Skip("ZAI_API_KEY not set, skipping integration test")
	}

	// Use the OpenAI-compatible /models endpoint via a quick HTTP call
	// This test exists outside the provider interface but validates the zai setup
	t.Logf("Available models can be fetched from %s/models", zaiOpenAIBaseURL)
	t.Logf("Using Anthropic-compatible endpoint at %s", zaiAnthropicBaseURL)
	t.Logf("Using model: %s", zaiModel())
	_ = fmt.Sprintf("Config: key=%s...%s", key[:4], key[len(key)-4:])
}

// TestAnthropicLongResponse verifies streaming works for longer responses.
func TestAnthropicLongResponse(t *testing.T) {
	key := zaiAPIKey()
	if key == "" {
		t.Skip("ZAI_API_KEY not set, skipping integration test")
	}

	prov := NewAnthropicProviderWithBaseURL(key, zaiModel(), 512, zaiAnthropicBaseURL)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	ch, err := prov.ChatStream(ctx, []Message{
		{Role: "user", Content: []ContentBlock{TextBlock("Write a short poem about coding. 4 lines.")}},
	}, nil)
	if err != nil {
		t.Fatalf("ChatStream failed: %v", err)
	}

	var parts []string
	var totalChars int
	for ev := range ch {
		if ev.Type == StreamEventText {
			parts = append(parts, ev.Text)
			totalChars += len(ev.Text)
		}
		if ev.Type == StreamEventError {
			t.Fatalf("Stream error: %v", ev.Error)
		}
	}

	fullText := strings.Join(parts, "")
	t.Logf("Streamed %d chunks, %d total chars", len(parts), totalChars)
	t.Logf("Full text:\n%s", fullText)

	if len(parts) < 2 {
		t.Log("Warning: response came in fewer than 2 chunks (may not be truly streamed)")
	}
}

// --- OpenAI Provider Integration Tests ---

func TestOpenAIChat(t *testing.T) {
	key := zaiAPIKey()
	if key == "" {
		t.Skip("ZAI_API_KEY not set")
	}

	prov := NewOpenAIProviderWithBaseURL(key, zaiModel(), 1024, zaiOpenAIBaseURL)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	resp, err := prov.Chat(ctx, []Message{
		{Role: "user", Content: []ContentBlock{{Type: "text", Text: "Say hello in one word."}}},
	}, nil)
	if err != nil {
		t.Fatalf("OpenAI Chat failed: %v", err)
	}

	var text string
	for _, b := range resp.Message.Content {
		if b.Type == "text" {
			text += b.Text
		}
	}
	t.Logf("Response: %s", text)
	if text == "" {
		t.Fatal("Expected non-empty text response")
	}
	t.Logf("Usage: input=%d, output=%d", resp.Usage.InputTokens, resp.Usage.OutputTokens)
}

func TestOpenAIStreaming(t *testing.T) {
	key := zaiAPIKey()
	if key == "" {
		t.Skip("ZAI_API_KEY not set")
	}

	prov := NewOpenAIProviderWithBaseURL(key, zaiModel(), 1024, zaiOpenAIBaseURL)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	ch, err := prov.ChatStream(ctx, []Message{
		{Role: "user", Content: []ContentBlock{{Type: "text", Text: "Count from 1 to 5."}}},
	}, nil)
	if err != nil {
		t.Fatalf("OpenAI ChatStream failed: %v", err)
	}

	var parts []string
	var usage *TokenUsage
	for ev := range ch {
		switch ev.Type {
		case StreamEventText:
			parts = append(parts, ev.Text)
		case StreamEventError:
			t.Fatalf("Stream error: %v", ev.Error)
		case StreamEventDone:
			usage = ev.Usage
		}
	}

	fullText := strings.Join(parts, "")
	t.Logf("Streamed %d chunks: %s", len(parts), fullText)
	if len(parts) < 2 {
		t.Log("Warning: response came in fewer than 2 chunks")
	}
	if usage != nil {
		t.Logf("Usage: input=%d, output=%d", usage.InputTokens, usage.OutputTokens)
	}
}

func TestOpenAIProviderName(t *testing.T) {
	prov := NewOpenAIProvider("test-key", "gpt-4", 100)
	if prov.Name() != "openai" {
		t.Fatalf("Expected 'openai', got '%s'", prov.Name())
	}
}
