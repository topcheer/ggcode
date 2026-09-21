package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/permission"
	"github.com/topcheer/ggcode/internal/provider"
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
	"read_file":               true,
	"multi_file_read":         true,
	"grep":                    true,
	"glob":                    true,
	"search_files":            true,
	"list_directory":          true,
	"lsp_hover":               true,
	"lsp_definition":          true,
	"lsp_references":          true,
	"lsp_symbols":             true,
	"lsp_workspace_symbols":   true,
	"lsp_diagnostics":         true,
	"lsp_implementation":      true,
	"lsp_incoming_calls":      true,
	"lsp_outgoing_calls":      true,
	"lsp_document_highlights": true,
	"git_status":              true,
	"git_diff":                true,
	"git_log":                 true,
	"git_branch_list":         true,
	"git_show":                true,
	"git_blame":               true,
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

// outputFlowSourceTools are read-only tools whose OUTPUT (not input args)
// carries file paths the model will likely read next: the top match of a
// grep, the first hit of a code_search, a glob result, a directory listing
// entry, an LSP reference target. This is the parameter-dependency half of
// AutoTool's Tool Inertia Graph (AAAI 2026, arXiv:2511.14650): the bigram
// model captures SEQUENCE inertia (grep → read_file), while data-flow edges
// capture WHERE the read target comes from (the previous tool's output).
// Without them, search→read (the most frequent agent sequence) can never
// be speculated, because predictArgs can only inherit paths from the
// previous tool's INPUT arguments (edit_file→read_file style).
var outputFlowSourceTools = map[string]bool{
	"grep":           true,
	"code_search":    true,
	"search_files":   true,
	"glob":           true,
	"list_directory": true,
	"lsp_references": true,
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

	// Redacted output of the last successful outputFlowSourceTools call,
	// from which data-flow read_file targets are extracted. Empty for
	// non-source tools and failed calls. Truncated to specOutputFlowOutputCap.
	lastOutput string

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
	// #1831 cases 1+2: freshness snapshot of the primary file behind the
	// speculated read, taken at speculation time. The TTL alone let an
	// external writer's change (shared workspace, editor autosave) be
	// served as a fresh read for up to 30s - and worse, agent.go then
	// memoized the stale content with a CURRENT stat, making it
	// effectively immortal (the memo's mtime self-heal could never fire
	// because the recorded mtime always matched the already-changed disk).
	// On hit, the snapshot is re-verified: any mtime/size drift is a miss.
	// Empty for tools with no single-file primary arg (TTL-only, as before).
	freshPath  string
	freshMtime int64 // unix nanos
	freshSize  int64
}

// statSnapshotFor returns the freshness snapshot for a speculated call, or
// zero values when the tool has no single-file primary argument (#1831).
func statSnapshotFor(toolName string, args json.RawMessage) (path string, mtime, size int64) {
	p := extractFilePathFromArgs(toolName, args)
	if p == "" {
		return "", 0, 0
	}
	st, err := os.Stat(p)
	if err != nil {
		// Unstatable now: record the path with impossible sentinel values so
		// any later successful stat (file appeared / became readable) reads
		// as drift and forces a fresh execution.
		return p, -1, -1
	}
	return p, st.ModTime().UnixNano(), st.Size()
}

// freshStill holds for a snapshot when the underlying file is unchanged.
func (r *speculativeResult) freshStill() bool {
	if r.freshPath == "" {
		return true // no snapshot: TTL-only (previous behavior)
	}
	st, err := os.Stat(r.freshPath)
	if err != nil {
		return r.freshMtime == -1 // was already absent and still is
	}
	if r.freshMtime == -1 {
		return false // appeared since speculation: drift
	}
	return st.ModTime().UnixNano() == r.freshMtime && st.Size() == r.freshSize
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

// recordOutput captures the (already secret-redacted) output of the last
// tool call so data-flow speculation can derive read_file targets from it
// when the next prediction pass runs. Call it immediately after
// recordObservation for the same tool call. Non-source tools and failed
// calls clear the stored output: data-flow edges only originate from
// successful search-shaped outputs.
func (s *speculator) recordOutput(toolName, output string, isError bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if isError || !outputFlowSourceTools[toolName] {
		s.lastOutput = ""
		return
	}
	if len(output) > specOutputFlowOutputCap {
		output = output[:specOutputFlowOutputCap]
	}
	s.lastOutput = output
}

// resetSequence clears the bigram tracking for a new agent run.
// Called at the start of each RunStreamWithContent to avoid cross-run noise.
func (s *speculator) resetSequence() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastTool = ""
	s.lastOutput = ""
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
	// #1496 C(d): the miss denominator must only count queries from
	// tools that could EVER be speculated. Every tool call - edit_file,
	// run_command, git_commit ... - used to query this cache and inflate
	// misses, structurally driving the hit rate toward zero and pushing
	// the adaptive threshold to its ceiling (most conservative setting)
	// forever.
	countable := speculativeSafeTools[toolName]
	// Non-speculatable tools cannot hit by construction; they skip the
	// denominator entirely.
	cached, ok := s.cache[key]
	if !ok {
		if countable {
			s.misses++
			s.maybeAdaptThreshold()
		}
		return tool.Result{}, false
	}
	if time.Since(cached.cachedAt) > s.ttl {
		s.removeFromCacheOrder(key)
		delete(s.cache, key)
		if countable {
			s.misses++
			s.maybeAdaptThreshold()
		}
		return tool.Result{}, false
	}
	// #1831 cases 1+2: TTL freshness does not cover external writes - a
	// shared-workspace edit lands in milliseconds, not after 30s. Serve the
	// hit only when the stat snapshot still matches the disk.
	if !cached.freshStill() {
		s.removeFromCacheOrder(key)
		delete(s.cache, key)
		if countable {
			s.misses++
			s.maybeAdaptThreshold()
		}
		debug.Log("speculate", "cache HIT rejected for %s: file changed since speculation (key=%s)", toolName, key)
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

	fp, fm, fs := statSnapshotFor(toolName, args)
	s.cache[key] = &speculativeResult{
		result:     result,
		cachedAt:   time.Now(),
		freshPath:  fp,
		freshMtime: fm,
		freshSize:  fs,
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
	// #1831 case 3: same freshness contract as getCached. hasCached gates
	// the PRE-EXECUTION batch (a true skip abandons a would-be-fresh
	// execution); a stale-on-disk entry must not suppress it.
	return time.Since(cached.cachedAt) <= s.ttl && cached.freshStill()
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
		predArgs := predictArgs(predicted, lastTool, lastArgs)
		if predArgs == nil {
			debug.Log("speculate", "no arg prediction for %s after %s, skipping", predicted, lastTool)
			continue
		}

		// Skip if already cached.
		if s.hasCached(predicted, predArgs) {
			continue
		}

		s.launchSpeculation(ctx, tools, predicted, predArgs, "bigram")
	}

	// Data-flow speculation: derive read_file targets from the last search
	// tool's OUTPUT (AutoTool parameter-dependency edges, AAAI 2026).
	s.maybeSpeculateFromOutput(ctx, tools, lastTool)
}

// launchSpeculation starts one background speculative execution, enforcing
// the global concurrency cap. source labels the predictor that produced the
// call ("bigram" or "output-flow") for debug attribution. Returns false if
// the cap was reached and nothing was launched.
func (s *speculator) launchSpeculation(ctx context.Context, tools *tool.Registry, toolName string, toolArgs json.RawMessage, source string) bool {
	s.mu.Lock()
	if s.activeSpeculations >= specMaxConcurrent {
		s.mu.Unlock()
		debug.Log("speculate", "max concurrent speculations (%d) reached, skipping %s", specMaxConcurrent, toolName)
		return false
	}
	s.speculations++
	s.activeSpeculations++
	s.mu.Unlock()

	// Launch background goroutine for speculative execution.
	go func() {
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
		debug.Log("speculate", "speculatively executed %s in %v via %s (cached for future use)", toolName, dur, source)
	}()
	return true
}

// Output-flow speculation tunables.
const (
	// specOutputFlowMaxCandidates caps how many distinct files one search
	// output may cause to be pre-read. The model most often reads the file
	// with the densest matches; two covers the common head of that curve.
	specOutputFlowMaxCandidates = 2

	// specOutputFlowMaxFileSize skips pre-reading huge files on a hunch; a
	// wrong guess must cost bounded I/O, not a 10MB speculative read.
	specOutputFlowMaxFileSize = 512 << 10

	// specOutputFlowOutputCap bounds the remembered search output.
	specOutputFlowOutputCap = 32 << 10

	// specOutputFlowMinBigramEvidence: how many bigram observations of
	// searchTool→read_file are required before data-flow speculation
	// activates for that search tool. One demonstration is enough: the
	// statistical prior says this model reads search results; the data-flow
	// edge then supplies WHERE.
	specOutputFlowMinBigramEvidence = 1

	// specOutputFlowMaxDistinct caps extraction work on pathological outputs.
	specOutputFlowMaxDistinct = 64
)

// maybeSpeculateFromOutput implements the data-flow half of speculation:
// when the last call was a search-shaped tool (grep, code_search, glob, ...),
// its output names the files the model will plausibly read next. Pre-read
// the top-ranked candidates so the model's actual read_file hits the
// speculative cache. A wrong guess is harmless: the cache entry expires via
// TTL, and a model call with different args (offset/limit, other file) is a
// plain miss served by the normal gated execution path.
func (s *speculator) maybeSpeculateFromOutput(ctx context.Context, tools *tool.Registry, lastTool string) {
	if tools == nil || !outputFlowSourceTools[lastTool] {
		return
	}
	s.mu.Lock()
	output := s.lastOutput
	var observedReads int
	if nexts, ok := s.patterns[lastTool]; ok {
		observedReads = nexts["read_file"]
	}
	s.mu.Unlock()
	if output == "" || observedReads < specOutputFlowMinBigramEvidence {
		return
	}

	launched := 0
	for _, c := range extractOutputFlowPaths(output) {
		if launched >= specOutputFlowMaxCandidates {
			break
		}
		// Validate before spending I/O: the path must exist on disk and fit
		// the size cap. Extraction is heuristic, so an invalid candidate is
		// simply skipped.
		if st, err := os.Stat(c.rawPath); err != nil || !st.Mode().IsRegular() || st.Size() > specOutputFlowMaxFileSize {
			continue
		}
		args, err := json.Marshal(map[string]string{"path": c.rawPath})
		if err != nil {
			continue
		}
		if s.hasCached("read_file", args) {
			continue
		}
		if s.launchSpeculation(ctx, tools, "read_file", args, "output-flow") {
			launched++
		}
	}
}

// outputFlowCandidate is one file path extracted from a search tool's output.
type outputFlowCandidate struct {
	rawPath string // exact token as it appeared in the output
	count   int    // occurrences (denser matches → likelier read target)
	order   int    // first-seen position (stable tiebreaker)
}

// outputFlowExts is the extension allowlist for data-flow candidates. A
// dotted source extension is a strong path signal in free-form tool output;
// extension-less names (Makefile, Dockerfile) are intentionally excluded
// because any word could match.
var outputFlowExts = map[string]bool{
	"astro": true, "bash": true, "c": true, "cc": true, "cfg": true,
	"conf": true, "cpp": true, "cs": true, "css": true, "cxx": true,
	"dart": true, "env": true, "erl": true, "ex": true, "go": true,
	"gradle": true, "h": true, "hh": true, "hpp": true, "hs": true,
	"htm": true, "html": true, "ini": true, "java": true, "jl": true,
	"js": true, "json": true, "jsonc": true, "jsx": true, "kt": true,
	"kts": true, "less": true, "lua": true, "m": true, "md": true,
	"mjs": true, "mm": true, "mod": true, "nim": true, "php": true,
	"pl": true, "proto": true, "py": true, "pyi": true, "r": true,
	"rb": true, "rs": true, "sass": true, "scala": true, "scss": true,
	"sh": true, "sol": true, "sql": true, "sum": true, "svelte": true,
	"svg": true, "swift": true, "tf": true, "toml": true, "ts": true,
	"tsx": true, "txt": true, "v": true, "vue": true, "work": true,
	"xml": true, "yaml": true, "yml": true, "zig": true, "zsh": true,
}

// extractOutputFlowPaths pulls file-path candidates out of a search tool's
// output. It understands the common output shapes:
//   - content mode:  "/abs/path/file.go:12:\tline of text"
//   - position mode: "/abs/path/file.go:12:5\tcontext" (lsp_references)
//   - bare mode:     "/abs/path/file.go" (files_with_matches, glob,
//     list_directory)
//
// Ranking: a path appearing more often in the output (denser matches) is the
// likelier read target; ties break by first appearance. Deterministic, zero
// LLM cost, bounded work.
func extractOutputFlowPaths(output string) []outputFlowCandidate {
	if output == "" {
		return nil
	}
	counts := make(map[string]int, 16)
	order := make(map[string]int, 16)
	distinct := 0
	for _, line := range strings.Split(output, "\n") {
		for _, tok := range outputFlowLineTokens(line) {
			path := outputFlowPathLike(tok)
			if path == "" {
				continue
			}
			if _, seen := counts[path]; !seen {
				if distinct >= specOutputFlowMaxDistinct {
					continue
				}
				order[path] = distinct
				distinct++
			}
			counts[path]++
		}
	}
	if len(counts) == 0 {
		return nil
	}
	cands := make([]outputFlowCandidate, 0, len(counts))
	for path, n := range counts {
		cands = append(cands, outputFlowCandidate{rawPath: path, count: n, order: order[path]})
	}
	sort.Slice(cands, func(i, j int) bool {
		if cands[i].count != cands[j].count {
			return cands[i].count > cands[j].count
		}
		return cands[i].order < cands[j].order
	})
	return cands
}

// outputFlowLineTokens yields the path-shaped substrings worth validating on
// one output line: the grep/lsp "path:line" prefix and the leading
// whitespace-delimited field (bare path listings).
func outputFlowLineTokens(line string) []string {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return nil
	}
	toks := make([]string, 0, 2)
	if idx := strings.IndexByte(trimmed, ':'); idx > 0 {
		toks = append(toks, trimmed[:idx])
	}
	if end := strings.IndexAny(trimmed, " \t"); end > 0 {
		toks = append(toks, trimmed[:end])
	} else {
		toks = append(toks, trimmed)
	}
	return toks
}

// outputFlowPathLike applies cheap lexical filters and returns the cleaned
// token, or "" when it cannot be a speculated read target.
func outputFlowPathLike(tok string) string {
	tok = strings.Trim(tok, "\"'`[](){}<>,;")
	for len(tok) > 0 {
		switch tok[len(tok)-1] {
		case ',', ';', ':', '.':
			tok = tok[:len(tok)-1]
			continue
		}
		break
	}
	if len(tok) < 3 || len(tok) > 512 {
		return ""
	}
	if strings.ContainsAny(tok, ":*?\\") {
		return ""
	}
	if strings.HasPrefix(tok, "-") || strings.HasPrefix(tok, "$") || strings.HasPrefix(tok, "#") {
		return ""
	}
	if strings.Contains(tok, "..") {
		return ""
	}
	dot := strings.LastIndexByte(tok, '.')
	if dot < 0 || dot == len(tok)-1 {
		return ""
	}
	if !outputFlowExts[tok[dot+1:]] {
		return ""
	}
	return tok
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

// speculativeHitAllowed reports whether a speculative cache hit may be
// served to the model without re-execution (#1496). The speculative
// goroutine executed the tool with zero permission flow (unlike the sibling
// preExecuted paths), so a deny(read_file X)+allow(edit X) policy was
// bypassed by serving the cached read. Only a clean Allow decision lets the
// hit through; anything else falls back to the normal gated execution path.
func (a *Agent) speculativeHitAllowed(ctx context.Context, tc provider.ToolCallDelta) bool {
	if err := ctx.Err(); err != nil {
		return false
	}
	a.mu.RLock()
	policy := a.policy
	a.mu.RUnlock()
	if policy == nil {
		return true
	}
	decision, err := policy.Check(tc.Name, tc.Arguments)
	if err != nil {
		return false
	}
	return decision == permission.Allow
}
