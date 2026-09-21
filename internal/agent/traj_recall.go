package agent

// Trajectory Efficiency-Learnings Recall — retrieval half of the traj_intel loop.
//
// Research basis: arXiv:2603.10600 ("Trajectory-Informed Memory Generation for
// Self-Improving Agent Systems") defines four components. traj_intel.go
// implements 1-3 (Trajectory Intelligence Extractor, Decision Attribution,
// Contextual Learning Generator) and persists the output to
// .ggcode/trajectory-learnings.jsonl — but nothing ever read that file back,
// leaving it write-only dead data. This file implements component 4, the
// Adaptive Memory Retrieval System: at run start, relevant recent learnings
// are injected into the prompt so efficiency lessons (over-exploration, high
// error rates, context pressure) actually reach the next run.
//
// Design (deterministic, zero LLM cost):
//   - Read the JSONL store (writes are lock+rename atomic, so plain reads are safe)
//   - Drop entries older than trajRecallMaxAge (efficiency advice decays as
//     the codebase evolves)
//   - Collapse per Category, keeping the newest entry — the same lesson stated
//     five times in a row is prompt noise, not reinforcement
//   - Rank by task-token overlap first (task-relevant lessons win), then by
//     recency; unlike ExperienceStore no relevance floor is applied because
//     efficiency lessons are largely task-independent ("explore less, act
//     sooner" applies to any task) and a floor would silence cold stores
//   - Inject at most trajRecallMaxInsights compact lines, next to the
//     experience-recall system message so both ride the same prompt-cache
//     prefix established before the first LLM request.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"
)

const (
	// trajRecallMaxInsights bounds prompt cost; beyond ~3 the marginal
	// guidance decays while noise grows (same rationale as experienceRecallTopK).
	trajRecallMaxInsights = 3

	// trajRecallMaxAge drops stale entries; a month-old "explore less" hint
	// may no longer match the codebase's current reality.
	trajRecallMaxAge = 30 * 24 * time.Hour
)

// trajRecallEntry mirrors trajectoryLearning in traj_intel.go. Kept separate
// so the two sides can evolve independently (recall only needs these fields).
type trajRecallEntry struct {
	Timestamp time.Time `json:"timestamp"`
	Type      string    `json:"type"`
	Task      string    `json:"task"`
	Insight   string    `json:"insight"`
	Category  string    `json:"category"`
}

// scoredTrajRecall pairs an entry with its task-relevance score.
type scoredTrajRecall struct {
	entry trajRecallEntry
	score int
}

// recallTrajectoryInsights retrieves recent efficiency learnings for this
// task and returns a formatted block, or "" when the store is missing, empty,
// or fully stale — callers skip injection entirely so cold projects pay
// nothing.
func (a *Agent) recallTrajectoryInsights(task string) string {
	workingDir := a.WorkingDir()
	if workingDir == "" {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(workingDir, ".ggcode", "trajectory-learnings.jsonl"))
	if err != nil {
		return ""
	}

	cutoff := time.Now().Add(-trajRecallMaxAge)
	byCategory := make(map[string]trajRecallEntry)
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var e trajRecallEntry
		if json.Unmarshal([]byte(line), &e) != nil {
			continue // skip malformed lines, matching traj_intel's lenient reader
		}
		if e.Insight == "" || e.Timestamp.Before(cutoff) {
			continue
		}
		// Category dedup: keep only the newest entry per category.
		key := e.Category
		if key == "" {
			key = e.Type
		}
		if prev, ok := byCategory[key]; ok && !prev.Timestamp.Before(e.Timestamp) {
			continue
		}
		byCategory[key] = e
	}
	if len(byCategory) == 0 {
		return ""
	}

	queryTokens := tiTokenSet(task)
	scored := make([]scoredTrajRecall, 0, len(byCategory))
	for _, e := range byCategory {
		scored = append(scored, scoredTrajRecall{
			entry: e,
			score: tiOverlap(queryTokens, tiTokenSet(e.Task+" "+e.Insight)),
		})
	}
	sort.Slice(scored, func(i, j int) bool {
		if scored[i].score != scored[j].score {
			return scored[i].score > scored[j].score
		}
		return scored[i].entry.Timestamp.After(scored[j].entry.Timestamp)
	})
	if len(scored) > trajRecallMaxInsights {
		scored = scored[:trajRecallMaxInsights]
	}

	var b strings.Builder
	for _, s := range scored {
		b.WriteString("- ")
		if s.entry.Category != "" {
			b.WriteString("[" + s.entry.Category + "] ")
		}
		b.WriteString(strings.TrimSpace(s.entry.Insight))
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// tiTokenSet lowercases and splits on non-alphanumeric runes; CJK runes are
// kept individually. Named with the ti- prefix: the agent package already
// has qcTokenize/tokenizeForRR with different semantics.
func tiTokenSet(s string) map[string]struct{} {
	set := make(map[string]struct{})
	var cur []rune
	flush := func() {
		if len(cur) >= 2 {
			set[string(cur)] = struct{}{}
		}
		cur = cur[:0]
	}
	for _, r := range strings.ToLower(s) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			if r >= 0x2E80 {
				flush()
				set[string(r)] = struct{}{}
			} else {
				cur = append(cur, r)
			}
		default:
			flush()
		}
	}
	flush()
	return set
}

// tiOverlap counts how many query tokens appear in the entry's token set.
func tiOverlap(query, entry map[string]struct{}) int {
	n := 0
	for t := range query {
		if _, ok := entry[t]; ok {
			n++
		}
	}
	return n
}
