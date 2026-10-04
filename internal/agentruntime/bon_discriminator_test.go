package agentruntime

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/topcheer/ggcode/internal/subagent"
	"github.com/topcheer/ggcode/internal/tool"
)

// r439 probes: execution-based tie-breaking for best-of-N. Unit-level —
// no LLM involved; the discriminator sub-agent is a fake whose final
// message carries the DISCRIMINATION marker.

func TestParseDiscrimination(t *testing.T) {
	cases := []struct {
		name string
		out  string
		want string
	}{
		{"winner A", "checks done\nA passed 3/4, B passed 1/4\nDISCRIMINATION: winner=A", "A"},
		{"winner B lowercase", "DISCRIMINATION: winner=b", "B"},
		{"inconclusive", "both pass\nDISCRIMINATION: winner=inconclusive", "inconclusive"},
		{"no marker", "I could not decide.", ""},
		{"marker scrolled out of window", "DISCRIMINATION: winner=A\np1\np2\np3\np4\np5\np6", ""},
		{"tolerates trailing prose", "verdict below\nDISCRIMINATION: winner=A\n(boundary edge case favored A)", "A"},
	}
	for _, c := range cases {
		if got := parseDiscrimination(c.out); got != c.want {
			t.Errorf("%s: parseDiscrimination() = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestPickTiePair(t *testing.T) {
	cands := []CandidateOutcome{
		{Name: "c1", Verdict: "failed", Worktree: "/wt1", Result: "boom"},
		{Name: "c2", Verdict: "succeeded", Worktree: "/wt2", Result: "ok"},
		{Name: "c3", Verdict: "partial", Worktree: "/wt3", Result: "half"},
		{Name: "c4", Verdict: "succeeded", Result: "no worktree"}, // skipped: isolation none
	}
	a, b, ok := pickTiePair(cands)
	if !ok || a != 1 || b != 2 {
		t.Fatalf("pickTiePair = (%d,%d,%v), want (1,2,true)", a, b, ok)
	}
	// Single discriminable candidate: no pair.
	if _, _, ok := pickTiePair(cands[:2]); ok {
		t.Fatal("pair found with only one worktree-backed candidate")
	}
	// Nothing at all.
	if _, _, ok := pickTiePair(nil); ok {
		t.Fatal("pair found in empty set")
	}
}

func discSnap(id, marker string) subagent.Snapshot {
	return subagent.Snapshot{
		ID:     id,
		Status: subagent.StatusCompleted,
		Result: "check matrix:\nA: 3/4 pass\nB: 4/4 pass\n" + marker,
	}
}

func tieCands() []CandidateOutcome {
	return []CandidateOutcome{
		{Name: "cand A", Verdict: "succeeded", Worktree: "/tmp/wt-1", Result: "ambiguous output 1"},
		{Name: "cand B", Verdict: "succeeded", Worktree: "/tmp/wt-2", Result: "ambiguous output 2"},
	}
}

func TestDiscriminateTiePicksWinner(t *testing.T) {
	sp := &fakeSpawner{}
	sn := &fakeSnaps{m: map[string]subagent.Snapshot{
		"cand-1": discSnap("cand-1", "DISCRIMINATION: winner=B"),
	}}
	idx, ev, ok := discriminateTie(context.Background(), sp, sn, tieCands(), BestOfNOptions{Task: "do the thing", Poll: 10 * time.Millisecond})
	if !ok {
		t.Fatal("expected a discriminated winner")
	}
	if idx != 1 {
		t.Fatalf("winner index = %d, want 1 (candidate B)", idx)
	}
	if !strings.Contains(ev, "4/4") {
		t.Fatalf("evidence missing discriminator report:\n%s", ev)
	}
	if len(sp.calls) != 1 {
		t.Fatalf("launcher calls = %d, want 1", len(sp.calls))
	}
	got := sp.calls[0]
	if got.Name != "bon-discriminator" {
		t.Fatalf("discriminator launch name = %q", got.Name)
	}
	for _, forbidden := range []string{"edit_file", "write_file", "file_ops"} {
		if containsTool(got.Tools, forbidden) {
			t.Fatalf("discriminator whitelist must be read/execute-only, found %s", forbidden)
		}
	}
	if !containsTool(got.Tools, "run_command") {
		t.Fatal("discriminator must be able to execute checks")
	}
}

func containsTool(tools []string, name string) bool {
	for _, x := range tools {
		if x == name {
			return true
		}
	}
	return false
}

func TestDiscriminateTieInconclusiveNoWinner(t *testing.T) {
	sp := &fakeSpawner{}
	sn := &fakeSnaps{m: map[string]subagent.Snapshot{
		"cand-1": discSnap("cand-1", "DISCRIMINATION: winner=inconclusive"),
	}}
	if _, _, ok := discriminateTie(context.Background(), sp, sn, tieCands(), BestOfNOptions{Task: "task", Poll: 10 * time.Millisecond}); ok {
		t.Fatal("inconclusive verdict must not produce a winner")
	}
}

func TestDiscriminateTieUnparseableAndFailedFallback(t *testing.T) {
	// Unparseable final message.
	sp := &fakeSpawner{}
	sn := &fakeSnaps{m: map[string]subagent.Snapshot{
		"cand-1": discSnap("cand-1", "I ran out of patience."),
	}}
	if _, _, ok := discriminateTie(context.Background(), sp, sn, tieCands(), BestOfNOptions{Task: "task", Poll: 10 * time.Millisecond}); ok {
		t.Fatal("unparseable output must not produce a winner")
	}
	// Discriminator itself failed.
	sn2 := &fakeSnaps{m: map[string]subagent.Snapshot{
		"cand-1": {ID: "cand-1", Status: subagent.StatusFailed, Error: "boom"},
	}}
	if _, _, ok := discriminateTie(context.Background(), sp, sn2, tieCands(), BestOfNOptions{Task: "task", Poll: 10 * time.Millisecond}); ok {
		t.Fatal("failed discriminator must not produce a winner")
	}
}

func TestDiscriminateTieNoPairNoLaunch(t *testing.T) {
	sp := &fakeSpawner{}
	sn := &fakeSnaps{m: map[string]subagent.Snapshot{}}
	noWT := []CandidateOutcome{{Name: "only", Verdict: "succeeded", Result: "ok"}}
	if _, _, ok := discriminateTie(context.Background(), sp, sn, noWT, BestOfNOptions{Task: "task", Poll: 10 * time.Millisecond}); ok {
		t.Fatal("no pair must not discriminate")
	}
	if len(sp.calls) != 0 {
		t.Fatalf("discriminator launched without a pair (%d calls)", len(sp.calls))
	}
}

// Compilation guard: tool.LaunchOptions must keep the fields the
// discriminator relies on.
var _ = tool.LaunchOptions{Name: "", Task: "", DisplayTask: "", Tools: nil}
