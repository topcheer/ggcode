package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/permission"
	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/safego"
	"github.com/topcheer/ggcode/internal/tool"
)

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

var argLinkedPatterns = map[string]bool{
	"edit_file→read_file":             true,
	"edit_file→multi_file_read":       true,
	"multi_edit_file→read_file":       true,
	"multi_edit_file→multi_file_read": true,
	"write_file→read_file":            true,
	"write_file→multi_file_read":      true,

	"edit_file→lsp_diagnostics":       true,
	"multi_edit_file→lsp_diagnostics": true,
	"write_file→lsp_diagnostics":      true,
	"edit_file→lsp_symbols":           true,
	"multi_edit_file→lsp_symbols":     true,
	"write_file→lsp_symbols":          true,
}

var noArgTemplates = map[string]json.RawMessage{
	"git_status":      json.RawMessage(`{}`),
	"git_branch_list": json.RawMessage(`{}`),
	"git_log":         json.RawMessage(`{}`),
}

type speculator struct {
	mu sync.Mutex

	patterns map[string]map[string]int

	lastTool string

	cache map[string]*speculativeResult

	cacheOrder []string

	hits         int
	misses       int
	speculations int
	savedMicros  int64

	adaptiveMinCount int

	activeSpeculations int

	ttl time.Duration
}

const (
	specMaxCacheSize = 50

	specMaxConcurrent = 3

	specAdaptiveFloor   = 1
	specAdaptiveCeiling = 5

	specAdaptiveWindow = 20
)

type speculativeResult struct {
	result     tool.Result
	cachedAt   time.Time
	freshPath  string
	freshMtime int64
	freshSize  int64
}

func statSnapshotFor(toolName string, args json.RawMessage) (path string, mtime, size int64) {
	p := extractFilePathFromArgs(toolName, args)
	if p == "" {
		return "", 0, 0
	}
	st, err := os.Stat(p)
	if err != nil {

		return p, -1, -1
	}
	return p, st.ModTime().UnixNano(), st.Size()
}

func (r *speculativeResult) freshStill() bool {
	if r.freshPath == "" {
		return true
	}
	st, err := os.Stat(r.freshPath)
	if err != nil {
		return r.freshMtime == -1
	}
	if r.freshMtime == -1 {
		return false
	}
	return st.ModTime().UnixNano() == r.freshMtime && st.Size() == r.freshSize
}

func newSpeculator() *speculator {
	s := &speculator{
		patterns:         make(map[string]map[string]int),
		cache:            make(map[string]*speculativeResult),
		cacheOrder:       make([]string, 0, specMaxCacheSize),
		ttl:              30 * time.Second,
		adaptiveMinCount: 2,
	}

	mergePersistedPatterns(s)
	return s
}

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

func (s *speculator) resetSequence() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastTool = ""
}

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

func cacheKey(toolName string, args json.RawMessage) string {

	if _, ok := noArgTemplates[toolName]; ok {
		args = stripDescriptionArg(args)
	}
	h := sha256.Sum256(append([]byte(toolName+":"), args...))
	return hex.EncodeToString(h[:8])
}

func stripDescriptionArg(args json.RawMessage) json.RawMessage {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(args, &fields); err != nil || len(fields) == 0 {
		return args
	}
	if _, has := fields["description"]; !has {
		return args
	}
	delete(fields, "description")
	b, err := json.Marshal(fields)
	if err != nil {
		return args
	}
	return b
}

func (s *speculator) getCached(toolName string, args json.RawMessage) (tool.Result, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := cacheKey(toolName, args)

	countable := speculativeSafeTools[toolName]

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

func (s *speculator) store(toolName string, args json.RawMessage, result tool.Result, freshPath string, freshMtime, freshSize int64) {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := cacheKey(toolName, args)

	if _, exists := s.cache[key]; exists {
		s.removeFromCacheOrder(key)
	} else if len(s.cache) >= specMaxCacheSize {

		if len(s.cacheOrder) > 0 {
			oldest := s.cacheOrder[0]
			delete(s.cache, oldest)
			s.cacheOrder = s.cacheOrder[1:]
			debug.Log("speculate", "cache full, evicted oldest entry (key=%s)", oldest)
		}
	}

	s.cache[key] = &speculativeResult{
		result:     result,
		cachedAt:   time.Now(),
		freshPath:  freshPath,
		freshMtime: freshMtime,
		freshSize:  freshSize,
	}
	s.cacheOrder = append(s.cacheOrder, key)
}

func (s *speculator) removeFromCacheOrder(key string) {
	for i, k := range s.cacheOrder {
		if k == key {
			s.cacheOrder = append(s.cacheOrder[:i], s.cacheOrder[i+1:]...)
			return
		}
	}
}

func (s *speculator) hasCached(toolName string, args json.RawMessage) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := cacheKey(toolName, args)
	cached, ok := s.cache[key]
	if !ok {
		return false
	}

	return time.Since(cached.cachedAt) <= s.ttl && cached.freshStill()
}

func predictArgs(nextTool, prevTool string, prevArgs json.RawMessage) json.RawMessage {

	if tmpl, ok := noArgTemplates[nextTool]; ok {
		return tmpl
	}

	linkKey := prevTool + "→" + nextTool
	if !argLinkedPatterns[linkKey] {
		return nil
	}

	var fields map[string]json.RawMessage
	if err := json.Unmarshal(prevArgs, &fields); err != nil {
		return nil
	}

	pathBytes, ok := fields["file_path"]
	if !ok {
		pathBytes, ok = fields["path"]
	}
	if !ok {
		return nil
	}

	if nextTool == "read_file" {
		obj := map[string]json.RawMessage{"path": pathBytes}
		result, err := json.Marshal(obj)
		if err != nil {
			return nil
		}
		return result
	}
	if nextTool == "multi_file_read" {

		obj := map[string]json.RawMessage{
			"files": json.RawMessage(`[{"path": ` + string(pathBytes) + `}]`),
		}
		result, err := json.Marshal(obj)
		if err != nil {
			return nil
		}
		return result
	}

	if nextTool == "lsp_diagnostics" || nextTool == "lsp_symbols" {
		obj := map[string]json.RawMessage{"path": pathBytes}
		result, err := json.Marshal(obj)
		if err != nil {
			return nil
		}
		return result
	}
	return nil
}

func (s *speculator) speculate(ctx context.Context, tools *tool.Registry, lastTool string, lastArgs json.RawMessage) {
	if tools == nil {
		return
	}
	predictions := s.predictNext(lastTool)
	if len(predictions) == 0 {
		return
	}

	for _, predicted := range predictions {

		if !s.reserveSpecSlot() {
			debug.Log("speculate", "max concurrent speculations (%d) reached, skipping %s", specMaxConcurrent, predicted)
			continue
		}

		predArgs := predictArgs(predicted, lastTool, lastArgs)
		if predArgs == nil {
			s.releaseSpecSlot()
			debug.Log("speculate", "no arg prediction for %s after %s, skipping", predicted, lastTool)
			continue
		}

		if s.hasCached(predicted, predArgs) {
			s.releaseSpecSlot()
			continue
		}

		s.mu.Lock()
		s.speculations++
		s.mu.Unlock()

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

			fp, fm, fs := statSnapshotFor(toolName, toolArgs)

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

			s.store(toolName, toolArgs, result, fp, fm, fs)
			s.mu.Lock()
			s.savedMicros += dur.Microseconds()
			s.mu.Unlock()
			debug.Log("speculate", "speculatively executed %s in %v (cached for future use)", toolName, dur)
		}(predicted, predArgs)
	}
}

func (s *speculator) reserveSpecSlot() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.activeSpeculations >= specMaxConcurrent {
		return false
	}
	s.activeSpeculations++
	return true
}

func (s *speculator) releaseSpecSlot() {
	s.mu.Lock()
	s.activeSpeculations--
	s.mu.Unlock()
}

func (s *speculator) Close() {
	s.mu.Lock()
	s.cache = make(map[string]*speculativeResult)
	s.cacheOrder = make([]string, 0, specMaxCacheSize)
	s.mu.Unlock()
}

func (s *speculator) invalidateCache() {
	s.mu.Lock()
	s.cache = make(map[string]*speculativeResult)
	s.cacheOrder = make([]string, 0, specMaxCacheSize)
	s.mu.Unlock()
}

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
