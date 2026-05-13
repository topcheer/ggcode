//go:build integration_local

package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/config"
	ctxpkg "github.com/topcheer/ggcode/internal/context"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/tool"
)

// withIsolatedHome creates a temp HOME so probe cache and config state don't
// pollute the real ~/.ggcode/. The real config is copied (not symlinked) so
// API keys are available but all writes go to the temp directory.
func withIsolatedHome(t *testing.T) string {
	t.Helper()
	origHome := os.Getenv("HOME")
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	t.Cleanup(func() { os.Setenv("HOME", origHome) })

	// Copy real config into temp home so API keys are available.
	src := filepath.Join(origHome, ".ggcode", "ggcode.yaml")
	dst := filepath.Join(tmpHome, ".ggcode", "ggcode.yaml")
	data, err := os.ReadFile(src)
	if err != nil {
		t.Skipf("no ~/.ggcode/ggcode.yaml: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, data, 0600); err != nil {
		t.Fatal(err)
	}

	// Also copy keys.env if it exists (API keys may live there).
	srcKeys := filepath.Join(origHome, ".ggcode", "keys.env")
	dstKeys := filepath.Join(tmpHome, ".ggcode", "keys.env")
	if kd, err := os.ReadFile(srcKeys); err == nil {
		_ = os.WriteFile(dstKeys, kd, 0600)
	}

	return tmpHome
}

// loadRealProvider loads the user's real config and creates a real provider.
// Returns the provider, resolved config, and probe key.
func loadRealProvider(t *testing.T) (provider.Provider, *config.ResolvedEndpoint, string) {
	t.Helper()
	home, _ := os.LookupEnv("HOME")
	configPath := filepath.Join(home, ".ggcode", "ggcode.yaml")
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		t.Skip("no ggcode.yaml in isolated home")
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}

	resolved, err := cfg.ResolveActiveEndpoint()
	if err != nil {
		t.Fatalf("ResolveActiveEndpoint: %v", err)
	}

	if resolved.APIKey == "" && resolved.AuthType != "oauth" {
		t.Skipf("no API key for vendor=%s endpoint=%s", resolved.VendorID, resolved.EndpointName)
	}

	prov, err := provider.NewProvider(resolved)
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}

	probeKey := provider.MakeProbeKey(resolved.VendorID, resolved.BaseURL, resolved.Model)
	return prov, resolved, probeKey
}

// collectStreamEvents drains a ChatStream channel and returns all events.
// Returns the first error event found, or nil if the stream completed normally.
func collectStreamEvents(t *testing.T, prov provider.Provider, messages []provider.Message) ([]provider.StreamEvent, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	stream, err := prov.ChatStream(ctx, messages, nil)
	if err != nil {
		return nil, err
	}

	var events []provider.StreamEvent
	var streamErr error
	for event := range stream {
		events = append(events, event)
		if event.Type == provider.StreamEventError {
			streamErr = event.Error
		}
	}
	return events, streamErr
}

// ─── Test 1: Real LLM overflow with direct ChatStream ───────────────────────
// Sends an oversized prompt directly to the LLM (no agent loop) to trigger a
// raw context overflow error, then verifies inference can parse it.

func TestIntegration_DirectOverflowInfersFromRealError(t *testing.T) {
	withIsolatedHome(t)
	prov, resolved, probeKey := loadRealProvider(t)

	t.Logf("vendor=%s model=%s protocol=%s", resolved.VendorID, resolved.Model, resolved.Protocol)

	// Build a prompt that exceeds the model's context window.
	// glm-5.1 has ~128K context → need ~150K+ tokens.
	// ~150K tokens ≈ ~600KB. Use 20000 repetitions (~800KB ≈ 200K tokens).
	bigText := strings.Repeat("The quick brown fox jumps over the lazy dog. This sentence is repeated to fill context. ", 20000)

	events, streamErr := collectStreamEvents(t, prov, []provider.Message{
		{Role: "user", Content: []provider.ContentBlock{
			{Type: "text", Text: bigText},
		}},
	})

	if streamErr == nil {
		// Check if we got text back (model accepted it).
		var hasText bool
		for _, e := range events {
			if e.Type == provider.StreamEventText && e.Text != "" {
				hasText = true
				break
			}
		}
		if hasText {
			t.Skip("LLM accepted the oversized prompt without overflow — try a larger prompt")
		}
		t.Fatal("no error event and no text in stream — unexpected")
	}

	t.Logf("Real overflow error: %v", streamErr)

	// Verify the error is recognized as context overflow.
	if !provider.IsContextOverflowError(streamErr) {
		t.Fatalf("error not recognized as context overflow: %v", streamErr)
	}

	// Run inference on the real error.
	cm := ctxpkg.NewManager(512_000)
	inferred := provider.InferContextWindowFromError(
		streamErr,
		500_000, // estimate: roughly the token count of our oversized prompt
		512_000,
		probeKey,
		func(n int) { cm.SetContextWindow(n) },
	)

	t.Logf("Inferred context window: %d (from real error)", inferred)
	t.Logf("Error message: %s", streamErr.Error())

	if inferred == 0 {
		t.Errorf("InferContextWindowFromError returned 0 — could not parse limit from real error")
		t.Errorf("This means parseContextWindowFromError needs a new regex pattern for this provider")
	}

	// If inference succeeded, verify persistence.
	if inferred > 0 {
		cached := provider.LookupProbeCache(probeKey)
		if cached != inferred {
			t.Errorf("cache mismatch: cached=%d inferred=%d", cached, inferred)
		}
		t.Logf("Probe cache persisted correctly: %d", cached)
	}
}

// ─── Test 2: Real multi-turn agent loop ──────────────────────────────────────
// Verifies the agent works normally with the real LLM — no forced overflow.
// This is a sanity check that the inference code doesn't break normal flows.

func TestIntegration_AgentLoopNormalMultiTurnWithRealLLM(t *testing.T) {
	withIsolatedHome(t)
	prov, resolved, probeKey := loadRealProvider(t)

	t.Logf("vendor=%s model=%s", resolved.VendorID, resolved.Model)

	a := NewAgent(prov, tool.NewRegistry(), "You are a helpful assistant. Be concise.", 5)
	a.SetProbeKey(probeKey)
	a.ContextManager().SetContextWindow(128_000)

	// Turn 1
	err := a.RunStreamWithContent(context.Background(),
		[]provider.ContentBlock{{Type: "text", Text: "What is the capital of France? Answer in one word."}},
		func(event provider.StreamEvent) {},
	)
	if err != nil {
		t.Fatalf("Turn 1 error: %v", err)
	}

	// Turn 2
	err = a.RunStreamWithContent(context.Background(),
		[]provider.ContentBlock{{Type: "text", Text: "What is its population? Answer briefly."}},
		func(event provider.StreamEvent) {},
	)
	if err != nil {
		t.Fatalf("Turn 2 error: %v", err)
	}

	// Turn 3
	err = a.RunStreamWithContent(context.Background(),
		[]provider.ContentBlock{{Type: "text", Text: "Name three famous landmarks there."}},
		func(event provider.StreamEvent) {},
	)
	if err != nil {
		t.Fatalf("Turn 3 error: %v", err)
	}

	t.Logf("3-turn conversation completed successfully")
	t.Logf("MaxTokens: %d (should be unchanged at 128000)", a.ContextManager().ContextWindow())
	t.Logf("TokenCount: %d", a.ContextManager().TokenCount())
	t.Logf("UsageRatio: %.2f%%", a.ContextManager().UsageRatio()*100)
}

// ─── Test 3: Persist → reload across agent instances ────────────────────────
// Uses direct ChatStream to trigger a real overflow and persist the inferred
// value, then creates a new agent and verifies it loads from the probe cache.

func TestIntegration_PersistAndReloadAcrossSessions(t *testing.T) {
	withIsolatedHome(t)
	prov, resolved, probeKey := loadRealProvider(t)

	t.Logf("vendor=%s model=%s", resolved.VendorID, resolved.Model)

	// --- Step 1: Trigger real overflow via direct ChatStream ---
	bigText := strings.Repeat("The quick brown fox jumps over the lazy dog. This sentence is repeated to fill context. ", 20000)

	_, streamErr := collectStreamEvents(t, prov, []provider.Message{
		{Role: "user", Content: []provider.ContentBlock{
			{Type: "text", Text: bigText},
		}},
	})
	if streamErr == nil {
		t.Skip("LLM accepted oversized prompt — cannot test persist/reload")
	}
	if !provider.IsContextOverflowError(streamErr) {
		t.Fatalf("expected overflow error, got: %v", streamErr)
	}

	// Run inference.
	cm := ctxpkg.NewManager(512_000)
	inferred := provider.InferContextWindowFromError(
		streamErr, 500_000, 512_000, probeKey,
		func(n int) { cm.SetContextWindow(n) },
	)
	if inferred == 0 {
		t.Skip("Inference returned 0 — cannot test persist/reload")
	}
	t.Logf("Step 1: Inferred context window = %d", inferred)

	// Verify probe cache.
	cached := provider.LookupProbeCache(probeKey)
	if cached != inferred {
		t.Fatalf("cache mismatch: cached=%d inferred=%d", cached, inferred)
	}

	// --- Step 2: New agent loads from cache ---
	a2 := NewAgent(prov, tool.NewRegistry(), "You are helpful.", 3)
	a2.SetProbeKey(probeKey)
	a2.ContextManager().SetContextWindow(128_000)

	// Simulate what TUI/daemon does on startup.
	if probeCachedValue := provider.LookupProbeCache(probeKey); probeCachedValue > 0 {
		a2.ContextManager().SetContextWindow(probeCachedValue)
	}

	session2Max := a2.ContextManager().ContextWindow()
	t.Logf("Step 2: New agent MaxTokens from cache = %d", session2Max)

	if session2Max != cached {
		t.Errorf("session 2 MaxTokens=%d != cached=%d", session2Max, cached)
	}

	// Verify the new agent works with the inferred context window.
	err := a2.RunStreamWithContent(context.Background(),
		[]provider.ContentBlock{{Type: "text", Text: "What is 2+2? Answer briefly."}},
		func(event provider.StreamEvent) {},
	)
	if err != nil {
		t.Fatalf("Session 2 RunStreamWithContent error: %v", err)
	}
	t.Logf("Step 2: Agent with cached context window responded successfully")
}

// ─── Test 4: No probe key → no inference (with real LLM) ────────────────────
// Verifies that without SetProbeKey, overflow does NOT update MaxTokens.

func TestIntegration_NoProbeKeyNoInference(t *testing.T) {
	withIsolatedHome(t)
	prov, resolved, _ := loadRealProvider(t)

	t.Logf("vendor=%s model=%s", resolved.VendorID, resolved.Model)

	bigText := strings.Repeat("The quick brown fox jumps over the lazy dog. This sentence is repeated to fill context. ", 20000)

	_, streamErr := collectStreamEvents(t, prov, []provider.Message{
		{Role: "user", Content: []provider.ContentBlock{
			{Type: "text", Text: bigText},
		}},
	})
	if streamErr == nil {
		t.Skip("LLM accepted oversized prompt")
	}
	if !provider.IsContextOverflowError(streamErr) {
		t.Fatalf("expected overflow error, got: %v", streamErr)
	}

	// Run inference WITHOUT a probe key.
	cm := ctxpkg.NewManager(512_000)
	inferred := provider.InferContextWindowFromError(
		streamErr, 500_000, 512_000, "", // empty probe key
		func(n int) { cm.SetContextWindow(n) },
	)

	if inferred != 0 {
		t.Errorf("expected 0 (no probe key), got %d — inference should be skipped", inferred)
	}
	if cm.ContextWindow() != 512_000 {
		t.Errorf("MaxTokens changed from 512000 to %d — should not change without probe key", cm.ContextWindow())
	}
	t.Logf("No probe key test passed — inference correctly skipped")
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

func init() {
	// Ensure the test binary can find config.
	_ = fmt.Sprintf
	_ = strings.Repeat
}
