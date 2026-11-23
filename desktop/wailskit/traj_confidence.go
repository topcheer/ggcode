package wailskit

import (
	"sort"

	"github.com/topcheer/ggcode/internal/agent"
)

type TrajConfidenceEntry struct {
	Type       string  `json:"type"`
	Insight    string  `json:"insight"`
	Confidence float64 `json:"confidence"`
	Reinforced int     `json:"reinforced"`
	General    bool    `json:"general"`
}

const topTrajEntries = 5

type TrajConfidenceOverview struct {
	Total         int                   `json:"total"`
	AvgConfidence float64               `json:"avgConfidence"`
	Top           []TrajConfidenceEntry `json:"top"`
	HasLearnings  bool                  `json:"hasLearnings"`
}

// TrajConfidenceOverview summarises the stored trajectory learnings for the
// desktop panel. Learnings are no longer injected into prompts, so the
// overview is a confidence view over the whole store (the former
// "injecting"/"holdout" facets went with the injection channel).
func (b *ChatBridge) TrajConfidenceOverview() TrajConfidenceOverview {
	views := agent.TrajListLearnings(b.workingDir)
	out := TrajConfidenceOverview{Total: len(views), Top: []TrajConfidenceEntry{}}
	if len(views) == 0 {
		return out
	}
	out.HasLearnings = true
	sum := 0.0
	for _, v := range views {
		sum += v.Confidence
	}
	out.AvgConfidence = sum / float64(len(views))
	sorted := append([]agent.TrajLearningView(nil), views...)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].Confidence != sorted[j].Confidence {
			return sorted[i].Confidence > sorted[j].Confidence
		}
		return sorted[i].Reinforced > sorted[j].Reinforced
	})
	for i, v := range sorted {
		if i >= topTrajEntries {
			break
		}
		out.Top = append(out.Top, TrajConfidenceEntry{
			Type:       v.Type,
			Insight:    v.Insight,
			Confidence: v.Confidence,
			Reinforced: v.Reinforced,
			General:    v.General,
		})
	}
	return out
}
