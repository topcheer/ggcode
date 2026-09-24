package runeval

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
)

func msg(role string, blocks ...provider.ContentBlock) provider.Message {
	return provider.Message{Role: role, Content: blocks}
}

func toolUse(id, name, input string) provider.ContentBlock {
	return provider.ContentBlock{
		Type:     "tool_use",
		ToolName: name,
		ToolID:   id,
		Input:    json.RawMessage(input),
	}
}

func toolResult(id, output string, isErr bool) provider.ContentBlock {
	return provider.ContentBlock{
		Type:    "tool_result",
		ToolID:  id,
		Output:  output,
		IsError: isErr,
	}
}

func TestCleanTrajectoryScoresHigh(t *testing.T) {
	msgs := []provider.Message{
		msg("user", provider.ContentBlock{Type: "text", Text: "inspect the repo"}),
		msg("assistant", toolUse("t1", "git_status", `{"path":"."}`)),
		msg("user", toolResult("t1", "clean tree", false)),
		msg("assistant", toolUse("t2", "read_file", `{"path":"main.go"}`)),
		msg("user", toolResult("t2", "package main", false)),
	}
	r := Evaluate(msgs, nil)
	if r.ToolCalls != 2 || r.ToolErrors != 0 {
		t.Fatalf("calls=%d errors=%d, want 2/0", r.ToolCalls, r.ToolErrors)
	}
	if r.TurnCount != 2 {
		t.Fatalf("turns=%d, want 2", r.TurnCount)
	}
	if r.DistinctTools != 2 {
		t.Fatalf("distinct=%d, want 2", r.DistinctTools)
	}
	if len(r.DuplicateGroups) != 0 {
		t.Fatalf("dupes=%d, want 0", len(r.DuplicateGroups))
	}
	if r.WastedRepeatCalls != 0 || r.WastedResultBytes != 0 {
		t.Fatalf("wasted calls=%d bytes=%d, want 0/0", r.WastedRepeatCalls, r.WastedResultBytes)
	}
	if r.EfficiencyScore != 100 {
		t.Fatalf("score=%d, want 100", r.EfficiencyScore)
	}
}

func TestIdenticalReadOnlyRepeatIsWasted(t *testing.T) {
	msgs := []provider.Message{
		msg("assistant", toolUse("t1", "read_file", `{"path":"main.go"}`)),
		msg("user", toolResult("t1", "package main", false)),
		msg("assistant", toolUse("t2", "read_file", `{"path":"main.go"}`)),
		msg("user", toolResult("t2", "package main", false)),
		msg("assistant", toolUse("t3", "read_file", `{"path":"main.go"}`)),
		msg("user", toolResult("t3", "package main", false)),
	}
	r := Evaluate(msgs, nil)
	if r.WastedRepeatCalls != 2 {
		t.Fatalf("wasted=%d, want 2", r.WastedRepeatCalls)
	}
	// "package main" is 12 bytes; only the two repeats count.
	if r.WastedResultBytes != 24 {
		t.Fatalf("bytes=%d, want 24", r.WastedResultBytes)
	}
	if r.WastedTokenEstimate != 24/bytesPerToken {
		t.Fatalf("est=%d, want %d", r.WastedTokenEstimate, 24/bytesPerToken)
	}
	if len(r.DuplicateGroups) != 1 || r.DuplicateGroups[0].Count != 3 {
		t.Fatalf("groups=%+v, want one group count=3", r.DuplicateGroups)
	}
	if !r.DuplicateGroups[0].Identical || !r.DuplicateGroups[0].ReadOnly {
		t.Fatalf("group flags wrong: %+v", r.DuplicateGroups[0])
	}
}

func TestVaryingRepeatIsReportedNotWasted(t *testing.T) {
	// Same input, different results (file changed between reads): a
	// read-only repeat with NEW information is not waste.
	msgs := []provider.Message{
		msg("assistant", toolUse("t1", "read_file", `{"path":"x.go"}`)),
		msg("user", toolResult("t1", "v1", false)),
		msg("assistant", toolUse("t2", "read_file", `{"path":"x.go"}`)),
		msg("user", toolResult("t2", "v2 changed", false)),
	}
	r := Evaluate(msgs, nil)
	if r.WastedRepeatCalls != 0 || r.WastedResultBytes != 0 {
		t.Fatalf("wasted=%d bytes=%d, want 0/0", r.WastedRepeatCalls, r.WastedResultBytes)
	}
	if len(r.DuplicateGroups) != 1 {
		t.Fatalf("groups=%d, want 1 (reported but not penalized)", len(r.DuplicateGroups))
	}
	if r.DuplicateGroups[0].Identical {
		t.Fatal("group must not be identical")
	}
}

func TestWriteToolRepeatsNeverCountedWasted(t *testing.T) {
	// edit_file with identical args twice can be legitimate (retry after
	// failure path, or a real re-edit); it is reported, never auto-wasted.
	msgs := []provider.Message{
		msg("assistant", toolUse("t1", "edit_file", `{"path":"a.go","old":"x","new":"y"}`)),
		msg("user", toolResult("t1", "ok", false)),
		msg("assistant", toolUse("t2", "edit_file", `{"path":"a.go","old":"x","new":"y"}`)),
		msg("user", toolResult("t2", "ok", false)),
	}
	r := Evaluate(msgs, nil)
	if r.WastedRepeatCalls != 0 {
		t.Fatalf("wasted=%d, want 0 for stateful tool", r.WastedRepeatCalls)
	}
	if len(r.DuplicateGroups) != 1 || r.DuplicateGroups[0].ReadOnly {
		t.Fatalf("group=%+v, want one non-readonly group", r.DuplicateGroups)
	}
}

func TestFailedCallsAreWasted(t *testing.T) {
	msgs := []provider.Message{
		msg("assistant", toolUse("t1", "grep", `{"pattern":"foo"}`)),
		msg("user", toolResult("t1", "boom: no such file", true)),
		msg("assistant", toolUse("t2", "grep", `{"pattern":"foo"}`)),
		msg("user", toolResult("t2", "match line", false)),
	}
	r := Evaluate(msgs, nil)
	if r.ToolErrors != 1 {
		t.Fatalf("errors=%d, want 1", r.ToolErrors)
	}
	if r.WastedRepeatCalls != 1 {
		t.Fatalf("wasted=%d, want 1 (failed result)", r.WastedRepeatCalls)
	}
	if r.EfficiencyScore >= 100 {
		t.Fatalf("score=%d, should be below 100", r.EfficiencyScore)
	}
}

func TestErrorRatePenalty(t *testing.T) {
	// 1 error in 4 calls = 25% error rate => -10 points.
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
	if r.EfficiencyScore != 90 {
		t.Fatalf("score=%d, want 90", r.EfficiencyScore)
	}
}

func TestOverheadUsageShare(t *testing.T) {
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
		{Source: "strategist", Usage: provider.TokenUsage{InputTokens: 100, OutputTokens: 50}},
	}
	r := Evaluate(msgs, usage)
	if r.TotalTokens != 1450 {
		t.Fatalf("total=%d, want 1450", r.TotalTokens)
	}
	if r.OverheadTokens != 450 {
		t.Fatalf("overhead=%d, want 450", r.OverheadTokens)
	}
	// overhead share ~31% > 15% threshold with 5 calls => -20*0.3103 = -6.2
	if r.EfficiencyScore != 94 {
		t.Fatalf("score=%d, want 94", r.EfficiencyScore)
	}
	if len(r.Findings) == 0 || !strings.Contains(strings.Join(r.Findings, "\n"), "overhead") {
		t.Fatalf("missing overhead finding: %v", r.Findings)
	}
}

func TestCanonicalJSONKeyOrderInsensitive(t *testing.T) {
	a := canonicalJSON(json.RawMessage(`{"b":1,"a":2}`))
	b := canonicalJSON(json.RawMessage(`{"a":2,"b":1}`))
	if a != b {
		t.Fatalf("canonical forms differ: %q vs %q", a, b)
	}
	if c := canonicalJSON(json.RawMessage(`not json`)); c != "not json" {
		t.Fatalf("non-json degraded wrongly: %q", c)
	}
	if c := canonicalJSON(nil); c != "{}" {
		t.Fatalf("nil input: %q", c)
	}
}

func TestRenderContainsScorecard(t *testing.T) {
	msgs := []provider.Message{
		msg("assistant", toolUse("t1", "read_file", `{"path":"main.go"}`)),
		msg("user", toolResult("t1", "package main", false)),
		msg("assistant", toolUse("t2", "read_file", `{"path":"main.go"}`)),
		msg("user", toolResult("t2", "package main", false)),
	}
	out := Render(Evaluate(msgs, nil))
	for _, want := range []string{"trajectory scorecard", "efficiency", "read_file", "Findings"} {
		if !strings.Contains(out, want) {
			t.Fatalf("render missing %q:\n%s", want, out)
		}
	}
}

func TestEmptyLog(t *testing.T) {
	r := Evaluate(nil, nil)
	if r.EfficiencyScore != 100 || r.ToolCalls != 0 {
		t.Fatalf("empty log: %+v", r)
	}
	if out := Render(r); out == "" {
		t.Fatal("render of empty report must not be empty")
	}
}

func TestUnpairedResultIgnored(t *testing.T) {
	msgs := []provider.Message{
		msg("user", toolResult("ghost", "orphan", true)),
	}
	r := Evaluate(msgs, nil)
	if r.ToolErrors != 0 || r.ToolCalls != 0 {
		t.Fatalf("unpaired result leaked: %+v", r)
	}
}

func TestUncorrectedRetryPenalized(t *testing.T) {
	// The same input sent three times, failing identically the first two:
	// the first failure is information, the second failed re-send is the
	// uncorrected retry (the third attempt succeeded — transient failure).
	msgs := []provider.Message{
		msg("assistant", toolUse("t1", "grep", `{"pattern":"foo"}`)),
		msg("user", toolResult("t1", "boom: no such file", true)),
		msg("assistant", toolUse("t2", "grep", `{"pattern":"foo"}`)),
		msg("user", toolResult("t2", "boom: no such file", true)),
		msg("assistant", toolUse("t3", "grep", `{"pattern":"foo"}`)),
		msg("user", toolResult("t3", "match line", false)),
	}
	r := Evaluate(msgs, nil)
	if r.RetrySameInput != 1 {
		t.Fatalf("retrySameInput=%d, want 1 (failed re-sends beyond the first failure)", r.RetrySameInput)
	}
	if !strings.Contains(r.WorstRetry, "grep") {
		t.Fatalf("worstRetry=%q, want grep call", r.WorstRetry)
	}
	// 1 retry × 12 penalty = 12.
	if r.ReliabilityScore != 88 {
		t.Fatalf("reliability=%d, want 88", r.ReliabilityScore)
	}
	found := false
	for _, f := range r.Findings {
		if strings.Contains(f, "re-sent an input that had already failed") {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing retry finding: %v", r.Findings)
	}
}

func TestSingleFailureNotARetry(t *testing.T) {
	// One failure then success on a DIFFERENT input is a corrected retry —
	// not penalized on the reliability axis.
	msgs := []provider.Message{
		msg("assistant", toolUse("t1", "grep", `{"pattern":"foo"}`)),
		msg("user", toolResult("t1", "err", true)),
		msg("assistant", toolUse("t2", "grep", `{"pattern":"bar"}`)),
		msg("user", toolResult("t2", "ok", false)),
	}
	r := Evaluate(msgs, nil)
	if r.RetrySameInput != 0 {
		t.Fatalf("retrySameInput=%d, want 0 (corrected retry)", r.RetrySameInput)
	}
	if r.ReliabilityScore != 100 {
		t.Fatalf("reliability=%d, want 100", r.ReliabilityScore)
	}
}

func TestChurnDetected(t *testing.T) {
	// Same file written three times with distinct inputs: rework churn.
	msgs := []provider.Message{
		msg("assistant", toolUse("t1", "edit_file", `{"path":"a.go","old":"x","new":"y"}`)),
		msg("user", toolResult("t1", "ok", false)),
		msg("assistant", toolUse("t2", "edit_file", `{"path":"a.go","old":"y","new":"z"}`)),
		msg("user", toolResult("t2", "ok", false)),
		msg("assistant", toolUse("t3", "edit_file", `{"path":"a.go","old":"z","new":"w"}`)),
		msg("user", toolResult("t3", "ok", false)),
	}
	r := Evaluate(msgs, nil)
	if len(r.ChurnedFiles) != 1 || r.ChurnedFiles[0].Edits != 3 {
		t.Fatalf("churned=%+v, want one file with 3 edits", r.ChurnedFiles)
	}
	if r.ChurnExtraEdits != 2 {
		t.Fatalf("extraEdits=%d, want 2", r.ChurnExtraEdits)
	}
	// 2 rework edits × 8 penalty = 16.
	if r.ReliabilityScore != 84 {
		t.Fatalf("reliability=%d, want 84", r.ReliabilityScore)
	}
	found := false
	for _, f := range r.Findings {
		if strings.Contains(f, "written more than once") {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing churn finding: %v", r.Findings)
	}
}

func TestSingleWriteNoChurn(t *testing.T) {
	msgs := []provider.Message{
		msg("assistant", toolUse("t1", "write_file", `{"path":"new.go","content":"x"}`)),
		msg("user", toolResult("t1", "ok", false)),
	}
	r := Evaluate(msgs, nil)
	if len(r.ChurnedFiles) != 0 || r.ChurnExtraEdits != 0 {
		t.Fatalf("single write flagged as churn: %+v", r.ChurnedFiles)
	}
	if r.ReliabilityScore != 100 {
		t.Fatalf("reliability=%d, want 100", r.ReliabilityScore)
	}
}

func TestDegradationFlag(t *testing.T) {
	// Clean first half, erroring second half: degraded trend.
	msgs := []provider.Message{
		msg("assistant", toolUse("t1", "grep", `{"pattern":"a"}`)),
		msg("user", toolResult("t1", "ok", false)),
		msg("assistant", toolUse("t2", "grep", `{"pattern":"b"}`)),
		msg("user", toolResult("t2", "ok", false)),
		msg("assistant", toolUse("t3", "glob", `{"pattern":"c"}`)),
		msg("user", toolResult("t3", "err", true)),
		msg("assistant", toolUse("t4", "glob", `{"pattern":"d"}`)),
		msg("user", toolResult("t4", "err", true)),
		msg("assistant", toolUse("t5", "glob", `{"pattern":"e"}`)),
		msg("user", toolResult("t5", "err", true)),
	}
	r := Evaluate(msgs, nil)
	if !r.Degraded {
		t.Fatal("expected degraded=true for late-half error concentration")
	}
	// Only the degradation penalty (12) applies: distinct inputs, so no
	// uncorrected retries, and read tools only, so no churn.
	if r.ReliabilityScore != 88 {
		t.Fatalf("reliability=%d, want 88", r.ReliabilityScore)
	}
	found := false
	for _, f := range r.Findings {
		if strings.Contains(f, "degraded instead of recovering") {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing degradation finding: %v", r.Findings)
	}
}

func TestEarlyErrorsNotDegraded(t *testing.T) {
	// Errors early, clean later: the run recovered — not degraded.
	msgs := []provider.Message{
		msg("assistant", toolUse("t1", "grep", `{"pattern":"a"}`)),
		msg("user", toolResult("t1", "err", true)),
		msg("assistant", toolUse("t2", "grep", `{"pattern":"b"}`)),
		msg("user", toolResult("t2", "err", true)),
		msg("assistant", toolUse("t3", "glob", `{"pattern":"c"}`)),
		msg("user", toolResult("t3", "err", true)),
		msg("assistant", toolUse("t4", "glob", `{"pattern":"d"}`)),
		msg("user", toolResult("t4", "ok", false)),
		msg("assistant", toolUse("t5", "glob", `{"pattern":"e"}`)),
		msg("user", toolResult("t5", "ok", false)),
	}
	r := Evaluate(msgs, nil)
	if r.Degraded {
		t.Fatal("early errors with clean recovery must not be degraded")
	}
}

func TestReliabilityRender(t *testing.T) {
	msgs := []provider.Message{
		msg("assistant", toolUse("t1", "grep", `{"pattern":"foo"}`)),
		msg("user", toolResult("t1", "boom", true)),
		msg("assistant", toolUse("t2", "grep", `{"pattern":"foo"}`)),
		msg("user", toolResult("t2", "boom", true)),
		msg("assistant", toolUse("t3", "grep", `{"pattern":"foo"}`)),
		msg("user", toolResult("t3", "ok", false)),
	}
	r := Evaluate(msgs, nil)
	if r.Degraded {
		t.Fatal("2 errors across both halves must not be degraded")
	}
	out := Render(r)
	for _, want := range []string{"reliability", "Reliability:", "re-sent an input"} {
		if !strings.Contains(out, want) {
			t.Fatalf("render missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "degraded trend") {
		t.Fatalf("degraded trend must not render when not degraded:\n%s", out)
	}
}

func TestInputPathExtraction(t *testing.T) {
	cases := map[string]string{
		`{"file_path":"/a/b.go","old":"x"}`: "/a/b.go",
		`{"path":"main.go"}`:                "main.go",
		`{"notebook_path":"n.ipynb"}`:       "n.ipynb",
		`{"pattern":"foo"}`:                 "",
		`not json`:                          "",
		``:                                  "",
	}
	for in, want := range cases {
		if got := inputPath(json.RawMessage(in)); got != want {
			t.Fatalf("inputPath(%s)=%q, want %q", in, got, want)
		}
	}
}
