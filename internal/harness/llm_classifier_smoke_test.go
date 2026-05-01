//go:build integration

package harness

// This file contains smoke tests for the LLM classifier using a real provider.
//
// To run (example):
//   GGCODE_TEST_MODEL=gpt-4o-mini \
//   GGCODE_TEST_BASE_URL=https://aihubmix.com/v1 \
//   GGCODE_TEST_API_KEY=$AIHUBMIX_API_KEY \
//   GGCODE_TEST_PROTOCOL=openai \
//   go test -tags=integration -run TestSmoke_LLMClassifier ./internal/harness/... -v
//
// All configuration is read from env vars at test time, held in memory only.
// No keys are ever written to disk or committed to the repository.

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/provider"
)

func smokeProvider(t *testing.T) provider.Provider {
	t.Helper()

	model := os.Getenv("GGCODE_TEST_MODEL")
	baseURL := os.Getenv("GGCODE_TEST_BASE_URL")
	apiKey := os.Getenv("GGCODE_TEST_API_KEY")
	protocol := os.Getenv("GGCODE_TEST_PROTOCOL")

	if model == "" || baseURL == "" || apiKey == "" || protocol == "" {
		t.Skip("Set GGCODE_TEST_MODEL, GGCODE_TEST_BASE_URL, GGCODE_TEST_API_KEY, " +
			"GGCODE_TEST_PROTOCOL to run real LLM classifier smoke tests")
	}

	resolved := &config.ResolvedEndpoint{
		VendorID:      "smoke-test",
		VendorName:    "smoke-test",
		Protocol:      protocol,
		AuthType:      "bearer",
		BaseURL:       baseURL,
		APIKey:        apiKey,
		Model:         model,
		ContextWindow: 128000,
		MaxTokens:     1024,
	}

	prov, err := provider.NewProvider(resolved)
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}
	return prov
}

func logClassification(t *testing.T, result *LLMClassifierResult) {
	class := "conversation"
	if result.IsCodeChange {
		class = "code_change"
	}
	t.Logf("  classification: %s", class)
	t.Logf("  confidence:     %.2f", result.Confidence)
	t.Logf("  complexity:     %s", result.Complexity)
	t.Logf("  reason:         %s", result.Reason)
}

func TestSmoke_LLMClassifier_BugReport(t *testing.T) {
	prov := smokeProvider(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	result, err := ClassifyWithLLM(ctx, prov,
		"the login page shows a 500 error when the session expires and users can't log in")
	if err != nil {
		t.Fatalf("ClassifyWithLLM error: %v", err)
	}
	if result == nil {
		t.Fatal("ClassifyWithLLM returned nil")
	}
	logClassification(t, result)

	if !result.IsCodeChange {
		t.Errorf("expected code_change, got conversation (confidence=%.2f)", result.Confidence)
	}
}

func TestSmoke_LLMClassifier_Question(t *testing.T) {
	prov := smokeProvider(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	result, err := ClassifyWithLLM(ctx, prov,
		"what is the difference between TCP and UDP transport protocols")
	if err != nil {
		t.Fatalf("ClassifyWithLLM error: %v", err)
	}
	if result == nil {
		t.Fatal("ClassifyWithLLM returned nil")
	}
	logClassification(t, result)

	if result.IsCodeChange {
		t.Errorf("expected conversation, got code_change (confidence=%.2f)", result.Confidence)
	}
}

func TestSmoke_LLMClassifier_AmbiguousInput(t *testing.T) {
	prov := smokeProvider(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	result, err := ClassifyWithLLM(ctx, prov,
		"add error handling to the payment processing module")
	if err != nil {
		t.Fatalf("ClassifyWithLLM error: %v", err)
	}
	if result == nil {
		t.Fatal("ClassifyWithLLM returned nil")
	}
	logClassification(t, result)

	if !result.IsCodeChange || result.Confidence < 0.5 {
		t.Errorf("expected code_change with confidence≥0.5, got %v (%.2f)",
			result.IsCodeChange, result.Confidence)
	}
}

func TestSmoke_LLMClassifier_ChineseBugReport(t *testing.T) {
	prov := smokeProvider(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	result, err := ClassifyWithLLM(ctx, prov,
		"登录页面在session过期的时候报500错误，用户无法登录")
	if err != nil {
		t.Fatalf("ClassifyWithLLM error: %v", err)
	}
	if result == nil {
		t.Fatal("ClassifyWithLLM returned nil")
	}
	logClassification(t, result)

	if !result.IsCodeChange {
		t.Errorf("expected code_change for Chinese bug report, got conversation (confidence=%.2f)", result.Confidence)
	}
}

func TestSmoke_LLMClassifier_ChineseChat(t *testing.T) {
	prov := smokeProvider(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	result, err := ClassifyWithLLM(ctx, prov,
		"解释一下什么是微服务架构，以及它和单体架构的区别是什么")
	if err != nil {
		t.Fatalf("ClassifyWithLLM error: %v", err)
	}
	if result == nil {
		t.Fatal("ClassifyWithLLM returned nil")
	}
	logClassification(t, result)

	if result.IsCodeChange {
		t.Errorf("expected conversation for Chinese chat, got code_change (confidence=%.2f)", result.Confidence)
	}
}

func TestSmoke_LLMClassifier_SimpleChange(t *testing.T) {
	prov := smokeProvider(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	result, err := ClassifyWithLLM(ctx, prov,
		"把 README 里的版本号从 1.0 改成 2.0")
	if err != nil {
		t.Fatalf("ClassifyWithLLM error: %v", err)
	}
	if result == nil {
		t.Fatal("ClassifyWithLLM returned nil")
	}
	logClassification(t, result)

	if !result.IsCodeChange {
		t.Errorf("expected code_change, got conversation")
	}
	if result.Complexity != "simple" {
		t.Errorf("expected simple, got %s (confidence=%.2f)", result.Complexity, result.Confidence)
	}
}

func TestSmoke_LLMClassifier_ComplexFeature(t *testing.T) {
	prov := smokeProvider(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	result, err := ClassifyWithLLM(ctx, prov,
		"重构认证模块，把 JWT 换成 session-based 认证，需要修改登录、注册、密码重置等所有相关接口")
	if err != nil {
		t.Fatalf("ClassifyWithLLM error: %v", err)
	}
	if result == nil {
		t.Fatal("ClassifyWithLLM returned nil")
	}
	logClassification(t, result)

	if !result.IsCodeChange {
		t.Errorf("expected code_change, got conversation")
	}
	if result.Complexity != "complex" {
		t.Errorf("expected complex, got %s (confidence=%.2f)", result.Complexity, result.Confidence)
	}
}
