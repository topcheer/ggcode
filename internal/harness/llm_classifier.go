package harness

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/provider"
)

// LLMClassifierResult is the outcome of an LLM-based prompt classification.
type LLMClassifierResult struct {
	// IsCodeChange is true when the LLM classified the input as a code change task.
	IsCodeChange bool
	// Confidence is the LLM's confidence score (0.0-1.0).
	Confidence float64
	// Reason is the LLM's explanation for its classification.
	Reason string
}

// classifierResponse is the expected JSON structure from the LLM.
type classifierResponse struct {
	Classification string  `json:"classification"`
	Confidence     float64 `json:"confidence"`
	Reason         string  `json:"reason"`
}

// llmClassifierPrompt is the system prompt for the task router.
const llmClassifierPrompt = `You are a task router. Classify the following user input as either:
- "code_change": The user is requesting code modifications, bug fixes, refactoring, new features, or any engineering task that would modify files in a codebase.
- "conversation": The user is asking a question, requesting explanation, having a chat, or doing anything that does NOT require modifying code files.

Respond with ONLY a JSON object, no other text:
{"classification": "code_change" or "conversation", "confidence": 0.0 to 1.0, "reason": "brief explanation"}`

// ClassifyWithLLM uses an LLM to classify whether a user input is a code change task.
// It returns nil (and a nil error) if the provider is nil, effectively disabling the classifier.
// On timeout or error, it returns nil, falling back to the deterministic router.
func ClassifyWithLLM(ctx context.Context, prov provider.Provider, input string) (*LLMClassifierResult, error) {
	if prov == nil {
		return nil, nil
	}
	if len(input) < 20 {
		return nil, nil
	}

	// Truncate very long inputs to control cost
	if len(input) > 2000 {
		input = input[:2000]
	}

	userPrompt := fmt.Sprintf("User input: %q", input)

	// 3-second timeout
	classifyCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	resp, err := prov.Chat(classifyCtx, []provider.Message{
		{Role: "system", Content: []provider.ContentBlock{provider.TextBlock(llmClassifierPrompt)}},
		{Role: "user", Content: []provider.ContentBlock{provider.TextBlock(userPrompt)}},
	}, nil) // No tools needed for classification

	if err != nil {
		debug.Log("llm-classifier", "LLM call failed: %v", err)
		return nil, err
	}

	// Extract text from response
	text := extractTextFromResponse(resp)
	if text == "" {
		debug.Log("llm-classifier", "empty response from LLM")
		return nil, fmt.Errorf("empty LLM response")
	}

	// Parse JSON response
	result, err := parseClassifierResponse(text)
	if err != nil {
		debug.Log("llm-classifier", "parse failed: %v (input: %s)", err, truncateText(text, 100))
		return nil, err
	}

	debug.Log("llm-classifier", "result: is_code_change=%v confidence=%.2f reason=%s",
		result.IsCodeChange, result.Confidence, truncateText(result.Reason, 60))

	return result, nil
}

// extractTextFromResponse extracts the text content from a ChatResponse.
func extractTextFromResponse(resp *provider.ChatResponse) string {
	if resp == nil {
		return ""
	}
	for _, block := range resp.Message.Content {
		if block.Type == "text" {
			return block.Text
		}
	}
	return ""
}

// truncateText truncates a string to at most n characters.
func truncateText(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// jsonBlockRegex matches JSON objects in text (may be wrapped in markdown code blocks).
var jsonBlockRegex = regexp.MustCompile(`(?s)\{[^{}]*"classification"[^{}]*\}`)

// parseClassifierResponse parses the LLM's JSON response.
func parseClassifierResponse(text string) (*LLMClassifierResult, error) {
	// Try to extract JSON from potential markdown wrapping
	cleaned := text
	if match := jsonBlockRegex.FindString(text); match != "" {
		cleaned = match
	}

	var resp classifierResponse
	if err := json.Unmarshal([]byte(cleaned), &resp); err != nil {
		return nil, fmt.Errorf("parse JSON: %w", err)
	}

	isCodeChange := resp.Classification == "code_change"

	// Clamp confidence to [0, 1]
	confidence := resp.Confidence
	if confidence < 0 {
		confidence = 0
	}
	if confidence > 1 {
		confidence = 1
	}

	return &LLMClassifierResult{
		IsCodeChange: isCodeChange,
		Confidence:   confidence,
		Reason:       resp.Reason,
	}, nil
}

// RouteFromLLMResult converts an LLM classifier result to a RouteDecision.
// High confidence (≥0.8) code change → RouteHarness.
// Medium confidence (≥0.5) code change → RouteSuggest.
// Low confidence or not code change → RouteNormal.
func RouteFromLLMResult(result *LLMClassifierResult, mode string) RouteDecision {
	if result == nil || !result.IsCodeChange {
		return RouteNormal
	}

	switch mode {
	case "strict":
		// Strict mode: always route to harness for code changes
		if result.Confidence >= 0.5 {
			return RouteHarness
		}
		return RouteNormal
	case "on":
		if result.Confidence >= 0.8 {
			return RouteHarness
		}
		if result.Confidence >= 0.5 {
			return RouteSuggest
		}
		return RouteNormal
	default:
		return RouteNormal
	}
}
