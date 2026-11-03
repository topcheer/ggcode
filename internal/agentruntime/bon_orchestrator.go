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

const (
	bestOfNMinCandidates  = 2
	bestOfNMaxCandidates  = 4
	bestOfNDefaultPoll    = 2 * time.Second
	bestOfNMaxSlots       = 16
	bonSharedErrorSimWarn = 0.85
	bonSimClip            = 2000
	bonSimShingleWords    = 5
)

type CandidateSpawner interface {
	Launch(ctx context.Context, opts tool.LaunchOptions) (id string, worktree string, err error)
}

type SnapshotSource interface {
	Snapshot(id string) (subagent.Snapshot, bool)
	RunningCount() int
}

type CandidateCanceller interface {
	Cancel(id string) bool
}

type BestOfNOptions struct {
	Task           string
	N              int
	Tools          []string
	Isolation      string
	Name           string
	Poll           time.Duration
	Models         []string
	VerifierModels []string

	YieldOnFirstSuccess bool
}

const bestOfNYieldFactor = 1.5

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

func (o BestOfNOptions) modelFor(i int) string {
	if len(o.Models) == 0 {
		return ""
	}
	return o.Models[(i-1)%len(o.Models)]
}

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

type CandidateOutcome struct {
	Name     string
	ID       string
	Status   string
	Verdict  string
	Worktree string
	Model    string
	Result   string
	Error    string
}

type BestOfNReport struct {
	N                int
	Candidates       []CandidateOutcome
	WinnerIndex      int
	Degraded         bool
	ConditioningHint string
	Partial          bool
	Discriminated    bool
	Evidence         string
	HighCorrelation  bool
	AvgSimilarity    float64
	AdmissionHint    string
	Report           string
	Err              string
}

const candidateTaskSuffix = "\n\nYou are one of %d INDEPENDENT parallel candidates attempting the same task. Work self-sufficiently: no coordination, no assumptions about other runs. Before finishing, run the narrowest verification you can (targeted build/test/grep) and state its outcome explicitly in your final message (e.g. \"go build ./pkg/ passes; tests X,Y green\")."

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

	if rc := snaps.RunningCount(); rc+n > bestOfNMaxSlots {
		rep.Err = fmt.Sprintf("best_of_n: needs %d free sub-agent slots but only %d of %d are free (currently running: %d). Wait for existing runs to finish or lower n.", n, bestOfNMaxSlots-rc, bestOfNMaxSlots, rc)
		rep.Report = rep.Err
		return rep
	}

	isolation := opts.Isolation
	if isolation == "" {
		isolation = "worktree"
	}

	if hint, ok := BestOfNAdmissionHint(opts.Task); ok {
		rep.AdmissionHint = hint
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

	terminal := make([]bool, n)

	snapsFinal := make([]subagent.Snapshot, n)
	canceller, _ := snaps.(CandidateCanceller)
	yieldClosed := false
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
				terminal[i] = true
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

		rep.WinnerIndex = idx
		rep.Discriminated = true
		rep.Evidence = ev
	} else {

		rep.Degraded = true
		sums := make([]RolloutSummary, 0, n)
		for _, c := range cands {
			sums = append(sums, SummarizeTrajectory(c.Transcript))
		}
		rep.ConditioningHint = DistillIntoPrompt(sums)
	}

	if sim, okSim := pairwiseResultSimilarity(successfulResults(rep.Candidates)); okSim {
		rep.AvgSimilarity = sim
		rep.HighCorrelation = sim >= bonSharedErrorSimWarn
	}
	rep.Report = formatBestOfNReport(rep, winnerSummary(wsum, ok))

	RecordBestOfNGap(opts.Task, rep)
	return rep
}

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
	if rep.AdmissionHint != "" {
		fmt.Fprintf(&b, "\n\nNote: %s\n", rep.AdmissionHint)
	}
	return b.String()
}

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
