package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/config"
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/util"
)

// probeTiers defines the context window sizes to try, from largest to
// smallest. Models below 64K are not worth probing — they won't work
// well for a coding agent anyway.
var probeTiers = []int{
	1_000_000, // 1M  — Gemini 2.5, etc.
	512_000,   // 512K
	256_000,   // 256K
	200_000,   // 200K — Claude
	168_000,   // 168K
	128_000,   // 128K — GPT-4 class
	100_000,   // 100K
	64_000,    // 64K — minimum viable
}

// ProbeResult is delivered asynchronously after a probe completes.
type ProbeResult struct {
	Key           string // "vendor|baseURL|model"
	ContextWindow int    // discovered value, 0 if probe failed
	FromCache     bool   // true if value came from persistent cache
}

// ─── persistent cache ──────────────────────────────────────────────────────

var (
	probeCacheMu sync.RWMutex
	probeCache   = map[string]int{} // key → context window
	probeLoaded  bool
)

func probeCachePath() string {
	return filepath.Join(config.ConfigDir(), "state", "context_windows.json")
}

func loadProbeCache() {
	path := probeCachePath()
	data, err := os.ReadFile(path)
	if err != nil {
		debug.Log("probe", "no cache file at %s: %v", path, err)
		return
	}
	var m map[string]int
	if err := json.Unmarshal(data, &m); err != nil {
		debug.Log("probe", "cache parse error: %v", err)
		return
	}
	probeCacheMu.Lock()
	probeCache = m
	probeCacheMu.Unlock()
	debug.Log("probe", "loaded %d entries from %s", len(m), path)
}

func saveProbeCache() {
	probeCacheMu.RLock()
	snap := make(map[string]int, len(probeCache))
	for k, v := range probeCache {
		snap[k] = v
	}
	probeCacheMu.RUnlock()

	data, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		debug.Log("probe", "cache marshal error: %v", err)
		return
	}
	path := probeCachePath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		debug.Log("probe", "cache mkdir error: %v", err)
		return
	}
	if err := util.AtomicWriteFile(path, data, 0o644); err != nil {
		debug.Log("probe", "cache save error: %v", err)
	} else {
		debug.Log("probe", "cache saved %d entries to %s", len(snap), path)
	}
}

// LookupProbeCache returns the cached context window for the given key.
// Returns 0 if not cached.
func LookupProbeCache(key string) int {
	if !probeLoaded {
		loadProbeCache()
		probeLoaded = true
	}
	probeCacheMu.RLock()
	defer probeCacheMu.RUnlock()
	return probeCache[key]
}

// SetProbeCache persists a discovered context window value.
func SetProbeCache(key string, window int) {
	if window <= 0 {
		return
	}
	probeCacheMu.Lock()
	probeCache[key] = window
	probeCacheMu.Unlock()
	saveProbeCache()
	debug.Log("probe", "cached context_window=%d for key=%s", window, key)
}

// ─── error message parsing ─────────────────────────────────────────────────

// parseContextWindowFromError tries to extract the actual context window
// limit from an API error message. Many providers include the limit in
// their error responses.
var contextLimitPatterns = []*regexp.Regexp{
	// "maximum context length is N" / "max context length: N"
	regexp.MustCompile(`(?i)maximum context length\W+(\d+)`),
	// "N tokens > M maximum" (Anthropic style — we want M)
	regexp.MustCompile(`(?i)(\d+)\s*tokens?\s*>\s*(\d+)\s*maximum`),
	// "exceeds ... (N)" (Gemini style)
	regexp.MustCompile(`(?i)exceeds.*?\((\d+)\)`),
	// "limit of N tokens" / "limit: N"
	regexp.MustCompile(`(?i)limit\W+(?:of\s+)?(\d+)`),
	// "maximum of N tokens"
	regexp.MustCompile(`(?i)maximum of\s+(\d+)`),
	// "model.*max.*N" (generic)
	regexp.MustCompile(`(?i)model.*?max\w*\W+(\d+)`),
}

func parseContextWindowFromError(err error) int {
	if err == nil {
		return 0
	}
	msg := err.Error()
	for i, re := range contextLimitPatterns {
		m := re.FindStringSubmatch(msg)
		if len(m) >= 2 {
			// For patterns with multiple captures, take the last number
			n, err := strconv.Atoi(m[len(m)-1])
			if err == nil && n >= 1000 {
				debug.Log("probe", "parsed context_window=%d from error (pattern #%d): %s", n, i, msg)
				return n
			}
		}
	}
	debug.Log("probe", "could not parse context window from error: %s", msg)
	return 0
}

// ─── key helpers ────────────────────────────────────────────────────────────

// MakeProbeKey builds the cache key for a vendor+baseURL+model combination.
// Matches adaptiveCap's capKey convention.
func MakeProbeKey(vendor, baseURL, model string) string {
	return strings.Join([]string{
		strings.TrimSpace(vendor),
		strings.TrimSpace(baseURL),
		strings.TrimSpace(model),
	}, "|")
}

// GetCachedContextWindow checks the persistent cache and returns the
// stored context window, or 0 if not cached.
func GetCachedContextWindow(vendor, baseURL, model string) int {
	return LookupProbeCache(MakeProbeKey(vendor, baseURL, model))
}

// ─── probe logic ────────────────────────────────────────────────────────────

// ProbeContextWindow probes the actual context window limit for the given
// provider. It runs asynchronously and calls onResult when done.
//
// This is fully non-blocking:
//   - Cache hit → onResult called synchronously (O(1) read + SetMaxTokens under lock)
//   - Cache miss → onResult called from a background goroutine
//
// The onResult callback may be called from any goroutine. The caller must
// ensure any shared state access within onResult is thread-safe.
// ContextManager.SetMaxTokens is already mutex-protected, so it's safe.
//
// Edge cases handled:
//   - nil provider → no probe, no callback
//   - empty vendor/baseURL/model → no probe, no callback
//   - API call failure → onResult called with ContextWindow=0
//   - All tiers fail → onResult called with ContextWindow=0
//   - Timeout (30s) → context cancellation stops probing
func ProbeContextWindow(ctx context.Context, p Provider, vendor, baseURL, model string, onResult func(ProbeResult)) {
	if p == nil {
		debug.Log("probe", "skipped: provider is nil")
		return
	}
	if strings.TrimSpace(vendor) == "" || strings.TrimSpace(model) == "" {
		debug.Log("probe", "skipped: empty vendor=%q or model=%q", vendor, model)
		return
	}

	key := MakeProbeKey(vendor, baseURL, model)
	debug.Log("probe", "starting probe for key=%s", key)

	// Phase 1: check cache
	if cached := LookupProbeCache(key); cached > 0 {
		debug.Log("probe", "cache HIT: key=%s window=%d — applying synchronously", key, cached)
		onResult(ProbeResult{Key: key, ContextWindow: cached, FromCache: true})
		return
	}

	debug.Log("probe", "cache MISS: key=%s — launching background goroutine", key)

	// Phase 2: fire background probe
	go func() {
		start := time.Now()
		window := probeInBackground(ctx, p, key)
		elapsed := time.Since(start)
		if window > 0 {
			debug.Log("probe", "COMPLETE: key=%s window=%d took=%s", key, window, elapsed)
		} else {
			debug.Log("probe", "FAILED: key=%s no window found took=%s — will use inferContextWindow fallback", key, elapsed)
		}
		onResult(ProbeResult{Key: key, ContextWindow: window, FromCache: false})
	}()
}

// probeInBackground does the actual probing. The strategy, in order:
//
//  1. Models API — query the provider's models endpoint for the model's
//     token limit. Only works for Gemini (returns inputTokenLimit).
//     OpenAI and Anthropic models API does NOT include context window info.
//
//  2. Simple probe — send "hi", check if the API error message reveals
//     the context limit (some APIs include it in error responses).
//
//  3. Tiered probing — send padded messages from 1M down to 64K to find
//     the actual limit by trial. Padding matches tier size (no scaling down).
//
// If all phases return 0, the caller keeps the existing context window
// (from config/inferContextWindow or the 128K default).
//
// No artificial timeout — this runs in a background goroutine and will
// complete at its own pace without blocking the user.
func probeInBackground(ctx context.Context, p Provider, key string) int {
	// Phase 1: Try models API (Gemini only — others don't return token limits)
	if window := tryModelsAPI(ctx, p); window > 0 {
		debug.Log("probe", "models API returned context_window=%d", window)
		SetProbeCache(key, window)
		return window
	}

	if ctx.Err() != nil {
		debug.Log("probe", "aborting: context error after models API: %v", ctx.Err())
		return 0
	}

	// Phase 2: Try simple probe — send "hi", check if error reveals the limit
	if window := trySimpleProbe(ctx, p); window > 0 {
		SetProbeCache(key, window)
		return window
	}

	if ctx.Err() != nil {
		debug.Log("probe", "aborting: context error after simple probe: %v", ctx.Err())
		return 0
	}

	debug.Log("probe", "simple probe inconclusive, starting tiered probing with %d tiers", len(probeTiers))

	// Phase 3: Tiered probing — send padded messages from largest tier downward.
	for i, tier := range probeTiers {
		if ctx.Err() != nil {
			debug.Log("probe", "tiered probe cancelled at tier[%d]=%d: %v", i, tier, ctx.Err())
			break
		}
		w := tryTierProbe(ctx, p, tier)
		if w > 0 {
			SetProbeCache(key, w)
			return w
		}
	}

	return 0
}

// tryModelsAPI queries the provider's models endpoint to extract the
// context window limit from the model metadata.
//
// Known API response formats:
//   - Gemini GET /v1beta/models/{model}: returns {"inputTokenLimit": N, "outputTokenLimit": M}
//   - OpenAI GET /v1/models/{model}: returns {"id", "object", "created", "owned_by"} — NO token limit
//   - Anthropic GET /v1/models/{model}: returns {"id", "display_name", "created_at", "type"} — NO token limit
//
// So this is only effective for Gemini. For others it returns 0 quickly.
func tryModelsAPI(ctx context.Context, p Provider) int {
	// Only Gemini's models API returns inputTokenLimit.
	if p.Name() != "gemini" {
		debug.Log("probe", "models API: skipped — %s does not expose token limits", p.Name())
		return 0
	}

	gp, ok := p.(*GeminiProvider)
	if !ok {
		debug.Log("probe", "models API: skipped — cannot cast to GeminiProvider")
		return 0
	}

	debug.Log("probe", "models API: querying Gemini models endpoint for %s", gp.model)
	return gp.probeModelsAPI(ctx, gp.model)
}

// prober is an internal interface for sending lightweight chat requests
// without retry, adaptive cap tracking, or token counting. Each provider
// implements this for context window probing.
type prober interface {
	probeChat(ctx context.Context, messages []Message) error
}

// chatNoRetry calls the provider's probeChat if available (bypasses retry),
// otherwise falls back to the normal Chat method.
func chatNoRetry(ctx context.Context, p Provider, msgs []Message) error {
	if pr, ok := p.(prober); ok {
		return pr.probeChat(ctx, msgs)
	}
	// Fallback: use normal Chat (has retry, but not all providers have probeChat)
	err := chatNoRetry(ctx, p, msgs)
	return err
}

// trySimpleProbe sends a minimal message to verify the API is working.
func trySimpleProbe(ctx context.Context, p Provider) int {
	msgs := []Message{
		{Role: "user", Content: []ContentBlock{{Type: "text", Text: "hi"}}},
	}

	debug.Log("probe", "sending simple probe (no-retry)")
	err := chatNoRetry(ctx, p, msgs)
	if err != nil {
		// Check if error contains context limit info
		if w := parseContextWindowFromError(err); w > 0 {
			return w
		}

		// Non-context error (auth, network, etc.) — stop probing entirely
		errMsg := strings.ToLower(err.Error())
		isContextError := strings.Contains(errMsg, "context") ||
			strings.Contains(errMsg, "token limit") ||
			strings.Contains(errMsg, "too long") ||
			strings.Contains(errMsg, "exceeds")

		if !isContextError {
			debug.Log("probe", "simple probe non-context error (auth/network?): %v", err)
			return 0
		}

		// Context error but couldn't parse the limit — continue to tiered probing
		debug.Log("probe", "simple probe hit context limit but couldn't parse exact value")
		return 0
	}

	debug.Log("probe", "simple probe succeeded (no-retry)")
	return 0
}

// tryTierProbe sends a message padded to approximately `tier` tokens.
// The padding is sized to match the tier — we want to test the REAL limit.
//
// Key insight: the caller guarantees that a simple probe ("hi") already
// succeeded with this provider. So auth, network, and API key are all fine.
// If a padded request fails, it's almost certainly because the padding
// exceeded the model's context window — not because of auth or network.
// We no longer try to match specific error keywords. Any non-success
// response is treated as context overflow. We still try to extract the
// exact numeric limit from the error for precision.
func tryTierProbe(ctx context.Context, p Provider, tier int) int {
	// Each "a " ≈ 1 token, so tier repetitions ≈ tier tokens.
	padding := strings.Repeat("a ", tier)

	msgs := []Message{
		{Role: "user", Content: []ContentBlock{{Type: "text", Text: padding}}},
	}

	debug.Log("probe", "sending tier probe: target=%dK padding_chars=%d padding_tokens≈%d",
		tier/1000, len(padding), tier)

	start := time.Now()
	err := chatNoRetry(ctx, p, msgs)
	elapsed := time.Since(start)

	if err == nil {
		debug.Log("probe", "tier %dK SUCCEEDED in %s — context window >= %dK", tier/1000, elapsed, tier/1000)
		return tier
	}

	// Context cancelled (app shutdown) — stop entirely
	if ctx.Err() != nil {
		debug.Log("probe", "tier %dK aborted after %s: %v", tier/1000, elapsed, ctx.Err())
		return 0
	}

	debug.Log("probe", "tier %dK FAILED in %s: %s", tier/1000, elapsed, err.Error())

	// Since simple probe succeeded, this failure is almost certainly
	// context overflow. Try to extract the exact limit for precision.
	if w := parseContextWindowFromError(err); w > 0 {
		debug.Log("probe", "tier %dK overflow — extracted exact limit=%dK from error", tier/1000, w/1000)
		return w
	}

	// No exact value in error, but still overflow — try next lower tier.
	debug.Log("probe", "tier %dK overflow (no exact value) — trying next tier", tier/1000)
	return 0
}

// formatWindow formats a context window size for display.
func formatWindow(n int) string {
	if n >= 1_000_000 {
		return fmt.Sprintf("%.0fM", float64(n)/1_000_000)
	}
	return fmt.Sprintf("%dK", n/1000)
}
