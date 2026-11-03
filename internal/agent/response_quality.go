package agent

import (
	"fmt"
	"math"
	"strings"
	"sync"
	"time"
)

type ResponseQualityScorer struct {
	mu     sync.RWMutex
	runs   []QualityEntry
	maxRun int

	latestRegression RegressionReport
}

type QualityEntry struct {
	Provider   string
	Model      string
	Timestamp  time.Time
	Score      float64
	Signals    QualitySignals
	UserPrompt string
	RunID      string
}

type QualitySignals struct {
	ToolEfficiency   float64
	ErrorRate        float64
	IterationRatio   float64
	Success          bool
	DurationSecs     float64
	FilesEdited      int
	ContextPeakRatio float64
}

func NewResponseQualityScorer(maxRuns int) *ResponseQualityScorer {
	if maxRuns <= 0 {
		maxRuns = 100
	}
	return &ResponseQualityScorer{maxRun: maxRuns}
}

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

func (s *ResponseQualityScorer) computeScore(stats *RunStats, providerName, modelName string) QualityEntry {
	signals := QualitySignals{
		Success:      stats.Success,
		FilesEdited:  len(stats.FilesEdited),
		DurationSecs: stats.Duration.Seconds(),
	}

	totalTools := 0
	for _, count := range stats.ToolCalls {
		totalTools += count
	}

	errorCount := stats.ErrorCount
	if totalTools > 0 {
		failedTools := 0
		if errorCount <= totalTools {
			failedTools = errorCount
		} else {
			failedTools = totalTools
		}
		signals.ToolEfficiency = float64(totalTools-failedTools) / float64(totalTools)
	} else if stats.Success {
		signals.ToolEfficiency = 1.0
	}

	if stats.Iterations > 0 {
		signals.ErrorRate = float64(errorCount) / float64(stats.Iterations)
	}

	expectedIter := 3
	if stats.Iterations > 0 {
		signals.IterationRatio = float64(stats.Iterations) / float64(expectedIter)
	}

	if stats.ContextWindow > 0 {
		signals.ContextPeakRatio = float64(stats.ContextPeakTokens) / float64(stats.ContextWindow)
	}

	score := s.weightedScore(signals)

	prompt := stats.UserPrompt
	if len(prompt) > 100 {

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

func (s *ResponseQualityScorer) weightedScore(sig QualitySignals) float64 {
	var score float64

	if sig.Success {
		score += 0.40
	}

	score += 0.25 * clamp01(sig.ToolEfficiency)

	errorScore := clamp01(1.0 - sig.ErrorRate)
	score += 0.15 * errorScore

	iterScore := clamp01(1.5 - sig.IterationRatio)
	score += 0.10 * iterScore

	ctxScore := clamp01(1.0 - sig.ContextPeakRatio)
	score += 0.10 * ctxScore

	return math.Round(score*1000) / 1000
}

type ProviderComparison struct {
	Provider    string
	Model       string
	RunCount    int
	AvgScore    float64
	SuccessRate float64
	AvgDuration float64
	BestScore   float64
}

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

	for i := 0; i < len(result); i++ {
		for j := i + 1; j < len(result); j++ {
			if result[j].AvgScore > result[i].AvgScore {
				result[i], result[j] = result[j], result[i]
			}
		}
	}
	return result
}

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

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}
