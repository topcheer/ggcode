package agent

// Post-Run Trajectory Intelligence Extractor
//
// Research basis:
//   - Fang et al. "Trajectory-Informed Memory Generation for Self-Improving
//     Agent Systems." arXiv:2603.10600 (Mar 2026).
//     Proposes automatic extraction of actionable learnings from agent
//     execution trajectories: (1) strategy tips from successful patterns,
//     (2) recovery tips from failure handling, (3) optimization tips from
//     inefficient-but-successful executions. Up to 14.3pp improvement on
//     AppWorld benchmark, 28.5pp on complex tasks.
//   - Xie et al. "Statistical Early Stopping for Reasoning Models."
//     arXiv:2602.13935 (Feb 2026).
//     Uncertainty signals accumulate predictably; detecting inefficiency
//     patterns after-the-fact helps calibrate future runs.
//
// Problem: ggcode has 60+ during-run detectors that fire in real time, but
// when a run ends, the trajectory data (tool patterns, error sequences,
// iteration counts, context usage) is discarded. The agent completes many
// tasks successfully but repeats inefficient patterns: too many exploration
// iterations before acting, redundant reads, excessive retries. No learnings
// are extracted and persisted for future runs.
//
// Gap: No post-run trajectory analysis extracts structured insights that
// could improve future performance. Each run is an island — successful
// strategies aren't reinforced, recovery patterns aren't generalized, and
// inefficiencies aren't flagged for avoidance.
//
// Design:
//   - Called in the post-run defer block (non-blocking, error-safe)
//   - Analyzes RunStats to classify the run and extract insights
//   - Three learning types (matching the paper):
//     1. Strategy tips: what efficient patterns led to success
//     2. Recovery tips: how errors were encountered and resolved
//     3. Optimization tips: inefficiencies in otherwise successful runs
//   - Persists to .ggcode/trajectory-learnings.jsonl (append-only)
//   - Capped at most recent 50 entries to bound file size
//   - Zero LLM cost — pure deterministic analysis of run statistics
//   - Skipped for trivial runs (< 3 iterations) to avoid noise

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
)

const (
	// trajIntelMinIterations: skip extraction for trivially short runs.
	trajIntelMinIterations = 3

	// trajIntelMaxEntries: max persisted learning entries (rolling window).
	trajIntelMaxEntries = 50

	// trajIntelHighIterationThreshold: runs exceeding this many iterations
	// for few edits suggest over-exploration.
	trajIntelHighIterationThreshold = 15

	// trajIntelLowEditThreshold: below this many edits, a high-iteration
	// run is flagged as exploration-heavy.
	trajIntelLowEditThreshold = 2

	// trajIntelHighRetryThreshold: tool error ratio above this signals
	// significant retry overhead.
	trajIntelHighRetryRatio = 0.25

	// trajIntelHighContextRatio: using >80% of context window suggests
	// the run nearly ran out of context.
	trajIntelHighContextRatio = 0.80

	// trajIntelHighCompaction: multiple compactions indicate context pressure.
	trajIntelHighCompaction = 2
)

// trajectoryLearning represents one extracted insight from a completed run.
type trajectoryLearning struct {
	Timestamp time.Time      `json:"timestamp"`
	Type      string         `json:"type"` // "strategy", "recovery", "optimization"
	Task      string         `json:"task"` // first 120 chars of user prompt
	Success   bool           `json:"success"`
	Insight   string         `json:"insight"`
	Metrics   map[string]int `json:"metrics,omitempty"`
	Category  string         `json:"category"` // coarse classification
}

// trajIntelState manages post-run trajectory intelligence extraction.
type trajIntelState struct {
	mu        sync.Mutex
	learnings []trajectoryLearning // in-memory cache
	filePath  string               // persistence path
	loaded    bool
}

func newTrajIntelState() *trajIntelState {
	return &trajIntelState{}
}

// extractTrajectoryInsights analyzes a completed run and produces zero or
// more structured learnings. This is the core intelligence — it classifies
// the run pattern and extracts actionable guidance.
func (s *trajIntelState) extractInsights(stats *RunStats) []trajectoryLearning {
	if stats == nil || stats.Iterations < trajIntelMinIterations {
		return nil
	}

	task := truncateTask(stats.UserPrompt, 120)
	now := time.Now()
	totalToolCalls := totalToolCallCount(stats.ToolCalls)
	failedCalls := len(stats.Errors)
	errorRatio := 0.0
	if totalToolCalls > 0 {
		errorRatio = float64(failedCalls) / float64(totalToolCalls)
	}
	editCount := len(stats.FilesEdited)
	contextRatio := 0.0
	if stats.ContextWindow > 0 {
		contextRatio = float64(stats.ContextPeakTokens) / float64(stats.ContextWindow)
	}

	metrics := map[string]int{
		"iterations":  stats.Iterations,
		"tool_calls":  totalToolCalls,
		"edits":       editCount,
		"errors":      failedCalls,
		"compactions": stats.CompactionCount,
	}

	var learnings []trajectoryLearning

	// --- Strategy tips (successful efficient patterns) ---
	if stats.Success && stats.Iterations <= 8 && editCount > 0 && errorRatio < 0.1 {
		topTools := topToolNames(stats.ToolCalls, 3)
		learnings = append(learnings, trajectoryLearning{
			Timestamp: now,
			Type:      "strategy",
			Task:      task,
			Success:   true,
			Insight:   fmt.Sprintf("Efficient completion in %d iterations with %d edits. Key tools: %s.", stats.Iterations, editCount, strings.Join(topTools, ", ")),
			Metrics:   metrics,
			Category:  "efficient-completion",
		})
	}

	// --- Recovery tips (errors encountered and resolved) ---
	if stats.Success && failedCalls > 0 {
		errSummary := summarizeErrors(stats.Errors)
		learnings = append(learnings, trajectoryLearning{
			Timestamp: now,
			Type:      "recovery",
			Task:      task,
			Success:   true,
			Insight:   fmt.Sprintf("Recovered from %d tool error(s) and completed successfully. Error patterns: %s", failedCalls, errSummary),
			Metrics:   metrics,
			Category:  "error-recovery",
		})
	}

	// --- Optimization tips (inefficient-but-successful or failed runs) ---

	// Over-exploration: many iterations with few edits.
	if stats.Iterations >= trajIntelHighIterationThreshold && editCount <= trajIntelLowEditThreshold {
		readHeavy := isReadHeavy(stats.ToolCalls)
		verb := "exploration-heavy"
		if readHeavy {
			verb = "read-heavy exploration"
		}
		status := "completed"
		if !stats.Success {
			status = "failed"
		}
		learnings = append(learnings, trajectoryLearning{
			Timestamp: now,
			Type:      "optimization",
			Task:      task,
			Success:   stats.Success,
			Insight:   fmt.Sprintf("Run %s with %d iterations but only %d edits - %s pattern. Future similar tasks should act sooner after initial exploration.", status, stats.Iterations, editCount, verb),
			Metrics:   metrics,
			Category:  "over-exploration",
		})
	}

	// High error ratio: significant retry overhead.
	if errorRatio >= trajIntelHighRetryRatio && totalToolCalls >= 5 {
		status := "completed despite"
		if !stats.Success {
			status = "failed due to"
		}
		learnings = append(learnings, trajectoryLearning{
			Timestamp: now,
			Type:      "optimization",
			Task:      task,
			Success:   stats.Success,
			Insight:   fmt.Sprintf("Run %s %.0f%% tool error rate (%d/%d calls). Common error: %s. Consider pre-validating tool arguments.", status, errorRatio*100, failedCalls, totalToolCalls, summarizeErrors(stats.Errors)),
			Metrics:   metrics,
			Category:  "high-error-rate",
		})
	}

	// Context pressure: nearly exhausted context window.
	if contextRatio >= trajIntelHighContextRatio || stats.CompactionCount >= trajIntelHighCompaction {
		learnings = append(learnings, trajectoryLearning{
			Timestamp: now,
			Type:      "optimization",
			Task:      task,
			Success:   stats.Success,
			Insight:   fmt.Sprintf("Context pressure: peaked at %.0f%% of window, %d compaction(s). Future tasks should use more targeted reads.", contextRatio*100, stats.CompactionCount),
			Metrics:   metrics,
			Category:  "context-pressure",
		})
	}

	// Failed run with no edits: couldn't make progress.
	if !stats.Success && editCount == 0 && stats.Iterations >= trajIntelMinIterations {
		learnings = append(learnings, trajectoryLearning{
			Timestamp: now,
			Type:      "optimization",
			Task:      task,
			Success:   false,
			Insight:   fmt.Sprintf("Run failed after %d iterations with no file modifications. Likely blocked by: %s", stats.Iterations, summarizeErrors(stats.Errors)),
			Metrics:   metrics,
			Category:  "no-progress-failure",
		})
	}

	return learnings
}

// maybeExtractAndPersist runs extraction and persists results.
// Called from the post-run defer block. Must never panic or block.
func (s *trajIntelState) maybeExtractAndPersist(workingDir string, stats *RunStats) {
	learnings := s.extractInsights(stats)
	if len(learnings) == 0 {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Ensure file path is set.
	if s.filePath == "" && workingDir != "" {
		s.filePath = filepath.Join(workingDir, ".ggcode", "trajectory-learnings.jsonl")
	}

	// Append to in-memory cache.
	s.learnings = append(s.learnings, learnings...)

	// Persist to JSONL file (append mode).
	if s.filePath != "" {
		if err := s.persistLocked(); err != nil {
			debug.Log("traj-intel", "failed to persist learnings: %v", err)
		}
	}
}

// persistLocked appends new learnings to the JSONL file and trims to max entries.
// Caller must hold s.mu.
func (s *trajIntelState) persistLocked() error {
	dir := filepath.Dir(s.filePath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}

	// Read existing entries to maintain rolling window.
	existing, loadErr := s.loadFromFile()
	if loadErr != nil && !os.IsNotExist(loadErr) {
		return fmt.Errorf("load: %w", loadErr)
	}

	all := append(existing, s.learnings...)

	// Trim to most recent N entries.
	if len(all) > trajIntelMaxEntries {
		all = all[len(all)-trajIntelMaxEntries:]
	}

	// Write atomically.
	tmpPath := s.filePath + ".tmp"
	f, err := os.Create(tmpPath)
	if err != nil {
		return fmt.Errorf("create tmp: %w", err)
	}
	enc := json.NewEncoder(f)
	for _, l := range all {
		if encErr := enc.Encode(l); encErr != nil {
			f.Close()
			os.Remove(tmpPath)
			return fmt.Errorf("encode: %w", encErr)
		}
	}
	if closeErr := f.Close(); closeErr != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("close: %w", closeErr)
	}
	if renameErr := os.Rename(tmpPath, s.filePath); renameErr != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("rename: %w", renameErr)
	}

	// Clear the pending buffer after successful write.
	s.learnings = nil
	s.loaded = true
	return nil
}

// loadFromFile reads existing learnings from the JSONL file.
func (s *trajIntelState) loadFromFile() ([]trajectoryLearning, error) {
	data, err := os.ReadFile(s.filePath)
	if err != nil {
		return nil, err
	}
	var result []trajectoryLearning
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var l trajectoryLearning
		if err := json.Unmarshal([]byte(line), &l); err != nil {
			continue // skip malformed lines
		}
		result = append(result, l)
	}
	return result, nil
}

// --- Helpers ---

func truncateTask(s string, max int) string {
	s = strings.TrimSpace(s)
	if len(s) <= max {
		return s
	}
	return s[:max-3] + "..."
}

func totalToolCallCount(m map[string]int) int {
	total := 0
	for _, v := range m {
		total += v
	}
	return total
}

func topToolNames(m map[string]int, n int) []string {
	type entry struct {
		name  string
		count int
	}
	var entries []entry
	for name, count := range m {
		entries = append(entries, entry{name, count})
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].count > entries[j].count
	})
	if len(entries) > n {
		entries = entries[:n]
	}
	result := make([]string, len(entries))
	for i, e := range entries {
		result[i] = e.name
	}
	return result
}

func isReadHeavy(m map[string]int) bool {
	readTools := map[string]bool{
		"read_file": true, "grep": true, "search_files": true,
		"glob": true, "list_directory": true, "code_search": true,
		"lsp_hover": true, "lsp_definition": true, "lsp_references": true,
		"lsp_symbols": true, "web_search": true, "web_fetch": true,
	}
	readCount, totalCount := 0, 0
	for name, count := range m {
		totalCount += count
		if readTools[name] {
			readCount += count
		}
	}
	if totalCount == 0 {
		return false
	}
	return float64(readCount)/float64(totalCount) > 0.5
}

func summarizeErrors(errors []string) string {
	if len(errors) == 0 {
		return "none"
	}
	// Take the first error, truncated.
	first := errors[0]
	if len(first) > 100 {
		first = first[:100] + "..."
	}
	if len(errors) == 1 {
		return first
	}
	return fmt.Sprintf("%s (+%d more)", first, len(errors)-1)
}
