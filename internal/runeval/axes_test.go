package runeval

import (
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
)

func axisByName(t *testing.T, r Report, name string) AxisScore {
	t.Helper()
	for _, a := range r.Axes {
		if a.Name == name {
			return a
		}
	}
	t.Fatalf("axis %q missing from %+v", name, r.Axes)
	return AxisScore{}
}

func TestCleanTrajectoryAllAxesPerfect(t *testing.T) {
	msgs := []provider.Message{
		msg("assistant", toolUse("t1", "git_status", `{"path":"."}`)),
		msg("user", toolResult("t1", "clean tree", false)),
		msg("assistant", toolUse("t2", "read_file", `{"path":"main.go"}`)),
		msg("user", toolResult("t2", "package main", false)),
	}
	r := Evaluate(msgs, nil)
	if len(r.Axes) != 4 {
		t.Fatalf("axes=%d, want 4", len(r.Axes))
	}
	for _, a := range r.Axes {
		if a.Score != 100 {
			t.Fatalf("axis %s score=%d, want 100", a.Name, a.Score)
		}
		if a.Detail != "" {
			t.Fatalf("axis %s detail=%q, want empty on a clean run", a.Name, a.Detail)
		}
	}
}

func TestErrorRetryDrivesRecoveryAxis(t *testing.T) {
	// grep(foo) fails, then the EXACT same input is retried: the classic
	// "retry the same broken input" anti-pattern.
	msgs := []provider.Message{
		msg("assistant", toolUse("t1", "grep", `{"pattern":"foo"}`)),
		msg("user", toolResult("t1", "boom: no such file", true)),
		msg("assistant", toolUse("t2", "grep", `{"pattern":"foo"}`)),
		msg("user", toolResult("t2", "match line", false)),
	}
	r := Evaluate(msgs, nil)
	if r.ErrorRetries != 1 {
		t.Fatalf("retries=%d, want 1", r.ErrorRetries)
	}
	a := axisByName(t, r, "error-recovery")
	// 50% error rate + 50% retry rate: 70*0.5 + 30*0.5 = 50 penalty.
	if a.Score != 50 {
		t.Fatalf("error-recovery=%d, want 50", a.Score)
	}
	if !strings.Contains(a.Detail, "1 identical-input retry") {
		t.Fatalf("detail=%q, want retry mention", a.Detail)
	}
	// Composite oracle unchanged: error rate is still its only error signal.
	if r.WastedRepeatCalls != 1 || r.EfficiencyScore >= 100 {
		t.Fatalf("composite drifted: wasted=%d score=%d", r.WastedRepeatCalls, r.EfficiencyScore)
	}
	// The weakest axis surfaces as a finding.
	if len(r.Axes) == 0 || r.Axes[0].Name != "error-recovery" {
		t.Fatalf("worst axis=%+v, want error-recovery first", r.Axes)
	}
	if len(r.Findings) == 0 || !strings.Contains(r.Findings[len(r.Findings)-1], "weakest axis error-recovery") {
		t.Fatalf("missing weakest-axis finding: %v", r.Findings)
	}
}

func TestErrorWithoutIdenticalRetryNotRetried(t *testing.T) {
	// grep(a) fails, agent corrects the argument (grep b): a proper
	// corrected retry must not count as an identical-input retry.
	msgs := []provider.Message{
		msg("assistant", toolUse("t1", "grep", `{"pattern":"a"}`)),
		msg("user", toolResult("t1", "err", true)),
		msg("assistant", toolUse("t2", "grep", `{"pattern":"b"}`)),
		msg("user", toolResult("t2", "ok", false)),
		msg("assistant", toolUse("t3", "glob", `{"pattern":"*"}`)),
		msg("user", toolResult("t3", "ok", false)),
		msg("assistant", toolUse("t4", "glob", `{"pattern":"x"}`)),
		msg("user", toolResult("t4", "ok", false)),
	}
	r := Evaluate(msgs, nil)
	if r.ErrorRetries != 0 {
		t.Fatalf("retries=%d, want 0 for corrected retry", r.ErrorRetries)
	}
	a := axisByName(t, r, "error-recovery")
	// 25% error rate, no retry: 70*0.25 = 17.5 penalty -> 83.
	if a.Score != 83 {
		t.Fatalf("error-recovery=%d, want 83", a.Score)
	}
	if strings.Contains(a.Detail, "retry") {
		t.Fatalf("detail=%q must not claim an identical retry", a.Detail)
	}
}

func TestReadOnlyRepeatsDriveUtilizationAxis(t *testing.T) {
	msgs := []provider.Message{
		msg("assistant", toolUse("t1", "read_file", `{"path":"main.go"}`)),
		msg("user", toolResult("t1", "package main", false)),
		msg("assistant", toolUse("t2", "read_file", `{"path":"main.go"}`)),
		msg("user", toolResult("t2", "package main", false)),
		msg("assistant", toolUse("t3", "read_file", `{"path":"main.go"}`)),
		msg("user", toolResult("t3", "package main", false)),
	}
	r := Evaluate(msgs, nil)
	if r.ReadOnlyRepeats != 2 {
		t.Fatalf("read-only repeats=%d, want 2", r.ReadOnlyRepeats)
	}
	a := axisByName(t, r, "result-utilization")
	// 2 of 3 calls were redundant re-reads: penalty 66.7 -> 33.
	if a.Score != 33 {
		t.Fatalf("result-utilization=%d, want 33", a.Score)
	}
	if !strings.Contains(a.Detail, "2 identical re-reads") {
		t.Fatalf("detail=%q, want re-read count", a.Detail)
	}
	if r.Axes[0].Name != "result-utilization" {
		t.Fatalf("worst axis=%s, want result-utilization", r.Axes[0].Name)
	}
}

func TestVaryingReadDoesNotPenalizeUtilization(t *testing.T) {
	// Same input, different results: new information, no penalty.
	msgs := []provider.Message{
		msg("assistant", toolUse("t1", "read_file", `{"path":"x.go"}`)),
		msg("user", toolResult("t1", "v1", false)),
		msg("assistant", toolUse("t2", "read_file", `{"path":"x.go"}`)),
		msg("user", toolResult("t2", "v2 changed", false)),
	}
	r := Evaluate(msgs, nil)
	a := axisByName(t, r, "result-utilization")
	if a.Score != 100 || a.Detail != "" {
		t.Fatalf("result-utilization=%+v, want clean", a)
	}
}

func TestStatefulIdenticalRepeatDrivesLoopAxis(t *testing.T) {
	// edit_file twice with identical args and identical output: reported
	// on the loop-discipline axis, still never auto-wasted in the
	// composite (stateful repeats can be legitimate).
	msgs := []provider.Message{
		msg("assistant", toolUse("t1", "edit_file", `{"path":"a.go","old":"x","new":"y"}`)),
		msg("user", toolResult("t1", "ok", false)),
		msg("assistant", toolUse("t2", "edit_file", `{"path":"a.go","old":"x","new":"y"}`)),
		msg("user", toolResult("t2", "ok", false)),
	}
	r := Evaluate(msgs, nil)
	if r.StatefulLoopRepeats != 1 {
		t.Fatalf("stateful loops=%d, want 1", r.StatefulLoopRepeats)
	}
	if r.WastedRepeatCalls != 0 {
		t.Fatalf("wasted=%d, composite must stay conservative for stateful tools", r.WastedRepeatCalls)
	}
	a := axisByName(t, r, "loop-discipline")
	if a.Score != 50 {
		t.Fatalf("loop-discipline=%d, want 50", a.Score)
	}
	if !strings.Contains(a.Detail, "possible loop") {
		t.Fatalf("detail=%q, want loop hint", a.Detail)
	}
}

func TestStatefulVaryingRepeatNoLoopPenalty(t *testing.T) {
	// Same mutating command, different results: legitimate re-runs.
	msgs := []provider.Message{
		msg("assistant", toolUse("t1", "run_command", `{"command":"date"}`)),
		msg("user", toolResult("t1", "Mon", false)),
		msg("assistant", toolUse("t2", "run_command", `{"command":"date"}`)),
		msg("user", toolResult("t2", "Tue", false)),
	}
	r := Evaluate(msgs, nil)
	if r.StatefulLoopRepeats != 0 {
		t.Fatalf("stateful loops=%d, want 0 when results vary", r.StatefulLoopRepeats)
	}
	if a := axisByName(t, r, "loop-discipline"); a.Score != 100 {
		t.Fatalf("loop-discipline=%d, want 100", a.Score)
	}
}

func TestContextEconomyAxis(t *testing.T) {
	msgs := []provider.Message{
		msg("assistant", toolUse("t1", "grep", `{"pattern":"a"}`)),
		msg("user", toolResult("t1", "ok", false)),
		msg("assistant", toolUse("t2", "glob", `{"pattern":"b"}`)),
		msg("user", toolResult("t2", "ok", false)),
		msg("assistant", toolUse("t3", "read_file", `{"path":"c"}`)),
		msg("user", toolResult("t3", "ok", false)),
		msg("assistant", toolUse("t4", "read_file", `{"path":"d"}`)),
		msg("user", toolResult("t4", "ok", false)),
		msg("assistant", toolUse("t5", "git_status", `{"path":"."}`)),
		msg("user", toolResult("t5", "ok", false)),
	}
	usage := []UsageSample{
		{Source: "agent", Usage: provider.TokenUsage{InputTokens: 800, OutputTokens: 200}},
		{Source: "compaction", Usage: provider.TokenUsage{InputTokens: 200, OutputTokens: 100}},
	}
	r := Evaluate(msgs, usage)
	a := axisByName(t, r, "context-economy")
	// overhead 300/1300 = 23.1% -> penalty 50*0.2308 = 11.5 -> 88.
	if a.Score != 88 {
		t.Fatalf("context-economy=%d, want 88", a.Score)
	}
	if !strings.Contains(a.Detail, "23% overhead machinery") {
		t.Fatalf("detail=%q, want overhead share", a.Detail)
	}
}

func TestAxesSortedWorstFirst(t *testing.T) {
	// Multiple issues at once; axes must come back sorted ascending.
	msgs := []provider.Message{
		msg("assistant", toolUse("t1", "grep", `{"pattern":"foo"}`)),
		msg("user", toolResult("t1", "boom", true)),
		msg("assistant", toolUse("t2", "grep", `{"pattern":"foo"}`)),
		msg("user", toolResult("t2", "match", false)),
		msg("assistant", toolUse("t3", "read_file", `{"path":"main.go"}`)),
		msg("user", toolResult("t3", "package main", false)),
		msg("assistant", toolUse("t4", "read_file", `{"path":"main.go"}`)),
		msg("user", toolResult("t4", "package main", false)),
	}
	usage := []UsageSample{
		{Source: "agent", Usage: provider.TokenUsage{InputTokens: 500, OutputTokens: 100}},
		{Source: "strategist", Usage: provider.TokenUsage{InputTokens: 400, OutputTokens: 200}},
	}
	r := Evaluate(msgs, usage)
	for i := 1; i < len(r.Axes); i++ {
		if r.Axes[i-1].Score > r.Axes[i].Score {
			t.Fatalf("axes not worst-first: %+v", r.Axes)
		}
	}
	out := Render(r)
	if !strings.Contains(out, "Axes:") {
		t.Fatalf("render missing axes block:\n%s", out)
	}
	if !strings.Contains(out, "▼ weakest:") {
		t.Fatalf("render missing weakest-axis diagnosis:\n%s", out)
	}
}

func TestRenderAxesCleanRunHasNoDiagnosis(t *testing.T) {
	msgs := []provider.Message{
		msg("assistant", toolUse("t1", "git_status", `{"path":"."}`)),
		msg("user", toolResult("t1", "clean", false)),
	}
	out := Render(Evaluate(msgs, nil))
	if strings.Contains(out, "▼ weakest") {
		t.Fatalf("clean run must not show a weakest-axis line:\n%s", out)
	}
	if !strings.Contains(out, "Axes:") {
		t.Fatalf("clean run should still list axes:\n%s", out)
	}
}
