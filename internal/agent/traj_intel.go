package agent

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
	trajIntelMinIterations = 3

	trajIntelMaxEntries = 50

	trajIntelHighIterationThreshold = 15

	trajIntelLowEditThreshold = 2

	trajIntelHighRetryRatio = 0.25

	trajIntelHighContextRatio = 0.80

	trajIntelHighCompaction = 2
)

type trajectoryLearning struct {
	Timestamp time.Time      `json:"timestamp"`
	Type      string         `json:"type"`
	Task      string         `json:"task"`
	Success   bool           `json:"success"`
	Insight   string         `json:"insight"`
	Metrics   map[string]int `json:"metrics,omitempty"`
	Category  string         `json:"category"`

	Confidence     float64   `json:"confidence,omitempty"`
	Reinforced     int       `json:"reinforced,omitempty"`
	LastReinforced time.Time `json:"last_reinforced,omitempty"`

	InjectedRuns int `json:"injected_runs,omitempty"`
	AfterSuccess int `json:"after_success,omitempty"`
	AfterFail    int `json:"after_fail,omitempty"`

	Refined string `json:"refined,omitempty"`
}

type trajIntelState struct {
	mu        sync.Mutex
	learnings []trajectoryLearning
	filePath  string
	loaded    bool

	injectedThisRun map[trajKey]bool

	holdoutThisRun map[trajKey]bool
}

func newTrajIntelState() *trajIntelState {
	return &trajIntelState{}
}

func (s *trajIntelState) extractInsights(stats *RunStats) []trajectoryLearning {
	if stats == nil || stats.Iterations < trajIntelMinIterations {
		return nil
	}

	task := truncateTask(stats.UserPrompt, 120)
	now := time.Now()
	totalToolCalls := totalToolCallCount(stats.ToolCalls)

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

func (s *trajIntelState) maybeExtractAndPersist(workingDir string, stats *RunStats) {
	learnings := s.extractInsights(stats)
	if len(learnings) == 0 {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.filePath == "" && workingDir != "" {
		s.filePath = filepath.Join(workingDir, ".ggcode", "trajectory-learnings.jsonl")
	}

	s.learnings = append(s.learnings, learnings...)

	if s.filePath != "" {
		if err := s.persistLocked(); err != nil {
			debug.Log("traj-intel", "failed to persist learnings: %v", err)
		}
	}
}

func trimLearnings(all []trajectoryLearning) []trajectoryLearning {
	if len(all) <= trajIntelMaxEntries {
		return all
	}
	sort.SliceStable(all, func(i, j int) bool {
		ci, cj := all[i].EffectiveConfidence(), all[j].EffectiveConfidence()
		if ci != cj {
			return ci > cj
		}
		return all[i].Timestamp.After(all[j].Timestamp)
	})
	all = all[:trajIntelMaxEntries]
	sort.SliceStable(all, func(i, j int) bool {
		return all[i].Timestamp.Before(all[j].Timestamp)
	})
	return all
}

func (s *trajIntelState) persistLocked() error {
	writeErr := s.rewriteAllLocked(func(existing []trajectoryLearning, loadErr error) ([]trajectoryLearning, error) {
		if loadErr != nil && !os.IsNotExist(loadErr) {
			return nil, fmt.Errorf("load: %w", loadErr)
		}

		all := consolidateLearnings(append(existing, s.learnings...))

		all = trimLearnings(all)
		return all, nil
	})
	if writeErr != nil {
		return writeErr
	}

	s.learnings = nil
	s.loaded = true
	return nil
}

func (s *trajIntelState) rewriteAllLocked(fn func(existing []trajectoryLearning, loadErr error) ([]trajectoryLearning, error)) error {
	dir := filepath.Dir(s.filePath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}

	unlock, lockErr := lockTrajFile(s.filePath + ".lock")
	if lockErr != nil {
		return fmt.Errorf("lock: %w", lockErr)
	}
	defer unlock()

	existing, loadErr := s.loadFromFile()
	all, err := fn(existing, loadErr)
	if err != nil {
		return err
	}
	if all == nil {
		return nil
	}

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
	return nil
}

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
			continue
		}
		result = append(result, l)
	}
	return result, nil
}

const (
	trajPromptMaxEntries = 8
	trajPromptMaxChars   = 1200
	trajPromptPerType    = 3
)

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

	if globalPath, gErr := TrajGlobalPath(); gErr == nil {
		if gEntries, gLoadErr := loadTrajFile(globalPath); gLoadErr == nil && len(gEntries) > 0 {

			localCats := map[string]bool{}
			for _, l := range entries {
				if trajInjectEligible(l) {
					localCats[l.Category] = true
				}
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

	sort.SliceStable(entries, func(i, j int) bool {
		ci, cj := entries[i].EffectiveConfidence(), entries[j].EffectiveConfidence()
		if ci != cj {
			return ci > cj
		}
		return entries[i].Timestamp.After(entries[j].Timestamp)
	})

	held := trajHoldoutSelect(workingDir, entries)
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

		if effectivenessGated(l) {
			continue
		}

		if held[trajKeyOf(l)] {
			s.recordHoldoutLocked(l)
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
		s.recordInjectedLocked(l)

		text := l.Refined
		if text == "" {
			text = l.Insight
		}
		lines = append(lines, fmt.Sprintf("- [%s] %s", l.Type, text))
		total++
	}
	if len(lines) == 0 {
		return ""
	}
	section := "Past-run learnings (auto-extracted from previous sessions - apply where relevant, ignore where not):\n" +
		strings.Join(lines, "\n")
	if len(section) > trajPromptMaxChars {
		const marker = "\n- [...] older learnings truncated"
		section = truncateRunesUTF8(section, trajPromptMaxChars-len(marker)) + marker
	}
	return section
}

type TrajLearningView struct {
	Type       string
	Category   string
	Insight    string
	Confidence float64
	Reinforced int
	Timestamp  time.Time
	Injects    bool
	General    bool

	Holdout  bool
	DeltaPP  float64
	HoldRuns int
}

func trajSimulateInjected(workingDir string, sorted []trajectoryLearning) map[int]bool {
	held := trajHoldoutSelect(workingDir, sorted)
	out := map[int]bool{}
	counts := map[string]int{}
	seenCat := map[string]bool{}
	total := 0
	for i, l := range sorted {
		if total >= trajPromptMaxEntries {
			break
		}
		if l.EffectiveConfidence() < trajPromptMinConfidence {
			continue
		}
		if effectivenessGated(l) {
			continue
		}
		if held[trajKeyOf(l)] {
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
		out[i] = true
		total++
	}
	return out
}

func TrajListLearnings(workingDir string) []TrajLearningView {
	s := newTrajIntelState()
	s.filePath = filepath.Join(workingDir, ".ggcode", "trajectory-learnings.jsonl")
	s.mu.Lock()
	defer s.mu.Unlock()
	entries, err := s.loadFromFile()
	if err != nil {
		entries = nil
	}
	if globalPath, gErr := TrajGlobalPath(); gErr == nil {
		if gEntries, gLoadErr := loadTrajFile(globalPath); gLoadErr == nil && len(gEntries) > 0 {

			localCats := map[string]bool{}
			for _, l := range entries {
				if trajInjectEligible(l) {
					localCats[l.Category] = true
				}
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
		return nil
	}
	sort.SliceStable(entries, func(i, j int) bool {
		ci, cj := entries[i].EffectiveConfidence(), entries[j].EffectiveConfidence()
		if ci != cj {
			return ci > cj
		}
		return entries[i].Timestamp.After(entries[j].Timestamp)
	})
	wouldInject := trajSimulateInjected(workingDir, entries)
	snap := trajHoldoutSnapshot(workingDir)
	views := make([]TrajLearningView, 0, len(entries))
	for i, l := range entries {
		conf := l.EffectiveConfidence()
		v := TrajLearningView{
			Type: l.Type, Category: l.Category, Insight: l.Insight,
			Confidence: conf, Reinforced: l.Reinforced, Timestamp: l.Timestamp,
			Injects: wouldInject[i],
			General: strings.HasSuffix(l.Insight, "(general, other projects)"),
		}

		k := trajKeyOf(l)
		if e, ok := snap[k]; ok && e.InsightKey == trajHoldoutKeyString(k) {
			v.Holdout = true
			v.HoldRuns = e.HoldoutRuns
			if d, dok := trajHoldoutDelta(l, e); dok {
				v.DeltaPP = d * 100
			}
		}
		views = append(views, v)
	}
	return views
}

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

func TrajClearGlobalLearnings() error {
	path, err := TrajGlobalPath()
	if err != nil {
		return err
	}
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

func TrajGlobalRemaining() (int, error) {
	path, err := TrajGlobalPath()
	if err != nil {
		return 0, err
	}
	entries, err := loadTrajFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	return len(entries), nil
}

func TrajGlobalPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".ggcode", "trajectory-learnings.jsonl"), nil
}

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

	merged = trimLearnings(merged)
	if err := writeTrajFile(dstPath, merged); err != nil {
		return 0, err
	}
	return added, nil
}

func trajDedupeKey(l trajectoryLearning) string {
	return l.Category + "|" + l.Type + "|" + l.Insight
}

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

func TrajGlobalStorePath() string {
	p, err := TrajGlobalPath()
	if err != nil {
		return ""
	}
	return p
}

func TrajBackflowFromWorktree(mainWorkingDir, worktreePath string) {
	if mainWorkingDir == "" || worktreePath == "" || mainWorkingDir == worktreePath {
		return
	}
	src := filepath.Join(worktreePath, ".ggcode", "trajectory-learnings.jsonl")
	if _, err := os.Stat(src); err != nil {
		return
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

type teammateExperienceEntry struct {
	Ts       time.Time `json:"ts"`
	TeamID   string    `json:"team_id"`
	Teammate string    `json:"teammate"`
	Digest   string    `json:"digest"`
}

const (
	trajPromptMinConfidence = 0.3

	trajConfidenceHalfLife = 30 * 24 * time.Hour

	trajConfidenceStep = 0.1

	trajEffectMinSamples = 3
	trajEffectMinSuccess = 0.4
)

const trajConfidenceDecayPoint = 0.25

func (l trajectoryLearning) EffectiveConfidence() float64 {
	conf := l.Confidence
	if l.Confidence == 0 && l.Reinforced == 0 && l.LastReinforced.IsZero() {
		conf = 0.5
	}
	ref := l.LastReinforced
	if ref.IsZero() {
		ref = l.Timestamp
	}
	age := time.Since(ref)
	if age <= 0 {
		return conf
	}

	decay := 1.0
	if halfLives := int64(age / trajConfidenceHalfLife); halfLives > 0 {
		for i := int64(0); i < halfLives && i < 8; i++ {
			decay *= 0.5
		}
	}

	return trajConfidenceDecayPoint + (conf-trajConfidenceDecayPoint)*decay
}

func effectivenessGated(l trajectoryLearning) bool {
	return l.InjectedRuns >= trajEffectMinSamples &&
		float64(l.AfterSuccess)/float64(l.InjectedRuns) < trajEffectMinSuccess
}

func trajInjectEligible(l trajectoryLearning) bool {
	return l.EffectiveConfidence() >= trajPromptMinConfidence && !effectivenessGated(l)
}

func consolidateLearnings(all []trajectoryLearning) []trajectoryLearning {
	type key struct{ cat, typ string }
	idx := map[key]int{}
	var out []trajectoryLearning
	for _, l := range all {

		if l.Category == "" {
			out = append(out, l)
			continue
		}
		k := key{l.Category, l.Type}
		if i, ok := idx[k]; ok {
			c := out[i].Confidence
			if c == 0 && out[i].Reinforced == 0 {
				c = 0.5
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

			out[i].InjectedRuns += l.InjectedRuns
			out[i].AfterSuccess += l.AfterSuccess
			out[i].AfterFail += l.AfterFail
			if l.Timestamp.After(out[i].LastReinforced) {
				out[i].LastReinforced = l.Timestamp
			}

			if l.Timestamp.After(out[i].Timestamp) {
				out[i].Insight = l.Insight
				out[i].Timestamp = l.Timestamp
			}
			continue
		}
		idx[k] = len(out)
		out = append(out, l)
	}
	return out
}

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
		return
	}
	var newest time.Time
	if existing, err := s.loadFromFile(); err == nil {
		for _, l := range existing {
			if l.Type != "teammate" {
				continue
			}

			for _, ts := range []time.Time{l.Timestamp, l.LastReinforced} {
				if ts.After(newest) {
					newest = ts
				}
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
			continue
		}
		fresh = append(fresh, trajectoryLearning{
			Timestamp: e.Ts,
			Type:      "teammate",
			Task:      truncateTask("swarm/"+e.Teammate, 120),
			Success:   true,
			Insight:   "teammate experience: " + truncateTask(e.Digest, 300),
			Category:  "teammate_experience",
		})
	}
	if len(fresh) == 0 {
		return
	}

	s.mu.Lock()
	s.learnings = append(s.learnings, fresh...)
	s.mu.Unlock()
	debug.Log("traj-intel", "ingested %d teammate experience entries", len(fresh))
}

type trajKey struct{ cat, typ string }

func trajKeyOf(l trajectoryLearning) trajKey {
	if l.Category != "" {
		return trajKey{cat: l.Category, typ: l.Type}
	}
	return trajKey{cat: l.Type, typ: l.Type}
}

func (s *trajIntelState) recordInjectedLocked(l trajectoryLearning) {
	if s.injectedThisRun == nil {
		s.injectedThisRun = make(map[trajKey]bool)
	}
	s.injectedThisRun[trajKeyOf(l)] = true
}

func (s *trajIntelState) clearInjectedRun() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.injectedThisRun = nil
	s.holdoutThisRun = nil
}

func (s *trajIntelState) recordInjectionOutcome(workingDir string, success bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.injectedThisRun) == 0 {
		return
	}
	keys := s.injectedThisRun
	s.injectedThisRun = nil
	if s.filePath == "" && workingDir != "" {
		s.filePath = filepath.Join(workingDir, ".ggcode", "trajectory-learnings.jsonl")
	}
	if s.filePath == "" {
		return
	}

	matched := make(map[trajKey]bool)
	bump := func(existing []trajectoryLearning) bool {
		changed := false
		for i := range existing {
			k := trajKeyOf(existing[i])
			if !keys[k] {
				continue
			}
			existing[i].InjectedRuns++
			if success {
				existing[i].AfterSuccess++
			} else {
				existing[i].AfterFail++
			}
			matched[k] = true
			changed = true
		}
		return changed
	}
	err := s.rewriteAllLocked(func(existing []trajectoryLearning, loadErr error) ([]trajectoryLearning, error) {
		if loadErr != nil && !os.IsNotExist(loadErr) {
			return nil, fmt.Errorf("load: %w", loadErr)
		}
		if !bump(existing) {
			return nil, nil
		}
		return existing, nil
	})
	if err != nil {
		debug.Log("traj-intel", "outcome write-back failed: %v", err)
	}

	leftover := false
	for k := range keys {
		if !matched[k] {
			leftover = true
			break
		}
	}
	if leftover {
		if globalPath, gErr := TrajGlobalPath(); gErr == nil {
			g := &trajIntelState{filePath: globalPath}
			if gErr := g.rewriteAllLocked(func(existing []trajectoryLearning, loadErr error) ([]trajectoryLearning, error) {
				if loadErr != nil && !os.IsNotExist(loadErr) {
					return nil, fmt.Errorf("load: %w", loadErr)
				}
				if !bump(existing) {
					return nil, nil
				}
				return existing, nil
			}); gErr != nil {
				debug.Log("traj-intel", "global-tier outcome write-back failed: %v", gErr)
			}
		}
	}
}

func truncateTask(s string, max int) string {
	s = strings.TrimSpace(s)
	return truncateRunesUTF8(s, max)
}

func truncateRunesUTF8(s string, max int) string {
	if len(s) <= max {
		return s
	}
	cut := max
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
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

	first := errors[0]
	first = truncateRunesUTF8(first, 100)
	if len(errors) == 1 {
		return first
	}
	return fmt.Sprintf("%s (+%d more)", first, len(errors)-1)
}
