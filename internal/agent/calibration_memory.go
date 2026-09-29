package agent

// Calibration Memory -- WorldEvolver Semantic Memory (arXiv:2606.30639)
//
// Research basis:
//   - WorldEvolver (arXiv:2606.30639, EMNLP 2026 Findings): "Self-Evolving
//     World Models for LLM Agent Planning." Its core contribution is not
//     prediction but deployment-time memory revision: prediction-observation
//     mismatches are distilled into persistent heuristic rules (Semantic
//     Memory) that revise the agent's context across episodes.
//
// ggcode already has the DETECTION half: foresight_calibrate.go scans
// assistant text for outcome predictions and counts mismatches against tool
// results within a run. But that signal dies with the run (reset()). The
// missing half is the Semantic Memory: persist each mismatch as a compact
// calibration lesson keyed by (kind, tool), merge repeats, and recall the
// strongest lessons at the start of FUTURE runs so the same wrong world-model
// assumption is corrected before the first tool call, not after the third
// failure.
//
// Distinctions from adjacent mechanisms:
//   - internal/memory/experience.go stores TASK-level cases (task/approach/
//     outcome) written at run end by reflection; this store is TOOL-level
//     calibration lessons written automatically at mismatch time.
//   - Tool Effectiveness Tracker (rejected) aggregates tool usage stats;
//     this persists prediction-vs-observation polarity failures only.
//
// Storage: <workingDir>/.ggcode/memory/calibration.json, atomic tmp+rename
// writes (last writer wins under concurrent runs in the same workspace).
// Zero LLM cost; capped at calibrationStoreMaxLessons entries (LRU by
// last_seen, RFC3339 UTC strings sort lexicographically = chronologically).

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	// calibrationStoreMaxLessons bounds the persisted set; beyond this the
	// least-recently-seen lesson is evicted.
	calibrationStoreMaxLessons = 64

	// calibrationRecallTopK matches experienceRecallTopK: a handful of
	// lessons per run keeps the injection block small enough to ride the
	// prompt-cache stable prefix.
	calibrationRecallTopK = 3

	// calibrationSnippetMax caps the stored prediction snippet.
	calibrationSnippetMax = 160
)

// Calibration lesson kinds -- the three mismatch polarities detected by
// foresight_calibrate.go.
const (
	calibKindPredSuccessGotFailure = "predicted_success_got_failure"
	calibKindPredContentGotEmpty   = "predicted_content_got_empty"
	calibKindPredFailureGotSuccess = "predicted_failure_got_success"
)

type calibrationLesson struct {
	Key      string `json:"key"`
	Kind     string `json:"kind"`
	Tool     string `json:"tool"`
	Snippet  string `json:"snippet"`
	Count    int    `json:"count"`
	LastSeen string `json:"last_seen"` // RFC3339 UTC
}

type calibrationStore struct {
	mu      sync.Mutex
	path    string
	lessons map[string]*calibrationLesson
}

// newCalibrationStore loads (or initializes) the persistent calibration
// lesson store for a workspace. Returns nil when workingDir is empty
// (caller skips recording/recall entirely).
func newCalibrationStore(workingDir string) *calibrationStore {
	if workingDir == "" {
		return nil
	}
	cs := &calibrationStore{
		path:    filepath.Join(workingDir, ".ggcode", "memory", "calibration.json"),
		lessons: make(map[string]*calibrationLesson),
	}
	cs.load()
	return cs
}

// load is best-effort: a missing or corrupt file yields an empty store.
func (cs *calibrationStore) load() {
	data, err := os.ReadFile(cs.path)
	if err != nil {
		return
	}
	var lessons []*calibrationLesson
	if err := json.Unmarshal(data, &lessons); err != nil {
		return
	}
	for _, l := range lessons {
		if l == nil || l.Key == "" {
			continue
		}
		cs.lessons[l.Key] = l
	}
}

// record merges a mismatch observation into the store and persists it.
// Safe to call with a nil receiver (disabled store).
func (cs *calibrationStore) record(kind, tool, snippet string) {
	if cs == nil || kind == "" || tool == "" {
		return
	}
	snippet = strings.TrimSpace(snippet)
	if len(snippet) > calibrationSnippetMax {
		snippet = snippet[:calibrationSnippetMax-3] + "..."
	}
	cs.mu.Lock()
	defer cs.mu.Unlock()
	key := kind + "|" + tool
	l, ok := cs.lessons[key]
	if !ok {
		if len(cs.lessons) >= calibrationStoreMaxLessons {
			cs.evictLocked()
		}
		l = &calibrationLesson{Key: key, Kind: kind, Tool: tool}
		cs.lessons[key] = l
	}
	l.Count++
	l.Snippet = snippet
	l.LastSeen = time.Now().UTC().Format(time.RFC3339)
	cs.saveLocked()
}

// evictLocked removes the least-recently-seen lesson. Caller holds mu.
func (cs *calibrationStore) evictLocked() {
	var oldestKey string
	var oldestSeen string
	first := true
	for k, l := range cs.lessons {
		if first || l.LastSeen < oldestSeen {
			oldestKey = k
			oldestSeen = l.LastSeen
			first = false
		}
	}
	if oldestKey != "" {
		delete(cs.lessons, oldestKey)
	}
}

// saveLocked writes the store atomically (tmp + rename). Caller holds mu.
// Best-effort: write failures are ignored -- calibration memory must never
// break the tool loop.
func (cs *calibrationStore) saveLocked() {
	lessons := make([]*calibrationLesson, 0, len(cs.lessons))
	for _, l := range cs.lessons {
		lessons = append(lessons, l)
	}
	data, err := json.MarshalIndent(lessons, "", "  ")
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(cs.path), 0o700); err != nil {
		return
	}
	tmp := cs.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return
	}
	_ = os.Rename(tmp, cs.path)
}

// recall renders the top-K strongest lessons as a compact injection block,
// or "" when nothing is recorded (callers skip injection). Ranking is by
// mismatch count desc, then recency desc.
func (cs *calibrationStore) recall(topK int) string {
	if cs == nil || len(cs.lessons) == 0 {
		return ""
	}
	cs.mu.Lock()
	lessons := make([]*calibrationLesson, 0, len(cs.lessons))
	for _, l := range cs.lessons {
		lessons = append(lessons, l)
	}
	cs.mu.Unlock()
	sort.Slice(lessons, func(i, j int) bool {
		if lessons[i].Count != lessons[j].Count {
			return lessons[i].Count > lessons[j].Count
		}
		return lessons[i].LastSeen > lessons[j].LastSeen
	})
	if topK > 0 && len(lessons) > topK {
		lessons = lessons[:topK]
	}
	var b strings.Builder
	b.WriteString("- ")
	for i, l := range lessons {
		if i > 0 {
			b.WriteString("- ")
		}
		fmt.Fprintf(&b, "tool %q: predictions were wrong %d time(s) in past runs (%s)", l.Tool, l.Count, l.Kind)
		if l.Snippet != "" {
			fmt.Fprintf(&b, "; latest: %q", l.Snippet)
		}
		b.WriteString(" -- verify actual state before relying on similar predictions.\n")
	}
	return b.String()
}

// recallCalibration is the run-start injection hook (WorldEvolver semantic
// memory recall). Empty string means nothing to inject.
func (a *Agent) recallCalibration() string {
	if a == nil || a.calibration == nil {
		return ""
	}
	return a.calibration.recall(calibrationRecallTopK)
}
