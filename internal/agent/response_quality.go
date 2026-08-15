package agent

import (
	"fmt"
	"math"
	"strings"
	"sync"
	"time"
)

// ResponseQualityScorer tracks per-run quality metrics to enable
// provider/model comparison. Unlike failover (which reacts to failures),
// this proactively measures response quality signals so users can make
// informed A/B decisions when switching models.
//
// Design: lightweight, deterministic, zero LLM overhead. All signals are
// derived from existing RunStats — no extra API calls needed.
type ResponseQualityScorer struct {
	mu     sync.RWMutex
	runs   []QualityEntry
	maxRun int

	// latestRegression is the most recent regression report (if any).
	// Populated by maybeDetectRegression after each scored run.
	latestRegression RegressionReport
}

// QualityEntry records quality signals for a single agent run.
type QualityEntry struct {
	Provider   string    // provider name (e.g. "anthropic", "openai")
	Model      string    // model name (e.g. "claude-sonnet-4")
	Timestamp  time.Time // when the run completed
	Score      float64   // overall quality score 0.0–1.0
	Signals    QualitySignals
	UserPrompt string // first 100 chars
	RunID      string
}

// QualitySignals are the individual metrics that compose the overall score.
type QualitySignals struct {
	ToolEfficiency   float64 // successful_tools / total_tools (0–1)
	ErrorRate        float64 // errors / iterations (0 = best)
	IterationRatio   float64 // iterations_used / expected (lower = better)
	Success          bool
	DurationSecs     float64
	FilesEdited      int
	ContextPeakRatio float64 // context_peak / context_window (lower = better)
}

// NewResponseQualityScorer creates a scorer with the given history capacity.
func NewResponseQualityScorer(maxRuns int) *ResponseQualityScorer {
	if maxRuns <= 0 {
		maxRuns = 100
	}
	return &ResponseQualityScorer{maxRun: maxRuns}
}

// ScoreRun computes quality signals from RunStats and records the entry.
// provider and model identify the LLM used for this run.
func (s *ResponseQualityScorer) ScoreRun(stats *RunStats, providerName, modelName string) QualityEntry {
	entry := s.computeScore(stats, providerName, modelName)
	s.mu.Lock()
	s.runs = append(s.runs, entry)
	if len(s.runs) > s.maxRun {
		s.runs = s.runs[len(s.runs)-s.maxRun:]
	}
	s.mu.Unlock()
	return entry
}

// computeScore derives quality signals from RunStats.
func (s *ResponseQualityScorer) computeScore(stats *RunStats, providerName, modelName string) QualityEntry {
	signals := QualitySignals{
		Success:      stats.Success,
		FilesEdited:  len(stats.FilesEdited),
		DurationSecs: stats.Duration.Seconds(),
	}

	// Tool efficiency: ratio of non-error tool calls to total tool calls.
	totalTools := 0
	for _, count := range stats.ToolCalls {
		totalTools += count
	}
	errorCount := len(stats.Errors)
	if totalTools > 0 {
		failedTools := 0
		if errorCount <= totalTools {
			failedTools = errorCount
		} else {
			failedTools = totalTools
		}
		signals.ToolEfficiency = float64(totalTools-failedTools) / float64(totalTools)
	} else if stats.Success {
		signals.ToolEfficiency = 1.0 // no tools needed, answered directly
	}
	// Error rate: errors per iteration.
	if stats.Iterations > 0 {
		signals.ErrorRate = float64(errorCount) / float64(stats.Iterations)
	}

	// Iteration ratio: how many turns were needed? Lower is better.
	// Expected baseline: 3 iterations for a simple task.
	expectedIter := 3
	if stats.Iterations > 0 {
		signals.IterationRatio = float64(stats.Iterations) / float64(expectedIter)
	}

	// Context usage efficiency.
	if stats.ContextWindow > 0 {
		signals.ContextPeakRatio = float64(stats.ContextPeakTokens) / float64(stats.ContextWindow)
	}

	// Compute overall score (weighted sum, 0.0–1.0).
	score := s.weightedScore(signals)

	prompt := stats.UserPrompt
	if len(prompt) > 100 {
		// #420: truncate by RUNE, not bytes — the old prompt[:100] sliced
		// mid-codepoint for CJK prompts (60 Chinese chars = 180 bytes →
		// invalid UTF-8 → U+FFFD mojibake after JSON serialization).
		prompt = truncateRunes(prompt, 100)
	}

	return QualityEntry{
		Provider:   providerName,
		Model:      modelName,
		Timestamp:  time.Now(),
		Score:      score,
		Signals:    signals,
		UserPrompt: prompt,
		RunID:      stats.runID,
	}
}

// weightedScore combines individual signals into an overall score.
func (s *ResponseQualityScorer) weightedScore(sig QualitySignals) float64 {
	var score float64

	// Success is the most important signal (40%).
	if sig.Success {
		score += 0.40
	}

	// Tool efficiency (25%): how well the agent used tools without errors.
	score += 0.25 * clamp01(sig.ToolEfficiency)

	// Error rate penalty (15%): fewer errors per iteration is better.
	errorScore := clamp01(1.0 - sig.ErrorRate)
	score += 0.15 * errorScore

	// Iteration efficiency (10%): fewer iterations than expected is better.
	iterScore := clamp01(1.5 - sig.IterationRatio) // 1.5 so even 50% over gets some credit
	score += 0.10 * iterScore

	// Context efficiency (10%): using less of the context window is better.
	ctxScore := clamp01(1.0 - sig.ContextPeakRatio)
	score += 0.10 * ctxScore

	return math.Round(score*1000) / 1000 // 3 decimal places
}

// ProviderComparison returns aggregated stats per provider/model pair.
type ProviderComparison struct {
	Provider    string
	Model       string
	RunCount    int
	AvgScore    float64
	SuccessRate float64
	AvgDuration float64
	BestScore   float64
}

// Compare returns per-provider/model aggregated comparisons.
func (s *ResponseQualityScorer) Compare() []ProviderComparison {
	s.mu.RLock()
	defer s.mu.RUnlock()

	type agg struct {
		count    int
		scoreSum float64
		success  int
		durSum   float64
		best     float64
	}
	// #420: struct key, not string concat — "openrouter/anthropic"+"claude-3.5"
	// concatenated to "openrouter/anthropic/claude-3.5" and SplitN("/",2)
	// then reparsed it as provider="openrouter", model="anthropic/claude-3.5",
	// mis-attributing A/B comparison stats.
	type pmKey struct{ provider, model string }
	aggs := map[pmKey]*agg{}

	for _, r := range s.runs {
		k := pmKey{r.Provider, r.Model}
		a, ok := aggs[k]
		if !ok {
			a = &agg{best: r.Score}
			aggs[k] = a
		}
		a.count++
		a.scoreSum += r.Score
		if r.Score > a.best {
			a.best = r.Score
		}
		if r.Signals.Success {
			a.success++
		}
		a.durSum += r.Signals.DurationSecs
	}

	result := make([]ProviderComparison, 0, len(aggs))
	for k, a := range aggs {
		comp := ProviderComparison{
			Provider:    k.provider,
			Model:       k.model,
			RunCount:    a.count,
			AvgScore:    math.Round(a.scoreSum/float64(a.count)*1000) / 1000,
			SuccessRate: math.Round(float64(a.success)/float64(a.count)*1000) / 1000,
			AvgDuration: math.Round(a.durSum/float64(a.count)*100) / 100,
			BestScore:   a.best,
		}
		result = append(result, comp)
	}

	// Sort by avg score descending.
	for i := 0; i < len(result); i++ {
		for j := i + 1; j < len(result); j++ {
			if result[j].AvgScore > result[i].AvgScore {
				result[i], result[j] = result[j], result[i]
			}
		}
	}
	return result
}

// FormatComparison returns a human-readable summary of provider performance.
func (s *ResponseQualityScorer) FormatComparison() string {
	comps := s.Compare()
	if len(comps) == 0 {
		return "No quality data recorded yet."
	}

	var b strings.Builder
	b.WriteString("Provider/Model Quality Comparison:\n")
	b.WriteString(fmt.Sprintf("  %-25s %5s %7s %7s %8s\n", "Provider/Model", "Runs", "AvgScore", "Success", "AvgTime"))
	for _, c := range comps {
		label := c.Provider + "/" + c.Model
		if len(label) > 25 {
			label = label[:22] + "..."
		}
		b.WriteString(fmt.Sprintf("  %-25s %5d %7.3f %6.1f%% %7.1fs\n",
			label, c.RunCount, c.AvgScore, c.SuccessRate*100, c.AvgDuration))
	}
	return b.String()
}

// Recent returns the last n quality entries.
func (s *ResponseQualityScorer) Recent(n int) []QualityEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if n <= 0 || n > len(s.runs) {
		n = len(s.runs)
	}
	start := len(s.runs) - n
	result := make([]QualityEntry, n)
	copy(result, s.runs[start:])
	return result
}

// truncateRunes truncates s to at most n runes without splitting a
// multi-byte codepoint (#420).
func truncateRunes(s string, n int) string {
	if n <= 0 {
		return ""
	}
	count := 0
	for i := range s {
		if count == n {
			return s[:i]
		}
		count++
	}
	return s
}

// clamp01 limits a value to the [0, 1] range.
func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}
