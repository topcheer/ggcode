package agent

import (
	"strings"
	"testing"
)

// mkAdherenceStats builds RunStats with the given edited files.
func mkAdherenceStats(files []string) *RunStats {
	rs := &RunStats{}
	for _, f := range files {
		rs.FilesEdited = append(rs.FilesEdited, f)
	}
	return rs
}

func TestComputePlanAdherence_FullCoverage(t *testing.T) {
	items := []planItem{
		{Text: "edit config loader", Keywords: []string{"config_loader.go"}},
		{Text: "edit auth middleware", Keywords: []string{"auth_middleware.go"}},
	}
	rs := mkAdherenceStats([]string{"internal/config/config_loader.go", "internal/auth/auth_middleware.go"})
	if got := computePlanAdherence(items, rs); got != 1 {
		t.Errorf("full coverage: got %.2f, want 1.00", got)
	}
}

func TestComputePlanAdherence_PartialCoverage(t *testing.T) {
	items := []planItem{
		{Text: "edit config loader", Keywords: []string{"config_loader.go"}},
		{Text: "edit auth middleware", Keywords: []string{"auth_middleware.go"}},
		{Text: "add tests", Keywords: []string{"config_loader_test.go"}},
	}
	// Only config_loader.go was touched: 2 of 3 items unaddressed -> 1/3.
	rs := mkAdherenceStats([]string{"internal/config/config_loader.go"})
	if got := computePlanAdherence(items, rs); got < 0.32 || got > 0.34 {
		t.Errorf("partial coverage: got %.2f, want ~0.33", got)
	}
}

func TestComputePlanAdherence_NoItems(t *testing.T) {
	if got := computePlanAdherence(nil, &RunStats{}); got != 1 {
		t.Errorf("no items: got %.2f, want 1 (vacuous)", got)
	}
}

func TestAdherenceTrend_SampleGapAndTrend(t *testing.T) {
	items := []planItem{
		{Text: "edit config loader", Keywords: []string{"config_loader.go"}},
	}
	rs := mkAdherenceStats(nil) // nothing done yet: score 0

	var tr planAdherenceTrend
	for i := 0; i < 12; i++ {
		tr.noteToolCall(items, rs)
	}
	// Gap=5: samples at call 1, 6, 11 -> 3 samples, 12 calls counted.
	if got := len(tr.Samples()); got != 3 {
		t.Fatalf("sample gap: got %d samples, want 3", got)
	}
	if tr.toolCalls != 12 {
		t.Errorf("toolCalls: got %d, want 12", tr.toolCalls)
	}
	if s := tr.Samples(); s[0].Score != 0 {
		t.Errorf("first sample score: got %.2f, want 0 (no work yet)", s[0].Score)
	}

	// Work appears: next sample must rise above 0 (trend visible).
	rs2 := mkAdherenceStats([]string{"internal/config/config_loader.go"})
	for i := 0; i < 5; i++ {
		tr.noteToolCall(items, rs2)
	}
	last := tr.Samples()[len(tr.Samples())-1]
	if last.Score != 1 {
		t.Errorf("after work: last sample %.2f, want 1", last.Score)
	}
	summary := tr.trendSummary()
	if !strings.Contains(summary, "0.00 -> 1.00") {
		t.Errorf("trendSummary %q missing rise 0.00 -> 1.00", summary)
	}
}

func TestAdherenceTrend_EmptyPlanNoOp(t *testing.T) {
	var tr planAdherenceTrend
	tr.noteToolCall(nil, &RunStats{})
	if got := len(tr.Samples()); got != 0 {
		t.Errorf("empty plan: got %d samples, want 0", got)
	}
	if s := tr.trendSummary(); s != "" {
		t.Errorf("empty trend summary: got %q, want empty", s)
	}
}

func TestAdherenceTrend_NilReceiverSafe(t *testing.T) {
	var tr *planAdherenceTrend
	tr.noteToolCall(nil, nil) // must not panic
	if tr.Samples() != nil {
		t.Error("nil receiver Samples should be nil")
	}
}

// The planDriftState wiring: reset clears the trend; captured plan feeds it.
func TestPlanDriftState_AdherenceResetWithDriftState(t *testing.T) {
	p := newPlanDriftState()
	p.capturePlan("- edit config_loader.go for the fix")
	items := []planItem{
		{Text: "edit config loader", Keywords: []string{"config_loader.go"}},
	}
	p.adherence.noteToolCall(items, &RunStats{})
	if got := len(p.adherence.Samples()); got != 1 {
		t.Fatalf("before reset: %d samples, want 1", got)
	}
	p.reset()
	if got := len(p.adherence.Samples()); got != 0 {
		t.Errorf("after reset: %d samples, want 0", got)
	}
	if p.adherence.toolCalls != 0 {
		t.Errorf("after reset: toolCalls %d, want 0", p.adherence.toolCalls)
	}
}

// buildPlanWorkCorpus equivalence: the extracted helper must produce the
// same corpus shape the fire-once warning relies on (lowercased paths,
// basenames, commands).
func TestBuildPlanWorkCorpus(t *testing.T) {
	rs := &RunStats{}
	rs.FilesEdited = []string{"internal/agent/Plan_Drift.go"}
	rs.CommandsRun = []string{"go test ./..."}
	corpus := buildPlanWorkCorpus(rs)
	for _, want := range []string{"internal/agent/plan_drift.go", "plan_drift.go", "go test ./..."} {
		if !strings.Contains(corpus, want) {
			t.Errorf("corpus missing %q: %q", want, corpus)
		}
	}
}
