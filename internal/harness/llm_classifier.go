package harness

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
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
	// Complexity is the LLM's assessment of task complexity: "simple" or "complex".
	// "simple" = small localized change (rename, typo fix, single-line edit)
	// "complex" = multi-file change, new feature, bug investigation, refactoring
	// Only meaningful when IsCodeChange is true.
	Complexity string
}

// classifierResponse is the expected JSON structure from the LLM.
type classifierResponse struct {
	Classification string  `json:"classification"`
	Confidence     float64 `json:"confidence"`
	Reason         string  `json:"reason"`
	Complexity     string  `json:"complexity"`
}

// llmClassifierPrompt is the system prompt for the task router.
const llmClassifierPrompt = `You are a task router. Classify the following user input:

1. Is it a code change request?
   - "code_change": The user is requesting code modifications, bug fixes, refactoring, new features, or any engineering task that would modify files in a codebase.
   - "conversation": The user is asking a question, requesting explanation, having a chat, or anything that does NOT require modifying code files.

2. If it IS a code change, assess complexity:
   - "simple": Small localized change — typo fix, rename, single value update, one-line edit, config tweak. The main agent can handle this directly without an isolated workflow.
   - "complex": Multi-file change, new feature, bug investigation requiring testing, refactoring, architecture change. Benefits from an isolated worktree with review before merging.

Respond with ONLY a JSON object, no other text:
{"classification": "code_change" or "conversation", "confidence": 0.0 to 1.0, "complexity": "simple" or "complex" or "", "reason": "brief explanation"}`

// ClassifyWithLLM uses an LLM to classify whether a user input is a code change task.
// It returns nil (and a nil error) if the provider is nil, effectively disabling the classifier.
// On timeout or error, it returns nil with the error so callers can fall back to the deterministic router.
func ClassifyWithLLM(ctx context.Context, prov provider.Provider, input string) (*LLMClassifierResult, error) {
	if prov == nil {
		return nil, nil
	}
	if len([]rune(strings.TrimSpace(input))) < 10 {
		return nil, nil
	}

	// Truncate very long inputs to control cost
	if len(input) > 2000 {
		input = input[:2000]
	}

	userPrompt := fmt.Sprintf("User input: %q", input)

	// 8-second timeout (CN proxy endpoints can be slow)
	classifyCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
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

	debug.Log("llm-classifier", "result: is_code_change=%v confidence=%.2f complexity=%s reason=%s",
		result.IsCodeChange, result.Confidence, result.Complexity, truncateText(result.Reason, 60))

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

	isCodeChange := strings.EqualFold(strings.TrimSpace(resp.Classification), "code_change")

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
		Complexity:   normalizeClassifierComplexity(resp.Complexity),
	}, nil
}

// RouteFromLLMResult converts an LLM classifier result to a RouteDecision.
// In "on" and "suggest" modes, only complex code changes go to harness/suggestion.
// In "strict" mode, any confident code change goes to harness because the main
// agent write guard prevents direct project mutation.
// High confidence (≥0.8) complex code change → RouteHarness.
// Medium confidence (≥0.5) complex code change → RouteSuggest.
// Unknown complexity never auto-routes to harness in "on"; it asks instead.
// Simple code change or low confidence → RouteNormal unless mode is "strict".
func RouteFromLLMResult(result *LLMClassifierResult, mode string) RouteDecision {
	if result == nil || !result.IsCodeChange {
		return RouteNormal
	}
	mode = strings.ToLower(strings.TrimSpace(mode))

	if result.Confidence < 0.5 {
		return RouteNormal
	}

	complexity := normalizeClassifierComplexity(result.Complexity)
	if mode == "strict" {
		return RouteHarness
	}
	if complexity == "simple" {
		return RouteNormal
	}
	if complexity == "" {
		if result.Confidence >= 0.8 && (mode == "on" || mode == "suggest") {
			return RouteSuggest
		}
		return RouteNormal
	}

	switch mode {
	case "suggest":
		return RouteSuggest
	case "on":
		if result.Confidence >= 0.8 {
			return RouteHarness
		}
		return RouteSuggest
	default:
		return RouteNormal
	}
}

func normalizeClassifierComplexity(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "simple":
		return "simple"
	case "complex":
		return "complex"
	default:
		return ""
	}
}
