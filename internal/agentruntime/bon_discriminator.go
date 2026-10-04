// Package agentruntime: execution-based tie-breaking for best-of-N (r439).
//
// Research basis: "Scaling Agentic Verifier for Competitive Coding" (ICML
// 2026) — when candidate solutions look textually similar, an execution-based
// verifier actively reasons about program behavior and searches for highly
// discriminative test inputs that expose behavioral discrepancies among the
// candidates. Companion line: AgentV-RL (ACL 2026) — verification as a
// multi-turn, tool-augmented deliberation rather than single-turn scoring.
//
// ggcode's BoN ranking (r377) is distilled-summary consensus: pure text
// heuristics, zero execution. When consensus cannot separate the top
// candidates the run degrades to sequential-retry conditioning — even when
// the candidates ARE behaviorally distinguishable by a few checks. This file
// adds the missing half: before degrading, ONE discriminator sub-agent reads
// both top candidates' worktrees, crafts 3-5 discriminating checks (edge
// arguments, error paths, task-contract assertions), executes them in each
// worktree, and reports which candidate behaves correctly.
//
// Safety/non-interference: discrimination only breaks ties. If it is
// inconclusive, fails to parse, errors, or ctx expires, the existing Degraded
// path runs unchanged. The discriminator runs in the PARENT workspace with a
// read/execute-only tool whitelist and never edits either worktree.
package agentruntime

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/subagent"
	"github.com/topcheer/ggcode/internal/tool"
)

// discriminationTimeout bounds the whole tie-break attempt. It runs only
// after N candidates have already finished, on the caller's critical path,
// so it must be clearly cheaper than re-running a candidate.
const discriminationTimeout = 5 * time.Minute

// discriminatorTools is a read/execute-only whitelist. No edit/write tools:
// the discriminator observes both worktrees, it never mutates them.
var discriminatorTools = []string{"run_command", "read_file", "grep", "git_diff"}

// discriminationMarker anchors the machine-readable verdict in the
// discriminator's final message (single line, end of output).
const discriminationMarker = "DISCRIMINATION:"

// verdictWeight orders candidate outcomes for top-2 selection.
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

// pickTiePair selects the two best candidates worth discriminating: highest
// verdict weight first, then non-empty result. Requires both to have an
// isolated worktree to execute checks in. Returns indices into cands, or
// ok=false when fewer than two discriminable candidates exist.
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

// discriminateTie attempts an execution-based tie-break between the top two
// candidates. Returns (winnerIndex, evidence, true) when a candidate was
// demonstrably better; ok=false means inconclusive — caller must fall back
// to the unchanged Degraded path.
func discriminateTie(ctx context.Context, spawner CandidateSpawner, snaps SnapshotSource, cands []CandidateOutcome, opts BestOfNOptions) (int, string, bool) {
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
	snap, done := waitOne(dctx, snaps, id, opts.Poll)
	if !done || snap.Status != subagent.StatusCompleted {
		return 0, "", false
	}
	pick := parseDiscrimination(snap.Result)
	// Provenance tag: which model judged the tie ("" = inherited parent
	// model, the r439 default). Cross-family verification beats
	// same-family (arXiv:2512.02304), so the report must show whether the
	// verdict was actually independent.
	tag := fmt.Sprintf("[verifier=%s] ", vmOrInherited(opts.verifierModelFor(cands[a], cands[b])))
	switch pick {
	case "A":
		return a, tag + clip(snap.Result, 2000), true
	case "B":
		return b, tag + clip(snap.Result, 2000), true
	}
	return 0, "", false // inconclusive or unparsed
}

func vmOrInherited(m string) string {
	if m == "" {
		return "inherited"
	}
	return m
}

// parseDiscrimination extracts the winner from the marker line. Scans the
// last 5 lines (the contract says final line; tolerant for trailing prose).
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
