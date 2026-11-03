package agentruntime

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/subagent"
	"github.com/topcheer/ggcode/internal/tool"
)

const discriminationTimeout = 5 * time.Minute

var discriminatorTools = []string{"run_command", "read_file", "grep", "git_diff"}

const discriminationMarker = "DISCRIMINATION:"

func verdictWeight(v string) int {
	switch v {
	case "succeeded":
		return 3
	case "partial":
		return 2
	case "unknown":
		return 1
	}
	return 0
}

func pickTiePair(cands []CandidateOutcome) (int, int, bool) {
	first, second := -1, -1
	for i, c := range cands {
		if c.Worktree == "" || strings.TrimSpace(c.Result) == "" {
			continue
		}
		w := verdictWeight(c.Verdict)
		if w == 0 {
			continue
		}
		if first == -1 || w > verdictWeight(cands[first].Verdict) {
			second = first
			first = i
		} else if second == -1 || w > verdictWeight(cands[second].Verdict) {
			second = i
		}
	}
	if first == -1 || second == -1 {
		return 0, 0, false
	}
	return first, second, true
}

func discriminateTie(ctx context.Context, spawner CandidateSpawner, snaps SnapshotSource, cands []CandidateOutcome, opts BestOfNOptions) (int, string, bool) {
	poll := opts.Poll
	if poll <= 0 {

		poll = bestOfNDefaultPoll
	}
	a, b, ok := pickTiePair(cands)
	if !ok {
		return 0, "", false
	}
	dctx, cancel := context.WithTimeout(ctx, discriminationTimeout)
	defer cancel()

	task := opts.Task
	taskText := fmt.Sprintf(`You are a discrimination verifier for two candidate solutions that ranked too close to separate by text consensus (execution-based tie-break).

Original task:
%s

Candidate A worktree: %s
Candidate B worktree: %s

Steps:
1. Read each candidate's change (git_diff or read key files) in BOTH worktrees.
2. Design 3-5 DISCRIMINATING checks that would expose behavioral differences between the two implementations: edge-case arguments, error-path inputs, assertions central to the task contract. Cheap, focused checks only (compile, targeted test, one-shot script).
3. Execute the SAME checks in BOTH worktrees with run_command (cd <worktree> first).
4. Compare behavior. Pick the winner ONLY if it passes checks the other fails; ties, both-fail, or both-pass are inconclusive.

You must NOT edit either worktree. End your final message with exactly one line:
%s winner=A   (or winner=B, or winner=inconclusive)`,
		task, cands[a].Worktree, cands[b].Worktree, discriminationMarker)

	id, _, err := spawner.Launch(dctx, tool.LaunchOptions{
		Name:        "bon-discriminator",
		Task:        taskText,
		DisplayTask: "best_of_n execution tie-break",
		Tools:       discriminatorTools,
		Model:       opts.verifierModelFor(cands[a], cands[b]),
	})
	if err != nil {
		return 0, "", false
	}
	snap, done := waitOne(dctx, snaps, id, poll)
	if !done || snap.Status != subagent.StatusCompleted {
		return 0, "", false
	}
	pick := parseDiscrimination(snap.Result)

	tag := fmt.Sprintf("[verifier=%s] ", vmOrInherited(opts.verifierModelFor(cands[a], cands[b])))
	switch pick {
	case "A":
		return a, tag + clip(snap.Result, 2000), true
	case "B":
		return b, tag + clip(snap.Result, 2000), true
	}
	return 0, "", false
}

func vmOrInherited(m string) string {
	if m == "" {
		return "inherited"
	}
	return m
}

func parseDiscrimination(out string) string {
	lines := strings.Split(strings.TrimRight(out, "\n "), "\n")
	start := len(lines) - 5
	if start < 0 {
		start = 0
	}
	for i := len(lines) - 1; i >= start; i-- {
		l := strings.TrimSpace(lines[i])
		if !strings.HasPrefix(l, discriminationMarker) {
			continue
		}
		rest := strings.TrimSpace(strings.TrimPrefix(l, discriminationMarker))
		rest = strings.ToLower(rest)
		switch {
		case strings.Contains(rest, "winner=a"):
			return "A"
		case strings.Contains(rest, "winner=b"):
			return "B"
		case strings.Contains(rest, "inconclusive"):
			return "inconclusive"
		}
	}
	return ""
}
