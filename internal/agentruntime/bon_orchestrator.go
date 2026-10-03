// Package agentruntime: best-of-N trajectory-level parallel sampling (r377).
//
// Research basis: "Scaling Test-time Compute for LLM Agents" (arXiv
// 2506.12928) — parallel Best-of-N sampling with verifier/consensus
// selection, sequential revision fallback (RTV), and Codex CLI rollout
// practice (rollout summary + retry): short-output BoN picks a winner;
// long agent trajectories need distilled per-rollout summaries before they
// can be compared or merged.
//
// ggcode already had every part (spawn pipeline, worktree isolation,
// SummarizeTrajectory/RankSubagentResults consensus ranking,
// DistillIntoPrompt conditioning) but no orchestration layer driving them
// for one task. RunBestOfN is that driver: launch N independent candidates
// on the same task, poll them to terminal state, build each candidate's
// trajectory, rank by distilled-summary consensus, and on no-winner degrade
// to a sequential-retry conditioning hint (the paper's escalation chain).
package agentruntime

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/subagent"
	"github.com/topcheer/ggcode/internal/tool"
)

// BestOfN limits: candidates are hard-capped at 4 — beyond the paper's
// useful parallel band, and each candidate occupies a concurrency slot that
// the parent's own tool calls also need (16-slot budget shared session-wide).
const (
	bestOfNMinCandidates = 2
	bestOfNMaxCandidates = 4
	bestOfNDefaultPoll   = 2 * time.Second
	bestOfNMaxSlots      = 16
)

// CandidateSpawner launches one candidate run. Production implementation is
// tool.SpawnAgentTool (via Launch); tests inject a fake.
type CandidateSpawner interface {
	Launch(ctx context.Context, opts tool.LaunchOptions) (id string, worktree string, err error)
}

// SnapshotSource polls candidate state. Production implementation is
// *subagent.Manager; tests inject a fake.
type SnapshotSource interface {
	Snapshot(id string) (subagent.Snapshot, bool)
	RunningCount() int
}

// CandidateCanceller rolls back one running candidate. Production
// *subagent.Manager implements it (Manager.Cancel); it is an optional
// capability of SnapshotSource so test fakes can omit it; when absent the
// abort path still exposes every launched ID so the parent can clean up.
type CandidateCanceller interface {
	Cancel(id string) bool
}

// BestOfNOptions configures one best-of-N run.
type BestOfNOptions struct {
	Task      string   // the shared task contract
	N         int      // candidate count (clamped 2..4)
	Tools     []string // optional tool whitelist for candidates
	Isolation string   // "worktree" (recommended) or "none"
	Name      string   // activity label base shown in the UI
	Poll      time.Duration
	Models    []string // optional per-candidate model override (r380 cross-model ensemble)
}

// modelFor returns the model for the 1-based candidate index i. With no
// Models configured every candidate inherits the parent runtime model
// (the r377 same-model behavior). When set, models map to candidates in
// order and cycle when fewer models than candidates are given — a
// heterogeneous ensemble (Mixture-of-Models, 2026) decorrelates candidate
// errors so distilled-summary consensus has genuinely independent votes
// to rank instead of N copies of the same model's failure modes.
func (o BestOfNOptions) modelFor(i int) string {
	if len(o.Models) == 0 {
		return ""
	}
	return o.Models[(i-1)%len(o.Models)]
}

// CandidateOutcome is one candidate's distilled result.
type CandidateOutcome struct {
	Name     string
	ID       string
	Status   string
	Verdict  string // from RolloutSummary: succeeded | partial | failed (or "unknown")
	Worktree string
	Model    string // per-candidate model (r380); "" = inherited parent model
	Result   string // final result text (truncated in Report)
	Error    string
}

// BestOfNReport is the full outcome of one best-of-N run.
type BestOfNReport struct {
	N                int
	Candidates       []CandidateOutcome
	WinnerIndex      int // -1 when no winner (degraded or partial)
	Degraded         bool
	ConditioningHint string // RTV sequential-retry conditioning when Degraded
	Partial          bool   // true when the caller ctx expired before all terminal
	// r439 execution tie-break: true when the winner was picked by an
	// execution-based discriminator sub-agent (not text consensus).
	Discriminated bool
	Evidence      string // discriminator report (present when Discriminated)
	Report        string
	Err           string // orchestration-level error (gate failure, launch failure)
}

// candidateTaskSuffix standardizes what each candidate's final message must
// contain so SummarizeTrajectory sees comparable verdicts across rollouts.
const candidateTaskSuffix = "\n\nYou are one of %d INDEPENDENT parallel candidates attempting the same task. Work self-sufficiently: no coordination, no assumptions about other runs. Before finishing, run the narrowest verification you can (targeted build/test/grep) and state its outcome explicitly in your final message (e.g. \"go build ./pkg/ passes; tests X,Y green\")."

// RunBestOfN launches N candidates on the same task, waits for all to reach
// a terminal state, and picks a winner by distilled-summary consensus.
// Context cancellation does NOT cancel the candidates (they keep running on
// the manager lifecycle); the report comes back Partial with live IDs.
func RunBestOfN(ctx context.Context, spawner CandidateSpawner, snaps SnapshotSource, opts BestOfNOptions) BestOfNReport {
	rep := BestOfNReport{WinnerIndex: -1, N: opts.N}
	if strings.TrimSpace(opts.Task) == "" {
		rep.Err = "best_of_n: task is required"
		rep.Report = rep.Err
		return rep
	}
	n := opts.N
	if n < bestOfNMinCandidates {
		rep.Err = fmt.Sprintf("best_of_n: n must be >= %d (got %d)", bestOfNMinCandidates, n)
		rep.Report = rep.Err
		return rep
	}
	if n > bestOfNMaxCandidates {
		n = bestOfNMaxCandidates
		opts.N = n
	}
	rep.N = n

	// Concurrency gate: reserve all n slots up front. A partial fan-out that
	// dies mid-batch on the limit is worse than a clean refusal.
	if rc := snaps.RunningCount(); rc+n > bestOfNMaxSlots {
		rep.Err = fmt.Sprintf("best_of_n: needs %d free sub-agent slots but only %d of %d are free (currently running: %d). Wait for existing runs to finish or lower n.", n, bestOfNMaxSlots-rc, bestOfNMaxSlots, rc)
		rep.Report = rep.Err
		return rep
	}

	isolation := opts.Isolation
	if isolation == "" {
		isolation = "worktree" // default: candidates edit files; shared cwd = corruption
	}
	poll := opts.Poll
	if poll <= 0 {
		poll = bestOfNDefaultPoll
	}
	name := strings.TrimSpace(opts.Name)
	if name == "" {
		name = "best-of-N"
	}

	type launched struct {
		id       string
		worktree string
		model    string
	}
	var ids []launched
	taskText := opts.Task + fmt.Sprintf(candidateTaskSuffix, n)
	for i := 1; i <= n; i++ {
		model := opts.modelFor(i)
		id, wt, err := spawner.Launch(ctx, tool.LaunchOptions{
			Name:        fmt.Sprintf("%s candidate %d/%d", name, i, n),
			Task:        taskText,
			DisplayTask: fmt.Sprintf("%s candidate %d/%d", name, i, n),
			Tools:       opts.Tools,
			Model:       model,
			Isolation:   isolation,
		})
		if err != nil {
			rep.Err = fmt.Sprintf("best_of_n: candidate %d/%d failed to launch: %v", i, n, err)
			// #3070: candidates already in flight must not become orphans.
			// Roll them back via Manager.Cancel when the snapshot source
			// supports it, and ALWAYS record them in rep.Candidates with
			// their IDs/worktrees so the report exposes what is still live
			// (or was cancelled) for parent-side cleanup.
			cl, _ := snaps.(CandidateCanceller)
			for j, l := range ids {
				status := "orphaned"
				if cl != nil && cl.Cancel(l.id) {
					status = string(subagent.StatusCancelled)
				}
				rep.Candidates = append(rep.Candidates, CandidateOutcome{
					Name:     fmt.Sprintf("%s candidate %d/%d", name, j+1, n),
					ID:       l.id,
					Status:   status,
					Worktree: l.worktree,
					Model:    l.model,
					Error:    "aborted: a later candidate failed to launch",
				})
			}
			rep.Report = fmt.Sprintf("Aborted after %d/%d candidates launched. %s\nLaunched candidates (cancelled or exposed for cleanup):\n%s", len(ids), n, rep.Err, formatCandidateLines(rep.Candidates))
			return rep
		}
		ids = append(ids, launched{id: id, worktree: wt, model: model})
	}

	// Poll all candidates to terminal state. ctx cancellation yields a
	// Partial report; candidates are NOT cancelled (parent can wait_agent).
	terminal := make([]bool, n)
	var snapsFinal []subagent.Snapshot
	ticker := time.NewTicker(poll)
	defer ticker.Stop()
	for {
		allDone := true
		snapsFinal = make([]subagent.Snapshot, n)
		for i, l := range ids {
			if terminal[i] {
				continue
			}
			s, ok := snaps.Snapshot(l.id)
			if !ok {
				terminal[i] = true // vanished from the manager: treat as failed
				snapsFinal[i] = subagent.Snapshot{ID: l.id, Status: subagent.StatusFailed, Error: "sub-agent not found"}
				continue
			}
			switch s.Status {
			case subagent.StatusCompleted, subagent.StatusFailed, subagent.StatusCancelled:
				terminal[i] = true
			default:
				allDone = false
			}
			snapsFinal[i] = s
		}
		if allDone {
			break
		}
		select {
		case <-ctx.Done():
			rep.Partial = true
			rep.N = n
			for i, l := range ids {
				st := string(subagent.StatusRunning)
				if terminal[i] {
					st = string(snapsFinal[i].Status)
				}
				rep.Candidates = append(rep.Candidates, CandidateOutcome{
					Name: fmt.Sprintf("%s candidate %d/%d", name, i+1, n), ID: l.id, Status: st, Verdict: "unknown", Worktree: l.worktree, Model: l.model,
				})
			}
			rep.Report = fmt.Sprintf("best_of_n: caller context expired before all %d candidates finished (partial). Candidates keep running; poll with wait_agent/list_agents:\n%s",
				n, formatCandidateLines(rep.Candidates))
			return rep
		case <-ticker.C:
		}
	}

	// Build trajectories and rank.
	cands := make([]RolloutCandidate, 0, n)
	for i, s := range snapsFinal {
		rep.Candidates = append(rep.Candidates, CandidateOutcome{
			Name:     fmt.Sprintf("%s candidate %d/%d", name, i+1, n),
			ID:       s.ID,
			Status:   string(s.Status),
			Worktree: ids[i].worktree,
			Model:    ids[i].model,
			Result:   s.Result,
			Error:    s.Error,
		})
		cands = append(cands, RolloutCandidate{
			Name:       fmt.Sprintf("candidate-%d", i+1),
			Transcript: trajectoryFromSnapshot(s),
		})
	}

	winner, wsum, ok := RankSubagentResults(cands)
	for i := range rep.Candidates {
		rep.Candidates[i].Verdict = verdictOf(summarizeAt(i, cands))
	}
	// A "winner" that did not succeed (best-of-a-bad-lot) is not usable:
	// RTV escalation says degrade to sequential retry with conditioning
	// distilled from every rollout instead of reporting a failed winner.
	if ok && wsum.Verdict != "succeeded" {
		ok = false
	}
	if ok {
		for i, c := range cands {
			if c.Name == winner.Name {
				rep.WinnerIndex = i
				break
			}
		}
	} else if idx, ev, dok := discriminateTie(ctx, spawner, snaps, rep.Candidates, opts.Task, poll); dok {
		// r439: text consensus could not separate the candidates — try an
		// execution-based tie-break (discriminator sub-agent runs the same
		// discriminating checks in both top worktrees) before degrading.
		rep.WinnerIndex = idx
		rep.Discriminated = true
		rep.Evidence = ev
	} else {
		// No distinguishable winner: degrade to the sequential-retry path —
		// distill ALL rollouts into one conditioning block (RTV escalation).
		rep.Degraded = true
		sums := make([]RolloutSummary, 0, n)
		for _, c := range cands {
			sums = append(sums, SummarizeTrajectory(c.Transcript))
		}
		rep.ConditioningHint = DistillIntoPrompt(sums)
	}
	rep.Report = formatBestOfNReport(rep, winnerSummary(wsum, ok))
	return rep
}

// summarizeAt recomputes the summary for candidate i (used to fill per-
// candidate verdicts after ranking already distilled them once; kept simple
// rather than threading summaries out of RankSubagentResults).
func summarizeAt(i int, cands []RolloutCandidate) RolloutSummary {
	return SummarizeTrajectory(cands[i].Transcript)
}

func verdictOf(s RolloutSummary) string {
	v := strings.TrimSpace(s.Verdict)
	if v == "" {
		return "unknown"
	}
	return v
}

func winnerSummary(wsum RolloutSummary, ok bool) string {
	if !ok {
		return ""
	}
	return wsum.Verdict
}

// trajectoryFromSnapshot converts one candidate's event log + final state
// into the TrajectoryEntry stream SummarizeTrajectory consumes.
func trajectoryFromSnapshot(s subagent.Snapshot) []TrajectoryEntry {
	entries := make([]TrajectoryEntry, 0, len(s.Events)+2)
	for _, ev := range s.Events {
		switch {
		case ev.Type == subagent.AgentEventToolCall:
			entries = append(entries, TrajectoryEntry{Kind: "action", Text: fmt.Sprintf("%s %s", ev.ToolName, clip(ev.ToolArgs, 160))})
		case ev.Type == subagent.AgentEventToolResult:
			kind := "observation"
			if ev.IsError {
				kind = "error"
			}
			entries = append(entries, TrajectoryEntry{Kind: kind, Text: clip(fmt.Sprintf("%s: %s", ev.ToolName, ev.Result), 220)})
		}
	}
	if s.Status == subagent.StatusCompleted && s.Result != "" {
		entries = append(entries, TrajectoryEntry{Kind: "result", Text: clip(s.Result, 800)})
	}
	if s.Error != "" {
		entries = append(entries, TrajectoryEntry{Kind: "error", Text: clip(s.Error, 300)})
	}
	if len(entries) == 0 {
		entries = append(entries, TrajectoryEntry{Kind: "note", Text: "no trajectory events recorded"})
	}
	return entries
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

func formatCandidateLines(cs []CandidateOutcome) string {
	var b strings.Builder
	for _, c := range cs {
		line := fmt.Sprintf("- %s [%s] id=%s", c.Name, c.Status, c.ID)
		if c.Model != "" {
			line += fmt.Sprintf(" model=%s", c.Model)
		}
		if c.Worktree != "" {
			line += fmt.Sprintf(" worktree=%s", c.Worktree)
		}
		b.WriteString(line + "\n")
	}
	return b.String()
}

// BestOfNRunnerFor adapts a SpawnAgentTool + manager pair into the runner
// closure injected into tool.BestOfNTool at registration. This is the
// cycle-breaker: internal/tool cannot import internal/agentruntime, so the
// registration sites (agentruntime/subsystems.go, tui/repl.go) wire it.
func BestOfNRunnerFor(sp CandidateSpawner, snaps SnapshotSource) func(context.Context, tool.BestOfNRequest) string {
	return func(ctx context.Context, req tool.BestOfNRequest) string {
		rep := RunBestOfN(ctx, sp, snaps, BestOfNOptions{
			Task:      req.Task,
			N:         req.N,
			Tools:     req.Tools,
			Isolation: req.Isolation,
			Name:      req.Name,
			Models:    req.Models,
		})
		return rep.Report
	}
}

func formatBestOfNReport(rep BestOfNReport, winnerVerdict string) string {
	var b strings.Builder
	if rep.Err != "" {
		return rep.Report
	}
	fmt.Fprintf(&b, "best_of_n: %d candidates ran to completion.\n", rep.N)
	b.WriteString(formatCandidateLines(rep.Candidates))
	switch {
	case rep.WinnerIndex >= 0:
		w := rep.Candidates[rep.WinnerIndex]
		if rep.Discriminated {
			fmt.Fprintf(&b, "\nWinner: %s (id=%s, verdict=execution-discriminated: picked by checks the other candidate failed).\n", w.Name, w.ID)
			if rep.Evidence != "" {
				fmt.Fprintf(&b, "\nDiscriminator evidence:\n%s\n", clip(rep.Evidence, 1500))
			}
		} else {
			fmt.Fprintf(&b, "\nWinner: %s (id=%s, verdict=%q).\n", w.Name, w.ID, winnerVerdict)
		}
		if w.Worktree != "" {
			fmt.Fprintf(&b, "Winner's isolated worktree: %s — inspect and merge from there; the other worktrees are disposable.\n", w.Worktree)
		}
		if w.Result != "" {
			fmt.Fprintf(&b, "\nWinner's result:\n%s\n", clip(w.Result, 2000))
		}
	case rep.Degraded:
		b.WriteString("\nNo distinguishable winner (no consensus among distilled summaries). Sequential-retry conditioning (distilled from ALL rollouts):\n")
		b.WriteString(clip(rep.ConditioningHint, 2000))
		b.WriteString("\nRetry the task once yourself with this conditioning appended, or inspect the candidate worktrees individually.")
	}
	return b.String()
}
