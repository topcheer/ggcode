// Package agentruntime: best-of-N trajectory-level parallel sampling (r377).
//
// Research basis: "Scaling Test-time Compute for LLM Agents" (arXiv
// 2506.12928) - parallel Best-of-N sampling with verifier/consensus
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
//
// Shared-error correlation sentinel (r478, "Phase Transition for Budgeted
// Multi-Agent Synergy"): that theory names shared-error correlation as a
// key predictor of when multi-agent ensembles collapse - when every
// candidate makes the SAME mistake, consensus ranking confidently picks
// that mistake. Existing mechanisms are orthogonal: heterogeneous models
// (r380) decorrelate by DESIGN but never MEASURE it; the r439 discriminator
// resolves candidates that are TOO CLOSE to rank, not candidates that are
// uniformly wrong; the Degraded path fires on no-consensus (disagreement).
// Nobody watches over-agreement. The sentinel measures pairwise similarity
// of successful candidates' results and flags near-identical outputs as
// doubtful ensemble gain (suspected shared error), recommending a
// third-family verifier instead.
package agentruntime

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/subagent"
	"github.com/topcheer/ggcode/internal/tool"
)

// BestOfN limits: candidates are hard-capped at 4 - beyond the paper's
// useful parallel band, and each candidate occupies a concurrency slot that
// the parent's own tool calls also need (16-slot budget shared session-wide).
const (
	bestOfNMinCandidates = 2
	bestOfNMaxCandidates = 4
	bestOfNDefaultPoll   = 2 * time.Second
	bestOfNMaxSlots      = 16
	// bonSharedErrorSimWarn is the mean pairwise Jaccard similarity of the
	// successful candidates' results above which the ensemble is flagged as
	// near-identical (suspected shared error; ensemble gain doubtful).
	bonSharedErrorSimWarn = 0.85
	// bonSimClip caps each result's contribution to the shingle set so one
	// verbose candidate cannot dominate the similarity estimate.
	bonSimClip = 2000
	// bonSimShingleWords is the word-shingle window size for similarity.
	bonSimShingleWords = 5
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
	// VerifierModels optionally routes the r439 tie-break discriminator to
	// a model DIFFERENT from the tied candidates. Research: verification
	// across model families beats self-verification, and the benefit
	// shrinks as solver and verifier converge (arXiv:2512.02304) - the
	// discriminator judging two same-model candidates is the exact
	// same-family verification the paper flags as weakest. Empty = inherit
	// the parent model (the r439 behavior).
	VerifierModels []string

	// r441 auction yield: when true, once the first candidate reaches
	// StatusCompleted, still-running candidates whose observed tool-call
	// spend exceeds bestOfNYieldFactor x the leader's spend are cancelled
	// so their remaining budget returns to the pool (contract-net style
	// early close). Default false: full-diversity behavior unchanged.
	YieldOnFirstSuccess bool
}

// bestOfNYieldFactor: a laggard that has already burned this multiple of
// the first-successful candidate's tool-call spend is judged a lost cause;
// the auction closes and its remaining budget returns to the pool.
const bestOfNYieldFactor = 1.5

// yieldLaggards is the first-success auction close: given the leader's
// observed tool-call spend and the still-running candidates' spends, it
// returns the IDs that should be cancelled. Pure and deterministic.
func yieldLaggards(leaderSpend int, laggards map[string]int) []string {
	if leaderSpend <= 0 || len(laggards) == 0 {
		return nil
	}
	var out []string
	for id, spend := range laggards {
		if float64(spend) > bestOfNYieldFactor*float64(leaderSpend) {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}

// modelFor returns the model for the 1-based candidate index i. With no
// Models configured every candidate inherits the parent runtime model
// (the r377 same-model behavior). When set, models map to candidates in
// order and cycle when fewer models than candidates are given - a
// heterogeneous ensemble (Mixture-of-Models, 2026) decorrelates candidate
// errors so distilled-summary consensus has genuinely independent votes
// to rank instead of N copies of the same model's failure modes.
func (o BestOfNOptions) modelFor(i int) string {
	if len(o.Models) == 0 {
		return ""
	}
	return o.Models[(i-1)%len(o.Models)]
}

// verifierModelFor picks the discriminator's model from VerifierModels,
// preferring one that differs from BOTH tied candidates' models so the
// tie-break verdict comes from an independent model family (2512.02304).
// Explicit user configuration wins: if every listed verifier model equals
// a candidate model, the first entry is still used. Empty list = ""
// (inherit parent model, unchanged r439 behavior).
func (o BestOfNOptions) verifierModelFor(a, b CandidateOutcome) string {
	if len(o.VerifierModels) == 0 {
		return ""
	}
	for _, m := range o.VerifierModels {
		if m != a.Model && m != b.Model {
			return m
		}
	}
	return o.VerifierModels[0]
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
	// r478 shared-error sentinel: HighCorrelation is true when the successful
	// candidates' results were near-identical (mean pairwise similarity >=
	// bonSharedErrorSimWarn); AvgSimilarity carries the measurement.
	HighCorrelation bool
	AvgSimilarity   float64
	Report          string
	Err             string // orchestration-level error (gate failure, launch failure)
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
	// #3288: allocated ONCE before the poll loop. Candidates finish at
	// staggered times (the norm in real parallel runs); re-making the slice
	// each iteration zeroed earlier-terminal candidates' final snapshots,
	// degrading ranking to empty trajectories, blanking winner result text,
	// and breaking the auction-close leader baseline. Terminal slots are
	// never rewritten, so each candidate's last snapshot survives to the
	// ranking/report phase.
	snapsFinal := make([]subagent.Snapshot, n)
	canceller, _ := snaps.(CandidateCanceller) // r441 auction-close support
	yieldClosed := false                       // at most one close per run
	ticker := time.NewTicker(poll)
	defer ticker.Stop()
	for {
		allDone := true
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
		// r441 opt-in auction close: once one candidate has completed,
		// laggards that already burned >1.5x the leader's tool-call spend
		// are unlikely to finish cheaper - cancel them and reclaim the
		// budget (no-op unless YieldOnFirstSuccess; at most one close per
		// run so a fresh success after the close does not re-trigger).
		if opts.YieldOnFirstSuccess && canceller != nil && !yieldClosed {
			var leaderSpend = -1
			laggards := make(map[string]int)
			for i, s := range snapsFinal {
				if terminal[i] {
					if s.Status == subagent.StatusCompleted && (leaderSpend < 0 || s.ToolCallCount < leaderSpend) {
						leaderSpend = s.ToolCallCount
					}
				} else {
					laggards[ids[i].id] = s.ToolCallCount
				}
			}
			if leaderSpend >= 0 {
				for _, id := range yieldLaggards(leaderSpend, laggards) {
					if canceller.Cancel(id) {
						rep.Evidence += fmt.Sprintf("[auction-close] candidate %s yielded (spend > %.1fx leader); budget reclaimed\n", id, bestOfNYieldFactor)
					}
				}
				yieldClosed = true
			}
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
	} else if idx, ev, dok := discriminateTie(ctx, spawner, snaps, rep.Candidates, opts); dok {
		// r439: text consensus could not separate the candidates - try an
		// execution-based tie-break (discriminator sub-agent runs the same
		// discriminating checks in both top worktrees) before degrading.
		rep.WinnerIndex = idx
		rep.Discriminated = true
		rep.Evidence = ev
	} else {
		// No distinguishable winner: degrade to the sequential-retry path -
		// distill ALL rollouts into one conditioning block (RTV escalation).
		rep.Degraded = true
		sums := make([]RolloutSummary, 0, n)
		for _, c := range cands {
			sums = append(sums, SummarizeTrajectory(c.Transcript))
		}
		rep.ConditioningHint = DistillIntoPrompt(sums)
	}
	// r478: measure shared-error correlation among SUCCESSFUL candidates.
	// Failures ("<error>" strings) would inflate similarity meaninglessly.
	if sim, okSim := pairwiseResultSimilarity(successfulResults(rep.Candidates)); okSim {
		rep.AvgSimilarity = sim
		rep.HighCorrelation = sim >= bonSharedErrorSimWarn
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
			Task:           req.Task,
			N:              req.N,
			Tools:          req.Tools,
			Isolation:      req.Isolation,
			Name:           req.Name,
			Models:         req.Models,
			VerifierModels: req.VerifierModels,
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
		if rep.HighCorrelation {
			fmt.Fprintf(&b, "\nWarning: candidates are near-identical (avg pairwise similarity=%.2f): ensemble gain doubtful - suspected shared error. Consider verifying this result with a third-family model before trusting it.\n", rep.AvgSimilarity)
		}
		if w.Worktree != "" {
			fmt.Fprintf(&b, "Winner's isolated worktree: %s - inspect and merge from there; the other worktrees are disposable.\n", w.Worktree)
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

// successfulResults collects the Result strings of candidates that reached
// the terminal completed status with non-empty output. Shared-error
// similarity is only meaningful among successful outputs (error strings
// would trivially match each other).
func successfulResults(cs []CandidateOutcome) []string {
	var out []string
	for _, c := range cs {
		if c.Status != string(subagent.StatusCompleted) || strings.TrimSpace(c.Result) == "" {
			continue
		}
		out = append(out, c.Result)
	}
	return out
}

// pairwiseResultSimilarity returns the mean Jaccard similarity over all
// result pairs. ok=false when fewer than two results (nothing to correlate).
func pairwiseResultSimilarity(results []string) (float64, bool) {
	if len(results) < 2 {
		return 0, false
	}
	shingles := make([]map[string]struct{}, 0, len(results))
	for _, r := range results {
		shingles = append(shingles, resultShingles(r))
	}
	var sum float64
	var pairs int
	for i := 0; i < len(shingles); i++ {
		for j := i + 1; j < len(shingles); j++ {
			sum += jaccardSets(shingles[i], shingles[j])
			pairs++
		}
	}
	if pairs == 0 {
		return 0, false
	}
	return sum / float64(pairs), true
}

// resultShingles builds a 5-word shingle set from the clipped result text.
// Word shingles (vs character n-grams) are robust to whitespace churn and
// cheap enough for a handful of 2KB clips.
func resultShingles(s string) map[string]struct{} {
	words := strings.Fields(strings.ToLower(clip(s, bonSimClip)))
	set := make(map[string]struct{}, len(words))
	if len(words) < bonSimShingleWords {
		for _, w := range words {
			set[w] = struct{}{}
		}
		return set
	}
	for i := 0; i+bonSimShingleWords <= len(words); i++ {
		set[strings.Join(words[i:i+bonSimShingleWords], " ")] = struct{}{}
	}
	return set
}

// jaccardSets is the local set-Jaccard (twin of internal/knight's
// jaccardSimilarity, kept local to avoid a cross-package dependency for
// three lines of arithmetic).
func jaccardSets(a, b map[string]struct{}) float64 {
	if len(a) == 0 && len(b) == 0 {
		return 1
	}
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	inter := 0
	for w := range a {
		if _, ok := b[w]; ok {
			inter++
		}
	}
	union := len(a) + len(b) - inter
	if union <= 0 {
		return 0
	}
	return float64(inter) / float64(union)
}
