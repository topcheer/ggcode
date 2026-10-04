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
	"unicode/utf8"

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
	Type      string         `json:"type"` // "strategy", "recovery", "optimization", "teammate"
	Task      string         `json:"task"` // first 120 chars of user prompt
	Success   bool           `json:"success"`
	Insight   string         `json:"insight"`
	Metrics   map[string]int `json:"metrics,omitempty"`
	Category  string         `json:"category"` // coarse classification

	// r459 memory consolidation (Voyager-style success counting + decay).
	// Zero values on legacy entries read as baseline confidence 0.5 via
	// EffectiveConfidence(), so the schema is backward compatible.
	Confidence     float64   `json:"confidence,omitempty"`      // [0,1], reinforcement-updated
	Reinforced     int       `json:"reinforced,omitempty"`      // merge hits (same category+type)
	LastReinforced time.Time `json:"last_reinforced,omitempty"` // last merge time
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
	// #1490-A: uncapped counter - len(Errors) freezes at 10 (prompt cap).
	failedCalls := stats.ErrorCount
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

	unlock, lockErr := lockTrajFile(s.filePath + ".lock")
	if lockErr != nil {
		return fmt.Errorf("lock: %w", lockErr)
	}
	defer unlock()

	// Read existing entries to maintain rolling window.
	existing, loadErr := s.loadFromFile()
	if loadErr != nil && !os.IsNotExist(loadErr) {
		return fmt.Errorf("load: %w", loadErr)
	}

	all := append(existing, s.learnings...)

	// r459 memory consolidation: instead of appending N duplicate rows
	// for a recurring pattern, merge same Category+Type entries into one
	// reinforced row (Voyager-style success counting). A Success=true
	// reinforcement raises confidence; Success=false lowers it (weak
	// arbitration: the insight TEXT keeps the historical winner, but a
	// failing streak erodes its injection priority instead of a silent
	// newest-wins overwrite).
	all = consolidateLearnings(all)

	// Trim to most recent N entries - but evict lowest-confidence first
	// among ties so repeatedly-reinforced old insights outlive one-off
	// noise (pure tail FIFO was the pre-r459 behavior).
	if len(all) > trajIntelMaxEntries {
		sort.SliceStable(all, func(i, j int) bool {
			ci, cj := all[i].EffectiveConfidence(), all[j].EffectiveConfidence()
			if ci != cj {
				return ci > cj
			}
			return all[i].Timestamp.After(all[j].Timestamp)
		})
		all = all[:trajIntelMaxEntries]
		sort.SliceStable(all, func(i, j int) bool { // restore chronological order for the file
			return all[i].Timestamp.Before(all[j].Timestamp)
		})
	}

	// Write atomically.
	// #1512 case C: the whole load→append→rewrite must run under a
	// cross-PROCESS file lock — s.mu is per-Agent-instance, but the file
	// is workspace-shared: two concurrently finishing agents each loaded
	// the same baseline, appended their own learnings, and the LAST
	// rename won, silently erasing the other's entries (a lost update
	// directly against the "accumulated self-improvement" purpose). The
	// tmp name is also unique now: two writers sharing the fixed
	// ".tmp" path interleaved content into one file.
	tmpF, err := os.CreateTemp(filepath.Dir(s.filePath), ".traj-*.tmp")
	if err != nil {
		return fmt.Errorf("create tmp: %w", err)
	}
	tmpPath := tmpF.Name()
	f := tmpF
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

// trajPromptMaxEntries / trajPromptMaxChars bound the injected section:
// past-run learnings are advisory context, never worth crowding out real
// conversation space.
const (
	trajPromptMaxEntries = 8
	trajPromptMaxChars   = 1200
	trajPromptPerType    = 3
)

// RenderPromptSection (r458) renders the persisted learning store as a
// deterministic system-prompt section, closing the distillation loop:
// extract -> persist -> RE-INJECT (the third leg of the Trajectory-
// Informed Memory loop this file implements; until now the store was
// write-only). Rules:
//   - same Category dedupes to the newest entry (recurring patterns
//     must not echo N times)
//   - per Type (strategy/recovery/optimization/teammate) take the
//     trajPromptPerType most recent, overall cap trajPromptMaxEntries
//   - hard char budget with truncation marker
//   - deterministic output (no timestamps) so the section is stable
//     across runs within a session and cache-friendly
func (s *trajIntelState) RenderPromptSection(workingDir string) string {
	if workingDir == "" {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.filePath = filepath.Join(workingDir, ".ggcode", "trajectory-learnings.jsonl")
	entries, err := s.loadFromFile()
	if err != nil {
		entries = nil
	}
	// r460 cross-workspace tier: top up from the user-level global store,
	// categories the workspace has not learned locally (marked so the
	// model can weight local over general). Workspace entries win their
	// categories; global only fills gaps.
	if globalPath, gErr := TrajGlobalPath(); gErr == nil {
		if gEntries, gLoadErr := loadTrajFile(globalPath); gLoadErr == nil && len(gEntries) > 0 {
			localCats := map[string]bool{}
			for _, l := range entries {
				localCats[l.Category] = true
			}
			for _, l := range gEntries {
				if l.Category == "" || localCats[l.Category] {
					continue
				}
				l.Insight = l.Insight + " (general, other projects)"
				entries = append(entries, l)
			}
		}
	}
	if len(entries) == 0 {
		return ""
	}
	// Sort by effective confidence (decayed) then recency; the newest
	// entry alone no longer silences a repeatedly-reinforced older
	// insight. Below-threshold entries never inject (r459).
	sort.SliceStable(entries, func(i, j int) bool {
		ci, cj := entries[i].EffectiveConfidence(), entries[j].EffectiveConfidence()
		if ci != cj {
			return ci > cj
		}
		return entries[i].Timestamp.After(entries[j].Timestamp)
	})
	// Dedupe per Category, keep newest; Type counter caps variety.
	var lines []string
	counts := map[string]int{}
	seenCat := map[string]bool{}
	total := 0
	for _, l := range entries {
		if total >= trajPromptMaxEntries {
			break
		}
		if l.EffectiveConfidence() < trajPromptMinConfidence {
			continue
		}
		key := l.Category
		if key == "" {
			key = l.Type
		}
		if seenCat[key] {
			continue
		}
		if counts[l.Type] >= trajPromptPerType {
			continue
		}
		seenCat[key] = true
		counts[l.Type]++
		lines = append(lines, fmt.Sprintf("- [%s] %s", l.Type, l.Insight))
		total++
	}
	if len(lines) == 0 {
		return ""
	}
	section := "Past-run learnings (auto-extracted from previous sessions - apply where relevant, ignore where not):\n" +
		strings.Join(lines, "\n")
	if len(section) > trajPromptMaxChars {
		// #3257: rune-safe cut + the marker's real byte length inside the
		// budget (the legacy code reserved 20 but appended 34, so every
		// truncated section was actually 1214 > 1200).
		const marker = "\n- [...] older learnings truncated"
		section = truncateRunesUTF8(section, trajPromptMaxChars-len(marker)) + marker
	}
	return section
}

// TrajLearningView is the user-facing projection of one learning (for
// the /traj TUI command). r459: injected learnings were invisible and
// unkillable - a polluted store kept whispering into the system prompt
// with no surface to audit or purge it.
type TrajLearningView struct {
	Type       string
	Category   string
	Insight    string
	Confidence float64
	Reinforced int
	Timestamp  time.Time
	Injects    bool // would RenderPromptSection include it?
}

// TrajListLearnings returns the store projected for display, sorted by
// effective confidence (same order the prompt renderer would pick in).
func TrajListLearnings(workingDir string) []TrajLearningView {
	s := newTrajIntelState()
	s.filePath = filepath.Join(workingDir, ".ggcode", "trajectory-learnings.jsonl")
	s.mu.Lock()
	defer s.mu.Unlock()
	entries, err := s.loadFromFile()
	if err != nil {
		return nil
	}
	views := make([]TrajLearningView, 0, len(entries))
	for _, l := range entries {
		conf := l.EffectiveConfidence()
		views = append(views, TrajLearningView{
			Type: l.Type, Category: l.Category, Insight: l.Insight,
			Confidence: conf, Reinforced: l.Reinforced, Timestamp: l.Timestamp,
			Injects: conf >= trajPromptMinConfidence,
		})
	}
	sort.SliceStable(views, func(i, j int) bool {
		if views[i].Confidence != views[j].Confidence {
			return views[i].Confidence > views[j].Confidence
		}
		return views[i].Timestamp.After(views[j].Timestamp)
	})
	return views
}

// TrajClearLearnings removes the learning store (user-invoked purge).
// Uses the same cross-process lock discipline as persistLocked.
func TrajClearLearnings(workingDir string) error {
	path := filepath.Join(workingDir, ".ggcode", "trajectory-learnings.jsonl")
	unlock, err := lockTrajFile(path + ".lock")
	if err != nil {
		return err
	}
	defer unlock()
	rmErr := os.Remove(path)
	if rmErr != nil && !os.IsNotExist(rmErr) {
		return rmErr
	}
	return nil
}

// TrajGlobalPath (r460) returns the user-level learning store path
// (~/.ggcode/trajectory-learnings.jsonl). The workspace store is the
// write target, but a fresh workspace previously meant losing every
// accumulated insight; the global store is the cross-workspace tier.
func TrajGlobalPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".ggcode", "trajectory-learnings.jsonl"), nil
}

// TrajMergeInto (r460) folds learnings from srcPath into the workspace
// store at dstWorkingDir (deduped by Category+Type+Insight, so repeat
// merges are idempotent). Used by /traj import and by the sub-agent
// worktree backflow. Same cross-process lock discipline as persistLocked.
func TrajMergeInto(dstWorkingDir, srcPath string) (int, error) {
	data, err := os.ReadFile(srcPath)
	if err != nil {
		return 0, err
	}
	var src []trajectoryLearning
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var l trajectoryLearning
		if json.Unmarshal([]byte(line), &l) == nil && l.Insight != "" {
			src = append(src, l)
		}
	}
	if len(src) == 0 {
		return 0, nil
	}
	dstPath := filepath.Join(dstWorkingDir, ".ggcode", "trajectory-learnings.jsonl")
	if err := os.MkdirAll(filepath.Dir(dstPath), 0o755); err != nil {
		return 0, err
	}
	unlock, lockErr := lockTrajFile(dstPath + ".lock")
	if lockErr != nil {
		return 0, lockErr
	}
	defer unlock()

	existing, loadErr := loadTrajFile(dstPath)
	if loadErr != nil && !os.IsNotExist(loadErr) {
		return 0, loadErr
	}
	have := map[string]bool{}
	for _, l := range existing {
		have[trajDedupeKey(l)] = true
	}
	merged := existing
	added := 0
	for _, l := range src {
		k := trajDedupeKey(l)
		if have[k] {
			continue
		}
		have[k] = true
		merged = append(merged, l)
		added++
	}
	if added == 0 {
		return 0, nil
	}
	merged = consolidateLearnings(merged)
	if len(merged) > trajIntelMaxEntries {
		merged = merged[len(merged)-trajIntelMaxEntries:]
	}
	if err := writeTrajFile(dstPath, merged); err != nil {
		return 0, err
	}
	return added, nil
}

func trajDedupeKey(l trajectoryLearning) string {
	return l.Category + "|" + l.Type + "|" + l.Insight
}

// loadTrajFile / writeTrajFile are path-parameterized variants so
// TrajMergeInto can operate on arbitrary stores (import source, worktree
// residue) without touching trajIntelState's cached filePath.
func loadTrajFile(path string) ([]trajectoryLearning, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var out []trajectoryLearning
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var l trajectoryLearning
		if json.Unmarshal([]byte(line), &l) == nil {
			out = append(out, l)
		}
	}
	return out, nil
}

func writeTrajFile(path string, entries []trajectoryLearning) error {
	tmpF, err := os.CreateTemp(filepath.Dir(path), ".traj-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmpF.Name()
	enc := json.NewEncoder(tmpF)
	for _, l := range entries {
		if encErr := enc.Encode(l); encErr != nil {
			tmpF.Close()
			os.Remove(tmpPath)
			return encErr
		}
	}
	if closeErr := tmpF.Close(); closeErr != nil {
		os.Remove(tmpPath)
		return closeErr
	}
	return os.Rename(tmpPath, path)
}

// TrajExportLearnings (r460) copies the workspace store to outPath
// (atomic tmp+rename). This is the migration/portability surface: users
// moving machines or sharing distilled experience across projects.
func TrajExportLearnings(workingDir, outPath string) (int, error) {
	src := filepath.Join(workingDir, ".ggcode", "trajectory-learnings.jsonl")
	entries, err := loadTrajFile(src)
	if err != nil {
		return 0, err
	}
	if len(entries) == 0 {
		return 0, nil
	}
	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		return 0, err
	}
	if err := writeTrajFile(outPath, entries); err != nil {
		return 0, err
	}
	return len(entries), nil
}

// TrajGlobalStorePath returns the global store path for TUI use (or an
// empty string when the home directory cannot be resolved).
func TrajGlobalStorePath() string {
	p, err := TrajGlobalPath()
	if err != nil {
		return ""
	}
	return p
}

// TrajBackflowFromWorktree (r460) folds a sub-agent worktree's learning
// store back into the main workspace store. Isolation-mode sub-agents ran
// with WorkingDir=worktreePath, so their post-run extraction landed in the
// transient worktree and died with its cleanup. Best-effort: callers
// treat errors as debug-log only, never blocking worktree removal.
func TrajBackflowFromWorktree(mainWorkingDir, worktreePath string) {
	if mainWorkingDir == "" || worktreePath == "" || mainWorkingDir == worktreePath {
		return
	}
	src := filepath.Join(worktreePath, ".ggcode", "trajectory-learnings.jsonl")
	if _, err := os.Stat(src); err != nil {
		return // nothing learned in isolation
	}
	added, err := TrajMergeInto(mainWorkingDir, src)
	if err != nil {
		debug.Log("traj-intel", "worktree backflow failed: %v", err)
		return
	}
	if added > 0 {
		debug.Log("traj-intel", "worktree backflow folded %d learnings into main store", added)
	}
}

// teammateExperienceEntry mirrors internal/swarm's ledger record (kept as
// a local struct to avoid an agent->swarm import edge).
type teammateExperienceEntry struct {
	Ts       time.Time `json:"ts"`
	TeamID   string    `json:"team_id"`
	Teammate string    `json:"teammate"`
	Digest   string    `json:"digest"`
}

// r459 consolidation knobs.
const (
	// trajPromptMinConfidence gates prompt injection: eroded insights stop
	// consuming the 8-slot budget.
	trajPromptMinConfidence = 0.3

	// trajConfidenceHalfLife: unreinforced insights decay to ~half weight
	// over 30 days (computed at read time, never written back).
	trajConfidenceHalfLife = 30 * 24 * time.Hour

	// trajConfidenceStep is the per-reinforcement delta (success raises,
	// failure lowers). Bounded to [0.05, 0.95].
	trajConfidenceStep = 0.1
)

// EffectiveConfidence returns the read-time confidence of a learning:
// legacy zero-value entries read as the 0.5 baseline, everything else is
// the stored value decayed exponentially by time since last
// reinforcement. Pure function - never mutates the entry.
func (l trajectoryLearning) EffectiveConfidence() float64 {
	if l.Confidence == 0 && l.Reinforced == 0 && l.LastReinforced.IsZero() {
		return 0.5
	}
	ref := l.LastReinforced
	if ref.IsZero() {
		ref = l.Timestamp
	}
	age := time.Since(ref)
	if age <= 0 {
		return l.Confidence
	}
	// Weight = 0.5^n for n elapsed half-lives; n=0 (fresh) keeps full
	// weight. Capped iterations floor very old entries instead of
	// floating-point dust.
	decay := 1.0
	if halfLives := int64(age / trajConfidenceHalfLife); halfLives > 0 {
		for i := int64(0); i < halfLives && i < 8; i++ {
			decay *= 0.5
		}
	}
	// Scale stored confidence toward the 0.5 midpoint by the decay weight.
	return 0.5 + (l.Confidence-0.5)*decay
}

// consolidateLearnings merges same Category+Type entries into a single
// reinforced row. The FIRST (oldest) entry keeps its insight text; each
// matching later entry bumps Reinforced and moves confidence up
// (Success=true) or down (Success=false) by trajConfidenceStep, clamped
// to [0.05, 0.95]. LastReinforced tracks the newest merge.
func consolidateLearnings(all []trajectoryLearning) []trajectoryLearning {
	// Fast path: nothing to merge.
	type key struct{ cat, typ string }
	idx := map[key]int{}
	var out []trajectoryLearning
	for _, l := range all {
		// Only classified observations (non-empty Category) participate in
		// reinforcement merging. Unclassified entries (empty Category -
		// e.g. raw concurrent writes with no extraction) are distinct
		// observations: merging them would silently drop rows and regress
		// the #1512 no-lost-update guarantee.
		if l.Category == "" {
			out = append(out, l)
			continue
		}
		k := key{l.Category, l.Type}
		if i, ok := idx[k]; ok {
			c := out[i].Confidence
			if c == 0 && out[i].Reinforced == 0 {
				c = 0.5 // legacy baseline
			}
			if l.Success {
				c += trajConfidenceStep
			} else {
				c -= trajConfidenceStep
			}
			if c < 0.05 {
				c = 0.05
			}
			if c > 0.95 {
				c = 0.95
			}
			out[i].Confidence = c
			out[i].Reinforced++
			if l.Timestamp.After(out[i].LastReinforced) {
				out[i].LastReinforced = l.Timestamp
			}
			continue
		}
		idx[k] = len(out)
		out = append(out, l)
	}
	return out
}

// ingestTeammateExperience (r457) folds the swarm teammate-experience
// ledger into the main learning store. The ledger is written by
// internal/swarm on every completed teammate task; without this ingest
// the accumulation stays siloed (teammate_results is latest-only and the
// teammate's experience dies with its shutdown). Entries become
// Type="teammate" learnings, deduped by timestamp so re-ingest is
// idempotent. Runs in the post-run defer path - best effort, never
// blocks the run.
func (s *trajIntelState) ingestTeammateExperience(workingDir string) {
	if workingDir == "" {
		return
	}
	if s.filePath == "" {
		s.filePath = filepath.Join(workingDir, ".ggcode", "trajectory-learnings.jsonl")
	}
	ledger := filepath.Join(workingDir, ".ggcode", "teammate-experience.jsonl")
	data, err := os.ReadFile(ledger)
	if err != nil || len(data) == 0 {
		return // absent ledger is normal (no swarm activity yet)
	}
	// Already-ingested high-water mark: newest teammate-ts in the MAIN
	// store (the file, not the pending buffer - persistLocked clears the
	// buffer after each write, so the buffer is not a durable mark).
	// Idempotence by monotonic ts, not content hashing.
	var newest time.Time
	if existing, err := s.loadFromFile(); err == nil {
		for _, l := range existing {
			if l.Type == "teammate" && l.Timestamp.After(newest) {
				newest = l.Timestamp
			}
		}
	}

	var fresh []trajectoryLearning
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var e teammateExperienceEntry
		if json.Unmarshal([]byte(line), &e) != nil || e.Digest == "" {
			continue
		}
		if !e.Ts.After(newest) {
			continue // already ingested
		}
		fresh = append(fresh, trajectoryLearning{
			Timestamp: e.Ts,
			Type:      "teammate",
			Task:      truncateTask("swarm/"+e.Teammate, 120),
			Success:   true, // completed results only; failures die in the breaker
			Insight:   "teammate experience: " + truncateTask(e.Digest, 300),
			Category:  "teammate_experience",
		})
	}
	if len(fresh) == 0 {
		return
	}
	// Reuse the persist path's dedupe/trim/lock discipline by feeding the
	// pending buffer; maybeExtractAndPersist->persistLocked will fold it
	// into the main store on this run's persist.
	s.mu.Lock()
	s.learnings = append(s.learnings, fresh...)
	s.mu.Unlock()
	debug.Log("traj-intel", "ingested %d teammate experience entries", len(fresh))
}

// --- Helpers ---

func truncateTask(s string, max int) string {
	s = strings.TrimSpace(s)
	return truncateRunesUTF8(s, max)
}

// truncateRunesUTF8 caps s at max BYTES without ever splitting a UTF-8
// rune (#3257): the three legacy byte-slice truncation sites produced
// invalid UTF-8 on CJK text ~2/3 of the time, and json.Encoder silently
// replaced the dangling bytes with U+FFFD - permanent corruption in the
// persisted store and every re-injected system prompt. Walks back to the
// last rune boundary at or before max; appends the ellipsis only when
// truncation actually happened (and within the budget).
func truncateRunesUTF8(s string, max int) string {
	if len(s) <= max {
		return s
	}
	cut := max
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	// Leave room for the ellipsis inside the budget when it fits.
	const ell = "..."
	if cut >= len(ell) {
		cut -= len(ell)
		for cut > 0 && !utf8.RuneStart(s[cut]) {
			cut--
		}
		return s[:cut] + ell
	}
	return s[:cut]
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
	// Take the first error, truncated (#3257: rune-safe for CJK errors).
	first := errors[0]
	first = truncateRunesUTF8(first, 100)
	if len(errors) == 1 {
		return first
	}
	return fmt.Sprintf("%s (+%d more)", first, len(errors)-1)
}
