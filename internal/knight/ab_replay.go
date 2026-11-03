package knight

import (
	"sort"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
)

type ABReplayResult struct {
	ScenariosConsidered int
	CandidateScore      float64
	BaselineScore       float64
	Delta               float64
	TopMatchedTasks     []string
}

func abReplayFingerprint(name, description, body string) string {
	return strings.Join([]string{name, description, body}, "\n")
}

func baselineReplayBody(e *SkillEntry) string {
	if e == nil {
		return ""
	}
	body := ""
	if b, err := readSkillContent(e.Path); err == nil {
		body = string(b)
	} else {

		debug.Log("knight", "ab-replay baseline read failed for %s: %v (fingerprint will be name+description only)", e.Path, err)
	}
	return abReplayFingerprint(e.Name, e.Meta.Description, body)
}

func computeABReplayScore(candidate *SkillEntry, candidateBody string, baselineBody string, scenarios []SkillScenarioLogEntry) ABReplayResult {
	res := ABReplayResult{ScenariosConsidered: len(scenarios)}
	if candidate == nil || len(scenarios) == 0 {
		return res
	}
	candText := abReplayFingerprint(candidate.Name, candidate.Meta.Description, candidateBody)
	candTokens := tokenizeForSimilarity(candText)
	baseTokens := tokenizeForSimilarity(baselineBody)

	type scored struct {
		task  string
		delta float64
		cand  float64
	}
	scoredTasks := make([]scored, 0, len(scenarios))
	var sumCand, sumBase float64
	for _, sc := range scenarios {
		taskTokens := tokenizeForSimilarity(sc.Task)
		if len(taskTokens) == 0 {
			continue
		}
		c := jaccardSimilarity(candTokens, taskTokens)
		b := jaccardSimilarity(baseTokens, taskTokens)

		if !sc.Success {
			c *= 0.7
			b *= 0.7
		}
		sumCand += c
		sumBase += b
		scoredTasks = append(scoredTasks, scored{task: sc.Task, delta: c - b, cand: c})
	}
	if len(scoredTasks) == 0 {
		return res
	}
	res.CandidateScore = sumCand / float64(len(scoredTasks))
	res.BaselineScore = sumBase / float64(len(scoredTasks))
	res.Delta = res.CandidateScore - res.BaselineScore
	sort.Slice(scoredTasks, func(i, j int) bool { return scoredTasks[i].cand > scoredTasks[j].cand })
	for i, s := range scoredTasks {
		if i >= 3 || s.cand <= 0 {
			break
		}
		task := s.task
		if len([]rune(task)) > 160 {
			task = string([]rune(task)[:160]) + "..."
		}
		res.TopMatchedTasks = append(res.TopMatchedTasks, task)
	}
	return res
}

func abReplayVerdict(res ABReplayResult) string {
	if res.ScenariosConsidered == 0 {
		return "no recent scenarios"
	}
	switch {
	case res.CandidateScore < 0.05:
		return "no observed coverage"
	case res.Delta >= 0.15:
		return "covers recent tasks better than baseline"
	case res.Delta <= -0.15:
		return "baseline already covers these tasks"
	default:
		return "modest overlap with recent tasks"
	}
}
