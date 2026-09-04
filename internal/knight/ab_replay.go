package knight

import (
	"sort"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
)

// ABReplayResult captures a deterministic, LLM-free relevance score between a
// candidate skill body and a set of recently observed prompt scenarios.
//
// The intuition: if Knight stages a candidate skill that does not actually
// resemble any task users have asked the agent to do recently, promoting it
// adds noise to the prompt without any payoff. Conversely, candidates that
// strongly overlap with real recent scenarios are more likely to improve
// agent behavior when promoted.
//
// CandidateScore and BaselineScore are normalized in [0,1]. Delta is
// CandidateScore - BaselineScore (range [-1,1]); positive Delta means the
// candidate covers a wider/more frequent slice of recent reality than the
// baseline (e.g., the active skill it would replace).
type ABReplayResult struct {
	ScenariosConsidered int
	CandidateScore      float64
	BaselineScore       float64
	Delta               float64
	TopMatchedTasks     []string
}

// abReplayFingerprint builds the similarity fingerprint text from a skill's
// name, description, and body. Both the candidate side and the baseline side
// of the A/B replay must use the same construction so Delta is not skewed by
// trigger words present in only one side's name/description (issue #977).
func abReplayFingerprint(name, description, body string) string {
	return strings.Join([]string{name, description, body}, "\n")
}

// baselineReplayBody builds the baseline fingerprint text for an active
// skill entry: name + description + file body, mirroring abReplayFingerprint
// used for the candidate. Name and description are kept even when the body
// cannot be read so the A/B comparison stays symmetric.
func baselineReplayBody(e *SkillEntry) string {
	if e == nil {
		return ""
	}
	body := ""
	if b, err := readSkillContent(e.Path); err == nil {
		body = string(b)
	} else {
		// #1573-B: a failed baseline read silently produced a name+description
		// -only fingerprint (the #977 partial) - every delta against it is
		// inflated and feeds the eval log/LLM rationale, biasing promotion.
		// Log it so the asymmetry (candidate side aborts on read failure)
		// is at least observable.
		debug.Log("knight", "ab-replay baseline read failed for %s: %v (fingerprint will be name+description only)", e.Path, err)
	}
	return abReplayFingerprint(e.Name, e.Meta.Description, body)
}

// computeABReplayScore evaluates how well candidateBody (plus name and
// description) matches the recent scenario tasks compared to baselineBody.
//
// baselineBody may be empty, in which case BaselineScore is 0 and Delta equals
// CandidateScore (i.e., we compare against "no skill" as the baseline).
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
		// Penalize candidates that "match" failed scenarios — those are the
		// scenarios where the relevant skill (if any) did not save us.
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

// abReplayVerdict converts a replay result into a short, deterministic verdict
// string used both in eval logs and in stale-skill / promotion decisions.
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
