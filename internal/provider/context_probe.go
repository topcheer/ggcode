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
	"github.com/topcheer/ggcode/internal/safego"
	"github.com/topcheer/ggcode/internal/util"
)

// probeTiers defines the context window sizes to try, from largest to
// smallest. Models below 64K are not worth probing — they won't work
// well for a coding agent anyway.
var probeTiers = []int{
	1_000_000,
	512_000,
	256_000,
	200_000,
	168_000,
	128_000,
	100_000,
	64_000,
}

// probeTokenBudget caps the total billed input tokens one tiered probe
// run may spend (#2116). Every tier attempt bills ~tier tokens of padding
// regardless of outcome, so a small-window model behind a gateway whose
// overflow errors carry no exact number descended 1M+512K+256K ≈ 1.8M
// tokens. The cap stops the descent and asks for explicit config; a model
// that legitimately fits a tier succeeds within the budget because the
// first tier that fits returns immediately.
const probeTokenBudget = 1_500_000

// tierFitsBudget reports whether firing the next tier keeps the run within
// probeTokenBudget given what has already been spent (#2116).
func tierFitsBudget(spent, nextTier, budget int) bool {
	return spent+nextTier <= budget
}

// ProbeResult is delivered asynchronously after a probe completes.
// probeInflight tracks running background probes by key (#1789 case 1).
var (
	probeInflightMu sync.Mutex
	probeInflight   = map[string]bool{}
)

type ProbeResult struct {
	Key           string // "vendor|baseURL|model"
	ContextWindow int    // discovered value, 0 if probe failed
	FromCache     bool   // true if value came from persistent cache
}

var (
	probeCacheMu sync.RWMutex
	probeCache   = map[string]int{} // key → context window
	probeLoaded  bool
)

func probeCachePath() string {
	return filepath.Join(config.ConfigDir(), "state", "context_windows.json")
}

const probeCacheVersion = 3

type probeCacheData struct {
	Version int            `json:"version"`
	Entries map[string]int `json:"entries"`
}

func loadProbeCache() {
	path := probeCachePath()
	data, err := os.ReadFile(path)
	if err != nil {
		debug.Log("probe", "no cache file at %s: %v", path, err)
		return
	}

	// Try to unmarshal as versioned cache format
	var cache probeCacheData
	if err := json.Unmarshal(data, &cache); err == nil && cache.Version > 0 {

		if cache.Version < probeCacheVersion {
			debug.Log("probe", "cache migration: v%d → v%d", cache.Version, probeCacheVersion)
			dropped := applyCacheMigration(cache.Version, &cache)
			cache.Version = probeCacheVersion
			saveProbeCacheData(cache)
			probeCacheMu.Lock()
			probeCache = cache.Entries
			probeCacheMu.Unlock()
			debug.Log("probe", "loaded %d entries from %s (dropped %d legacy entries)",
				len(cache.Entries), path, dropped)
			return
		}

		probeCacheMu.Lock()
		probeCache = cache.Entries
		probeCacheMu.Unlock()
		debug.Log("probe", "loaded %d entries from %s", len(cache.Entries), path)
		return
	}

	debug.Log("probe", "legacy cache format detected, migrating to v%d", probeCacheVersion)
	var legacyMap map[string]int
	if err := json.Unmarshal(data, &legacyMap); err != nil {
		debug.Log("probe", "cache parse error: %v", err)
		return
	}

	entries := make(map[string]int, len(legacyMap))
	for k, v := range legacyMap {
		entries[k] = v
	}
	cache = probeCacheData{
		Version: probeCacheVersion,
		Entries: entries,
	}
	dropped := applyCacheMigration(0, &cache)
	saveProbeCacheData(cache)
	probeCacheMu.Lock()
	probeCache = cache.Entries
	probeCacheMu.Unlock()
	debug.Log("probe", "loaded %d entries from %s (dropped %d legacy entries)",
		len(cache.Entries), path, dropped)
}

// applyCacheMigration drops entries below the minimum tier (128K) that were
// written by the old estimate-based inference. Returns count of dropped entries.
func applyCacheMigration(fromVersion int, cache *probeCacheData) int {
	dropped := 0
	if fromVersion < 2 {
		minTier := contextOverflowTiers[len(contextOverflowTiers)-1]
		for k, v := range cache.Entries {
			if v < minTier {
				delete(cache.Entries, k)
				dropped++
			}
		}
	}
	if fromVersion < 3 {

		for k := range cache.Entries {
			model := k
			if idx := strings.LastIndex(k, "|"); idx >= 0 {
				model = k[idx+1:]
			}
			if lookupKnownRetiredFamilyHijack(model) {
				delete(cache.Entries, k)
				dropped++
			}
		}
	}
	return dropped
}

// lookupKnownRetiredFamilyHijack reports whether model is an unlisted variant
// of a bare family name whose prefix-match retirement (#782/#1287) means the
// cached value under it can no longer be trusted: the model starts with a
// retired family name, the NEW known table returns 0 for it (falls through to
// probe), and the model is not itself protected by an earlier specific entry
// (which the lookup already honors — returning 0 here means it did not).
func lookupKnownRetiredFamilyHijack(model string) bool {
	if LookupKnownModelContextWindow(model) != 0 {
		return false
	}
	for _, fam := range []string{"gpt-4", "qwen3", "glm-4", "deepseek-chat", "deepseek-v3"} {
		if strings.HasPrefix(strings.ToLower(model), fam) {
			return true
		}
	}
	return false
}

func saveProbeCache() {
	probeCacheMu.RLock()
	snap := make(map[string]int, len(probeCache))
	for k, v := range probeCache {
		snap[k] = v
	}
	probeCacheMu.RUnlock()

	cache := probeCacheData{
		Version: probeCacheVersion,
		Entries: snap,
	}
	saveProbeCacheData(cache)
}

func saveProbeCacheData(cache probeCacheData) {
	path := probeCachePath()
	// #1337: same lost-update shape as the cron store - hold a cross-process
	// lock around the read-merge-write cycle so two processes' saves don't
	// erase each other's disk-only entries. Degrade to unlocked on lock
	// failure: the merge still reduces loss.
	var unlock func()
	if lock, err := util.FileLock(path + ".flock"); err == nil {
		unlock = lock
		defer unlock()
	} else {
		debug.Log("probe", "cache lock unavailable: %v", err)
	}

	if data, err := os.ReadFile(probeCachePath()); err == nil {
		var onDisk probeCacheData
		if json.Unmarshal(data, &onDisk) == nil {
			merged := make(map[string]int, len(cache.Entries)+len(onDisk.Entries))
			for k, v := range onDisk.Entries {
				merged[k] = v
			}
			for k, v := range cache.Entries {
				merged[k] = v
			}
			cache.Entries = merged
		}
	}
	data, err := json.MarshalIndent(cache, "", "  ")
	if err != nil {
		debug.Log("probe", "cache marshal error: %v", err)
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		debug.Log("probe", "cache mkdir error: %v", err)
		return
	}
	if err := util.AtomicWriteFile(path, data, 0o644); err != nil {
		debug.Log("probe", "cache save error: %v", err)
	} else {
		debug.Log("probe", "cache saved %d entries to %s", len(cache.Entries), path)
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

// contextOverflowTiers are used when inferring context window from an overflow
// error. Ordered from largest to smallest. We pick the largest tier that does
// not exceed the estimated token count.
var contextOverflowTiers = []int{
	2_000_000,
	1_000_000,
	512_000,
	256_000,
	200_000,
	168_000,
	128_000,
}

// matchOverflowTier returns the largest contextOverflowTiers entry that is <=
// tokenCount. Returns 0 when tokenCount is smaller than every tier — callers
// must NOT substitute a floor value: the old 64K floor let a fuzzy local
// token estimate permanently shrink healthy 128K+ windows.
func matchOverflowTier(tokenCount int) int {
	for _, tier := range contextOverflowTiers {
		if tier <= tokenCount {
			return tier
		}
	}
	return 0
}

// InferContextWindowFromError is called when a context overflow error is
// received. It attempts to determine the model's actual context window:
//
//   - Parses the EXACT limit from the error message. This is required: an
//     overflow error without a number is not evidence of the real limit, and
//     the old fallback (matching the local token estimate against tiers)
//     shrank healthy windows to the 64K minimum tier whenever the estimate
//     undercounted, then persisted the bogus value. Window resets now happen
//     ONLY on precise numbers.
//   - Matches the exact value to the largest tier from contextOverflowTiers
//     that does not exceed it (e.g. 200019 → 200000).
//   - If the parsed value is below every tier (sub-128K model), trusts the
//     provider's precise number as-is instead of snapping up to 128K.
//   - If the inferred window is strictly smaller than currentMaxTokens,
//     updates via setMaxTokens and persists to the probe cache.
//
// Returns the inferred context window (0 if no update was needed/possible).
func InferContextWindowFromError(
	err error,
	currentTokenCount int,
	currentMaxTokens int,
	probeKey string,
	setMaxTokens func(int),
) int {
	if probeKey == "" {
		return 0
	}

	exactWindow := parseContextWindowFromError(err)
	if exactWindow <= 0 {
		debug.Log("probe", "overflow inference: no exact window in error, refusing to guess (tokens=%d, current=%d, key=%s)",
			currentTokenCount, currentMaxTokens, probeKey)
		return 0
	}

	tier := matchOverflowTier(exactWindow)
	if tier == 0 {
		tier = exactWindow
	}

	if tier >= currentMaxTokens {
		debug.Log("probe", "overflow inference: tier=%d >= current=%d, no update needed",
			tier, currentMaxTokens)
		return 0
	}

	debug.Log("probe", "overflow inference: parsed=%d tier=%d (was %d, key=%s)",
		exactWindow, tier, currentMaxTokens, probeKey)
	setMaxTokens(tier)
	SetProbeCache(probeKey, tier)
	return tier
}

// parseContextWindowFromError tries to extract the actual context window
// limit from an API error message. Many providers include the limit in
// their error responses.
var contextLimitPatterns = []*regexp.Regexp{

	regexp.MustCompile(`(?i)maximum context length\D+(\d+)`),

	regexp.MustCompile(`(?i)(\d+)\s*tokens?\s*>\s*(\d+)(?:\s*tokens?)?\s*maximum`),

	regexp.MustCompile(`(?i)maximum is\s+(\d+)`),

	regexp.MustCompile(`(?i)exceeds.*?\((\d+)[^)]*\)`),

	regexp.MustCompile(`(?i)(?:token|context|prompt|input)\s+limit\W+(\d+)`),

	regexp.MustCompile(`(?i)limit\W+(?:of\s+)?(\d+)\s*tokens?`),

	regexp.MustCompile(`(?i)maximum of\s+(\d+)\s*tokens?`),

	regexp.MustCompile(`(?i)model.*?max\w*\W+(\d+)\s*tokens?`),
}

func parseContextWindowFromError(err error) int {
	if err == nil {
		return 0
	}
	msg := err.Error()
	for i, re := range contextLimitPatterns {
		m := re.FindStringSubmatch(msg)
		if len(m) >= 2 {

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

// MakeProbeKey builds the cache key for a vendor+baseURL+model combination.
// Matches adaptiveCap's capKey convention.
func MakeProbeKey(vendor, baseURL, model string) string {
	return strings.Join([]string{
		strings.TrimSpace(vendor),
		strings.TrimSpace(baseURL),
		strings.TrimSpace(model),
	}, "|")
}

// ProbeContextWindow probes the actual context window limit for the given
// provider. It runs asynchronously and calls onResult when done.
//
// This is fully non-blocking:
//   - Cache hit → onResult called synchronously (O(1) read + SetContextWindow under lock)
//   - Cache miss → onResult called from a background goroutine
//
// The onResult callback may be called from any goroutine. The caller must
// ensure any shared state access within onResult is thread-safe.
// ContextManager.SetContextWindow is already mutex-protected, so it's safe.
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

	if cached := LookupProbeCache(key); cached > 0 {
		debug.Log("probe", "cache HIT: key=%s window=%d — applying synchronously", key, cached)
		onResult(ProbeResult{Key: key, ContextWindow: cached, FromCache: true})
		return
	}

	if known := LookupKnownModelContextWindow(model); known > 0 {
		debug.Log("probe", "known model: %s → context_window=%d — applying synchronously (caching)", model, known)
		SetProbeCache(key, known)
		onResult(ProbeResult{Key: key, ContextWindow: known, FromCache: false})
		return
	}

	probeInflightMu.Lock()
	if probeInflight[key] {
		probeInflightMu.Unlock()
		debug.Log("probe", "in-flight probe for key=%s suppressed duplicate launch", key)
		return
	}
	probeInflight[key] = true
	probeInflightMu.Unlock()

	debug.Log("probe", "cache MISS: key=%s — launching background goroutine", key)

	safego.Go("provider.contextProbe", func() {
		defer func() {
			probeInflightMu.Lock()
			delete(probeInflight, key)
			probeInflightMu.Unlock()
		}()
		start := time.Now()
		window := probeInBackground(ctx, p, key)
		elapsed := time.Since(start)
		if window > 0 {
			debug.Log("probe", "COMPLETE: key=%s window=%d took=%s", key, window, elapsed)
		} else {
			debug.Log("probe", "FAILED: key=%s no window found took=%s — will use inferContextWindow fallback", key, elapsed)
		}
		onResult(ProbeResult{Key: key, ContextWindow: window, FromCache: false})
	})
}

// probeInBackground does the actual probing. The strategy, in order:
//
//  1. Models API — query the provider's models endpoint (Gemini only,
//     others don't return token limits).
//
//  2. Simple probe — send "hi" to verify API connectivity, check if
//     the error message reveals the context limit.
//
//  3. Tiered probing — send padded messages from 1M down to 64K.
//     Padding matches tier size to test the real limit.
//
// No artificial timeout — this runs in a background goroutine and will
// complete at its own pace without blocking the user.
func probeInBackground(ctx context.Context, p Provider, key string) int {

	if window := tryModelsAPI(ctx, p); window > 0 {
		debug.Log("probe", "models API returned context_window=%d", window)
		SetProbeCache(key, window)
		return window
	}

	if ctx.Err() != nil {
		debug.Log("probe", "aborting: context error after models API: %v", ctx.Err())
		return 0
	}

	simpleResult := trySimpleProbe(ctx, p)
	if simpleResult > 0 {
		SetProbeCache(key, simpleResult)
		return simpleResult
	}
	if simpleResult < 0 {

		debug.Log("probe", "simple probe signalled abort, stopping all probing")
		return 0
	}

	if ctx.Err() != nil {
		debug.Log("probe", "aborting: context error after simple probe: %v", ctx.Err())
		return 0
	}

	debug.Log("probe", "simple probe inconclusive, starting tiered probing with %d tiers", len(probeTiers))

	spent := 0
	for i, tier := range probeTiers {
		if ctx.Err() != nil {
			debug.Log("probe", "tiered probe cancelled at tier[%d]=%d: %v", i, tier, ctx.Err())
			break
		}
		if !tierFitsBudget(spent, tier, probeTokenBudget) {
			debug.Log("probe", "tiered probe budget exhausted after %d tokens (next tier %d would exceed %d) — set context_window in config to avoid probing", spent, tier, probeTokenBudget)
			return 0
		}
		w := tryTierProbe(ctx, p, tier)
		spent += tier
		if w > 0 {
			SetProbeCache(key, w)
			return w
		}

		if w < 0 {
			debug.Log("probe", "tiered probe aborting at tier[%d]=%d (auth failure)", i, tier)
			return 0
		}
	}

	return 0
}

// tryModelsAPI queries the provider's models endpoint to extract the
// context window limit. Only effective for Gemini (returns inputTokenLimit).
func tryModelsAPI(ctx context.Context, p Provider) int {
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

// prober is implemented by all provider types (OpenAI, Anthropic, Gemini).
// It sends a single chat request without retry, adaptive cap tracking,
// or token counting.
type prober interface {
	probeChat(ctx context.Context, messages []Message) error
}

// chatNoRetry calls the provider's probeChat directly.
func chatNoRetry(ctx context.Context, p Provider, msgs []Message) error {
	pp, ok := p.(prober)
	if !ok {
		return fmt.Errorf("provider %T does not implement probeChat", p)
	}
	return pp.probeChat(ctx, msgs)
}

// trySimpleProbe sends "hi" to verify the API is working.
// Returns: >0 = found context limit in error, 0 = continue probing, -1 = abort.
func trySimpleProbe(ctx context.Context, p Provider) int {
	msgs := []Message{
		{Role: "user", Content: []ContentBlock{{Type: "text", Text: "hi"}}},
	}

	debug.Log("probe", "sending simple probe (no-retry)")
	err := chatNoRetry(ctx, p, msgs)
	if err != nil {

		if w := parseContextWindowFromError(err); w > 0 {
			return w
		}

		errMsg := strings.ToLower(err.Error())
		isContextError := strings.Contains(errMsg, "context") ||
			strings.Contains(errMsg, "token limit") ||
			strings.Contains(errMsg, "too long") ||
			strings.Contains(errMsg, "exceeds")

		if isContextError {

			debug.Log("probe", "simple probe hit context limit but couldn't parse exact value")
			return 0
		}
		if containsAny(errMsg, authKeywords) || containsAnyAnchored(errMsg, authStatusPatterns) {
			debug.Log("probe", "simple probe auth error (aborting all probing): %v", err)
			return -1
		}
		debug.Log("probe", "simple probe inconclusive error (rate-limit/network/5xx/timeout/unknown — deferring to tiers): %v", err)
		return 0
	}

	debug.Log("probe", "simple probe succeeded (no-retry)")
	return 0
}

// tryTierProbe sends a message padded to approximately `tier` tokens.
// Returns: >0 = found context limit in error, 0 = inconclusive/retry, -1 = abort.
// Like trySimpleProbe, classifies errors to distinguish transient failures
// (rate limits, 5xx, network) from genuine context overflow.
func tryTierProbe(ctx context.Context, p Provider, tier int) int {
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

	if ctx.Err() != nil {
		debug.Log("probe", "tier %dK aborted after %s: %v", tier/1000, elapsed, ctx.Err())
		return 0
	}

	if w := parseContextWindowFromError(err); w > 0 {
		debug.Log("probe", "tier %dK overflow — extracted exact limit=%dK from error", tier/1000, w/1000)
		return w
	}

	errMsg := strings.ToLower(err.Error())

	if IsContextOverflowError(err) {

		debug.Log("probe", "tier %dK FAILED in %s: context overflow (no exact value) — trying next tier", tier/1000, elapsed)
		return 0
	}

	if containsAny(errMsg, authKeywords) || containsAnyAnchored(errMsg, authStatusPatterns) {
		debug.Log("probe", "tier %dK FAILED in %s: auth error (aborting all probing): %v", tier/1000, elapsed, err)
		return -1
	}

	if containsAny(errMsg, rateLimitKeywords) || isRateLimitStatusHit(errMsg) {
		debug.Log("probe", "tier %dK FAILED in %s: rate limit (inconclusive, may retry): %v", tier/1000, elapsed, err)
		return 0
	}

	if containsAny(errMsg, networkKeywords) {
		debug.Log("probe", "tier %dK FAILED in %s: network error (inconclusive, may retry): %v", tier/1000, elapsed, err)
		return 0
	}

	if strings.Contains(errMsg, " 5") && (containsAny(errMsg, []string{"500", "502", "503", "504", "529"}) ||
		containsAnyAnchored(errMsg, []string{" 500 ", " 500,", " 500\n", " 502 ", " 502,", " 502\n",
			" 503 ", " 503,", " 503\n", " 504 ", " 504,", " 504\n", " 529 ", " 529,", " 529\n"})) {
		debug.Log("probe", "tier %dK FAILED in %s: 5xx error (inconclusive, may retry): %v", tier/1000, elapsed, err)
		return 0
	}

	if strings.Contains(errMsg, "timeout") || strings.Contains(errMsg, "deadline") {
		debug.Log("probe", "tier %dK FAILED in %s: timeout (inconclusive, may retry): %v", tier/1000, elapsed, err)
		return 0
	}

	debug.Log("probe", "tier %dK FAILED in %s: unknown error (inconclusive, may retry): %v", tier/1000, elapsed, err)
	return 0
}
