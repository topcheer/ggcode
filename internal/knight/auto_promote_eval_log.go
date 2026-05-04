package knight

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"time"
)

type AutoPromoteEvalLogEntry struct {
	Time                   time.Time `json:"time"`
	Skill                  string    `json:"skill"`
	Scope                  string    `json:"scope"`
	Path                   string    `json:"path"`
	Allowed                bool      `json:"allowed"`
	Promote                bool      `json:"promote"`
	ReplayPass             bool      `json:"replay_pass"`
	SavedReplayRequired    bool      `json:"saved_replay_required,omitempty"`
	SavedReplayStatus      string    `json:"saved_replay_status,omitempty"`
	FalsePositiveCount     int       `json:"false_positive_count,omitempty"`
	FalseNegativeCount     int       `json:"false_negative_count,omitempty"`
	BaselineReplayRequired bool      `json:"baseline_replay_required,omitempty"`
	BaselineReplayStatus   string    `json:"baseline_replay_status,omitempty"`
	OverlapCount           int       `json:"overlap_count,omitempty"`
	RuleOverlap            bool      `json:"rule_overlap,omitempty"`
	RuleOverlapJaccard     float64   `json:"rule_overlap_jaccard,omitempty"`
	RuleOverlapWith        string    `json:"rule_overlap_with,omitempty"`
	ReplayCandidateScore   float64   `json:"replay_candidate_score,omitempty"`
	ReplayBaselineScore    float64   `json:"replay_baseline_score,omitempty"`
	ReplayDelta            float64   `json:"replay_delta,omitempty"`
	ReplayVerdict          string    `json:"replay_verdict,omitempty"`
	ReplayScenarios        int       `json:"replay_scenarios,omitempty"`
	Rationale              string    `json:"rationale,omitempty"`
	RawOutput              string    `json:"raw_output,omitempty"`
	FailureMode            string    `json:"failure_mode,omitempty"`
}

func (k *Knight) appendAutoPromoteEval(entry *SkillEntry, decision autoPromoteEvalDecision) {
	if k == nil || entry == nil {
		return
	}
	logPath := filepath.Join(k.projDir, ".ggcode", "skill-auto-promote-evals.jsonl")
	if err := os.MkdirAll(filepath.Dir(logPath), 0755); err != nil {
		return
	}
	line, err := json.Marshal(AutoPromoteEvalLogEntry{
		Time:                   time.Now(),
		Skill:                  entry.Name,
		Scope:                  entry.Scope,
		Path:                   entry.Path,
		Allowed:                decision.Allowed(),
		Promote:                decision.Promote,
		ReplayPass:             decision.ReplayPassed,
		SavedReplayRequired:    decision.SavedReplayRequired,
		SavedReplayStatus:      decision.SavedReplayStatus,
		FalsePositiveCount:     decision.FalsePositiveCount,
		FalseNegativeCount:     decision.FalseNegativeCount,
		BaselineReplayRequired: decision.BaselineReplayRequired,
		BaselineReplayStatus:   decision.BaselineReplayStatus,
		OverlapCount:           decision.OverlapCount,
		RuleOverlap:            decision.RuleOverlap,
		RuleOverlapJaccard:     decision.RuleOverlapJaccard,
		RuleOverlapWith:        decision.RuleOverlapWith,
		ReplayCandidateScore:   decision.ReplayCandidateScore,
		ReplayBaselineScore:    decision.ReplayBaselineScore,
		ReplayDelta:            decision.ReplayDelta,
		ReplayVerdict:          decision.ReplayVerdict,
		ReplayScenarios:        decision.ReplayScenarios,
		Rationale:              decision.Rationale,
		RawOutput:              decision.RawOutput,
		FailureMode:            decision.FailureMode,
	})
	if err != nil {
		return
	}
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write(append(line, '\n'))
}

func (k *Knight) RecentAutoPromoteEvals(limit int) ([]AutoPromoteEvalLogEntry, error) {
	if k == nil {
		return nil, nil
	}
	logPath := filepath.Join(k.projDir, ".ggcode", "skill-auto-promote-evals.jsonl")
	f, err := os.Open(logPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()

	var entries []AutoPromoteEvalLogEntry
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var entry AutoPromoteEvalLogEntry
		if err := json.Unmarshal(line, &entry); err != nil {
			continue
		}
		entries = append(entries, entry)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	sort.SliceStable(entries, func(i, j int) bool {
		return entries[i].Time.After(entries[j].Time)
	})
	if limit > 0 && len(entries) > limit {
		entries = entries[:limit]
	}
	return entries, nil
}

func (k *Knight) RecentAutoPromoteEvalsForSkill(scope, name string, limit int) ([]AutoPromoteEvalLogEntry, error) {
	entries, err := k.RecentAutoPromoteEvals(0)
	if err != nil {
		return nil, err
	}
	var filtered []AutoPromoteEvalLogEntry
	for _, entry := range entries {
		if entry.Scope == scope && entry.Skill == name {
			filtered = append(filtered, entry)
			if limit > 0 && len(filtered) >= limit {
				break
			}
		}
	}
	return filtered, nil
}
