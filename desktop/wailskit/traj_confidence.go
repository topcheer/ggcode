package wailskit

import (
	"sort"

	"github.com/topcheer/ggcode/internal/agent"
)

// TrajConfidenceEntry is one learning projected for UI display.
type TrajConfidenceEntry struct {
	Type       string  `json:"type"`
	Insight    string  `json:"insight"`
	Confidence float64 `json:"confidence"`
	Reinforced int     `json:"reinforced"`
	Injects    bool    `json:"injects"`
	General    bool    `json:"general"`
	Holdout    bool    `json:"holdout"`
}

// topTrajEntries caps the Top list so the UI badge stays compact.
const topTrajEntries = 5

// TrajConfidenceOverview aggregates the workspace's trajectory
// learnings for confidence signaling. AvgConfidence covers entries
// that actually inject (Holdout entries are excluded from the average
// but still counted in Total); 0 when nothing injects. Top lists the
// highest-confidence injecting entries (max 5) so the UI can show what
// currently steers the agent.
type TrajConfidenceOverview struct {
	Total         int                   `json:"total"`
	Injecting     int                   `json:"injecting"`
	AvgConfidence float64               `json:"avgConfidence"`
	Top           []TrajConfidenceEntry `json:"top"`
	HasLearnings  bool                  `json:"hasLearnings"`
}

// TrajConfidenceOverview computes the aggregate from the workspace's
// learning store. Errors degrade to an empty overview (the UI treats it
// as "no signal"), matching the advisory nature of the score.
func (b *ChatBridge) TrajConfidenceOverview() TrajConfidenceOverview {
	views := agent.TrajListLearnings(b.workingDir)
	out := TrajConfidenceOverview{Total: len(views), Top: []TrajConfidenceEntry{}}
	if len(views) == 0 {
		return out
	}
	out.HasLearnings = true
	var injecting []agent.TrajLearningView
	sum := 0.0
	for _, v := range views {
		if v.Injects && !v.Holdout {
			out.Injecting++
			sum += v.Confidence
			injecting = append(injecting, v)
		}
	}
	if out.Injecting > 0 {
		out.AvgConfidence = sum / float64(out.Injecting)
	}
	sort.Slice(injecting, func(i, j int) bool {
		if injecting[i].Confidence != injecting[j].Confidence {
			return injecting[i].Confidence > injecting[j].Confidence
		}
		return injecting[i].Reinforced > injecting[j].Reinforced
	})
	for i, v := range injecting {
		if i >= topTrajEntries {
			break
		}
		out.Top = append(out.Top, TrajConfidenceEntry{
			Type:       v.Type,
			Insight:    v.Insight,
			Confidence: v.Confidence,
			Reinforced: v.Reinforced,
			Injects:    v.Injects,
			General:    v.General,
			Holdout:    v.Holdout,
		})
	}
	return out
}
