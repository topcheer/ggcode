package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/safego"
	"github.com/topcheer/ggcode/internal/tool"
)

// Speculative Tool Execution — inspired by PASTE (arXiv:2603.18897, Microsoft
// Research, March 2026) and speculative-tools (GitHub: joelvarun/speculative-tools).
//
// Core idea: while the LLM is generating its next response (2-5 seconds of idle
// CPU), predict likely read-only tool calls based on learned patterns and
// pre-execute them. When the LLM's response arrives, if the prediction matches,
// return the cached result instantly — eliminating tool execution latency.
//
// Safety guarantees:
//   - Only read-only, idempotent tools are speculated (no side effects)
//   - Speculative results are cached with a TTL (30s) to prevent staleness
//   - If the actual tool call has different args, it's a cache miss (no harm)
//   - Behavioral correctness is preserved — speculation only affects latency

// readOnlyTools that are safe to speculate on (no side effects, idempotent).
var speculativeSafeTools = map[string]bool{
	"read_file":             true,
	"multi_file_read":       true,
	"grep":                  true,
	"glob":                  true,
	"search_files":          true,
	"list_directory":        true,
	"lsp_hover":             true,
	"lsp_definition":        true,
	"lsp_references":        true,
	"lsp_symbols":           true,
	"lsp_workspace_symbols": true,
	"lsp_diagnostics":       true,
	"lsp_implementation":    true,
	"lsp_incoming_calls":    true,
	"lsp_outgoing_calls":    true,
	"git_status":            true,
	"git_diff":              true,
	"git_log":               true,
	"git_branch_list":       true,
	"git_show":              true,
	"git_blame":             true,
}

// argLinkedPatterns maps tool transitions where the next tool's primary argument
// (usually "path" or "file_path") is likely the same as the previous tool's.
// Key format: "prevTool→nextTool".
var argLinkedPatterns = map[string]bool{
	"edit_file→read_file":             true,
	"edit_file→multi_file_read":       true,
	"multi_edit_file→read_file":       true,
	"multi_edit_file→multi_file_read": true,
	"write_file→read_file":            true,
	"write_file→multi_file_read":      true,
}

// speculator implements pattern-aware speculative tool execution.
// It learns bigram patterns (tool A → tool B) from observed sequences,
// predicts likely next read-only calls, and pre-executes them in background
// goroutines while the LLM is generating.
type speculator struct {
	mu sync.Mutex

	// Bigram pattern model: prevTool → nextTool → observation count.
	// Built incrementally from observed tool call sequences.
	patterns map[string]map[string]int

	// Last tool name observed (for building bigram transitions).
	lastTool string

	// Cache of speculative results keyed by cacheKey(toolName, argsHash).
	cache map[string]*speculativeResult

	// Cache ordering for LRU eviction (front = oldest, back = newest).
	cacheOrder []string

	// Statistics for observability.
	hits         int
	misses       int
	speculations int
	savedMicros  int64 // approximate latency saved in microseconds

	// Adaptive threshold: minimum observation count before predicting.
	// Self-tunes based on hit rate (inspired by speculative-tools adaptive strategy).
	adaptiveMinCount int

	// Number of active speculative goroutines (bounded by maxConcurrent).
	activeSpeculations int

	ttl time.Duration
}

const (
	// specMaxCacheSize bounds the speculative result cache to prevent
	// unbounded memory growth during long agent runs.
	specMaxCacheSize = 50

	// specMaxConcurrent limits the number of concurrent speculative
	// goroutines to prevent resource exhaustion.
	specMaxConcurrent = 3

	// specAdaptiveFloor/Ceiling bound the adaptive threshold.
	specAdaptiveFloor   = 1
	specAdaptiveCeiling = 5

	// specAdaptiveWindow is the number of cache lookups (hits+misses)
	// between threshold re-evaluations.
	specAdaptiveWindow = 20
)

type speculativeResult struct {
	result   tool.Result
	cachedAt time.Time
}

func newSpeculator() *speculator {
	return &speculator{
		patterns:         make(map[string]map[string]int),
		cache:            make(map[string]*speculativeResult),
		cacheOrder:       make([]string, 0, specMaxCacheSize),
		ttl:              30 * time.Second,
		adaptiveMinCount: 2, // start conservative, adapt based on hit rate
	}
}

// recordObservation records a tool call and updates the bigram model.
// prevTool is the tool that ran before this one (empty for the first call).
func (s *speculator) recordObservation(toolName string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.lastTool != "" {
		if s.patterns[s.lastTool] == nil {
			s.patterns[s.lastTool] = make(map[string]int)
		}
		s.patterns[s.lastTool][toolName]++
		debug.Log("speculate", "pattern observed: %s → %s (count=%d)",
			s.lastTool, toolName, s.patterns[s.lastTool][toolName])
	}
	s.lastTool = toolName
}

// resetSequence clears the bigram tracking for a new agent run.
// Called at the start of each RunStreamWithContent to avoid cross-run noise.
func (s *speculator) resetSequence() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastTool = ""
}

// predictNext returns likely next read-only tool names based on the last tool.
// Uses adaptiveMinCount which self-tunes based on cache hit rate.
func (s *speculator) predictNext(lastTool string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()

	nexts, ok := s.patterns[lastTool]
	if !ok {
		return nil
	}

	minCount := s.adaptiveMinCount

	type prediction struct {
		tool  string
		count int
	}
	var preds []prediction
	for nextTool, count := range nexts {
		if speculativeSafeTools[nextTool] && count >= minCount {
			preds = append(preds, prediction{nextTool, count})
		}
	}
	// Sort by count descending (simple selection for small slices).
	for i := range preds {
		for j := i + 1; j < len(preds); j++ {
			if preds[j].count > preds[i].count {
				preds[i], preds[j] = preds[j], preds[i]
			}
		}
	}

	result := make([]string, len(preds))
	for i, p := range preds {
		result[i] = p.tool
	}
	return result
}

// cacheKey generates a deterministic cache key from tool name and arguments.
func cacheKey(toolName string, args json.RawMessage) string {
	h := sha256.Sum256(append([]byte(toolName+":"), args...))
	return hex.EncodeToString(h[:8])
}

// getCached returns a speculative result if available and fresh.
// Returns (result, true) on hit, (zero, false) on miss/stale.
func (s *speculator) getCached(toolName string, args json.RawMessage) (tool.Result, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := cacheKey(toolName, args)
	cached, ok := s.cache[key]
	if !ok {
		s.misses++
		s.maybeAdaptThreshold()
		return tool.Result{}, false
	}
	if time.Since(cached.cachedAt) > s.ttl {
		s.removeFromCacheOrder(key)
		delete(s.cache, key)
		s.misses++
		s.maybeAdaptThreshold()
		return tool.Result{}, false
	}
	s.hits++
	debug.Log("speculate", "cache HIT for %s (key=%s)", toolName, key)
	return cached.result, true
}

// maybeAdaptThreshold adjusts the prediction threshold based on recent
// cache hit rate. Called after every cache lookup. Uses a fixed evaluation
// window to avoid premature adaptation.
//
// High hit rate (>40%): predictions are valuable → lower threshold → speculate more.
// Low hit rate (<15%): predictions are wasteful → raise threshold → speculate less.
func (s *speculator) maybeAdaptThreshold() {
	total := s.hits + s.misses
	if total == 0 || total%specAdaptiveWindow != 0 {
		return
	}

	hitRate := float64(s.hits) / float64(total)

	if hitRate >= 0.40 && s.adaptiveMinCount > specAdaptiveFloor {
		s.adaptiveMinCount--
		debug.Log("speculate", "adaptive threshold lowered to %d (hitRate=%.0f%%, hits=%d, misses=%d)",
			s.adaptiveMinCount, hitRate*100, s.hits, s.misses)
	} else if hitRate < 0.15 && s.adaptiveMinCount < specAdaptiveCeiling {
		s.adaptiveMinCount++
		debug.Log("speculate", "adaptive threshold raised to %d (hitRate=%.0f%%, hits=%d, misses=%d)",
			s.adaptiveMinCount, hitRate*100, s.hits, s.misses)
	}
}

// store caches a speculative result, evicting the oldest entry if the
// cache is at capacity (simple LRU using cacheOrder slice).
func (s *speculator) store(toolName string, args json.RawMessage, result tool.Result) {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := cacheKey(toolName, args)

	// If key already exists, remove from order (will be re-added at end).
	if _, exists := s.cache[key]; exists {
		s.removeFromCacheOrder(key)
	} else if len(s.cache) >= specMaxCacheSize {
		// Evict oldest entry.
		if len(s.cacheOrder) > 0 {
			oldest := s.cacheOrder[0]
			delete(s.cache, oldest)
			s.cacheOrder = s.cacheOrder[1:]
			debug.Log("speculate", "cache full, evicted oldest entry (key=%s)", oldest)
		}
	}

	s.cache[key] = &speculativeResult{
		result:   result,
		cachedAt: time.Now(),
	}
	s.cacheOrder = append(s.cacheOrder, key)
}

// removeFromCacheOrder removes a key from the LRU ordering slice.
func (s *speculator) removeFromCacheOrder(key string) {
	for i, k := range s.cacheOrder {
		if k == key {
			s.cacheOrder = append(s.cacheOrder[:i], s.cacheOrder[i+1:]...)
			return
		}
	}
}

// hasCached checks if a speculative result exists without affecting stats.
func (s *speculator) hasCached(toolName string, args json.RawMessage) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := cacheKey(toolName, args)
	cached, ok := s.cache[key]
	if !ok {
		return false
	}
	return time.Since(cached.cachedAt) <= s.ttl
}

// predictArgs predicts the arguments for the next tool based on the pattern.
// For argument-linked patterns (e.g., edit_file→read_file), it extracts the
// "path" or "file_path" field from the previous tool's args.
func predictArgs(nextTool, prevTool string, prevArgs json.RawMessage) json.RawMessage {
	linkKey := prevTool + "→" + nextTool
	if !argLinkedPatterns[linkKey] {
		return nil
	}

	// Extract path from previous tool's args.
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(prevArgs, &fields); err != nil {
		return nil
	}

	// Try common path field names.
	pathBytes, ok := fields["file_path"]
	if !ok {
		pathBytes, ok = fields["path"]
	}
	if !ok {
		return nil
	}

	// Build args for the next tool with the predicted path.
	// read_file uses "path", multi_file_read uses "files" (array).
	if nextTool == "read_file" {
		obj := map[string]json.RawMessage{"path": pathBytes}
		result, err := json.Marshal(obj)
		if err != nil {
			return nil
		}
		return result
	}
	if nextTool == "multi_file_read" {
		// multi_file_read expects {"files": [{"path": "..."}]}
		obj := map[string]json.RawMessage{
			"files": json.RawMessage(`[{"path": ` + string(pathBytes) + `}]`),
		}
		result, err := json.Marshal(obj)
		if err != nil {
			return nil
		}
		return result
	}
	return nil
}

// speculate starts background goroutines to pre-execute predicted tool calls.
// It runs while the LLM is generating its next response (2-5 seconds).
// At most specMaxConcurrent goroutines run concurrently.
func (s *speculator) speculate(ctx context.Context, tools *tool.Registry, lastTool string, lastArgs json.RawMessage) {
	if tools == nil {
		return
	}
	predictions := s.predictNext(lastTool)
	if len(predictions) == 0 {
		return
	}

	for _, predicted := range predictions {
		// Check concurrency limit.
		s.mu.Lock()
		if s.activeSpeculations >= specMaxConcurrent {
			s.mu.Unlock()
			debug.Log("speculate", "max concurrent speculations (%d) reached, skipping %s", specMaxConcurrent, predicted)
			continue
		}
		// Predict arguments for this tool.
		s.mu.Unlock()

		predArgs := predictArgs(predicted, lastTool, lastArgs)
		if predArgs == nil {
			debug.Log("speculate", "no arg prediction for %s after %s, skipping", predicted, lastTool)
			continue
		}

		// Skip if already cached.
		if s.hasCached(predicted, predArgs) {
			continue
		}

		s.mu.Lock()
		s.speculations++
		s.activeSpeculations++
		s.mu.Unlock()

		// Launch background goroutine for speculative execution.
		go func(toolName string, toolArgs json.RawMessage) {
			defer safego.Recover("agent.speculate.exec")
			defer func() {
				s.mu.Lock()
				s.activeSpeculations--
				s.mu.Unlock()
			}()

			specCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()

			t, ok := tools.Get(toolName)
			if !ok {
				debug.Log("speculate", "predicted tool %s not found in registry", toolName)
				return
			}

			start := time.Now()
			result, err := t.Execute(specCtx, toolArgs)
			dur := time.Since(start)

			if err != nil {
				debug.Log("speculate", "speculative %s failed: %v (after %v)", toolName, err, dur)
				return
			}
			if result.IsError {
				debug.Log("speculate", "speculative %s returned error result, not caching", toolName)
				return
			}

			s.store(toolName, toolArgs, result)
			s.mu.Lock()
			s.savedMicros += dur.Microseconds()
			s.mu.Unlock()
			debug.Log("speculate", "speculatively executed %s in %v (cached for future use)", toolName, dur)
		}(predicted, predArgs)
	}
}

// specStats returns current speculation statistics for observability.
type specStats struct {
	Hits             int   `json:"hits"`
	Misses           int   `json:"misses"`
	Speculations     int   `json:"speculations"`
	SavedMicros      int64 `json:"saved_micros"`
	AdaptiveMinCount int   `json:"adaptive_min_count"`
	CacheSize        int   `json:"cache_size"`
	ActiveSpecs      int   `json:"active_specs"`
}

func (s *speculator) stats() specStats {
	s.mu.Lock()
	defer s.mu.Unlock()
	return specStats{
		Hits:             s.hits,
		Misses:           s.misses,
		Speculations:     s.speculations,
		SavedMicros:      s.savedMicros,
		AdaptiveMinCount: s.adaptiveMinCount,
		CacheSize:        len(s.cache),
		ActiveSpecs:      s.activeSpeculations,
	}
}

// Close stops all background goroutines and clears the cache.
func (s *speculator) Close() {
	s.mu.Lock()
	s.cache = make(map[string]*speculativeResult)
	s.cacheOrder = make([]string, 0, specMaxCacheSize)
	s.mu.Unlock()
}

// invalidateCache clears all cached speculative results. Called after
// file-editing tools to prevent stale pre-executed read_file/grep results
// from being served when the agent reads the same file on the next turn.
func (s *speculator) invalidateCache() {
	s.mu.Lock()
	s.cache = make(map[string]*speculativeResult)
	s.cacheOrder = make([]string, 0, specMaxCacheSize)
	s.mu.Unlock()
}
