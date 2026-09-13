package main

import (
	"os"
	"strings"
	"testing"
)

// #1537: report family fixes - llmCalls on turnJSON (case B display layer),
// zero-TTFT exclusion from AvgTTFT (case D), and the exactly-once channel
// send in the scan collect loop (case C, source-level pin).

func TestIssue1537TurnJSONCarriesLLMCalls(t *testing.T) {
	src := mustRead1537(t, "report_data.go")
	if !strings.Contains(src, `json:"llmCalls"`) {
		t.Fatal("turnJSON must carry a llmCalls json field (#1537-B)")
	}
	if !strings.Contains(src, "sj.LLMCalls - llmCallsBefore") {
		t.Fatal("turn llmCalls must be the per-turn delta, not the session cumulative")
	}
}

func TestIssue1537HTMLCardsUseLLMCalls(t *testing.T) {
	src := mustRead1537(t, "report_html.go")
	for _, ghost := range []string{
		"tLLM=turns.length",
		"card('LLM Calls', filtered.length,",
		"card('LLM Calls', dayTurns.length)",
	} {
		if strings.Contains(src, ghost) {
			t.Fatalf("display layer still counts turns: %s", ghost)
		}
	}
	for _, want := range []string{
		"t.llmCalls||0",
		"filtered.reduce((a,t)=>a+(t.llmCalls||0),0)",
		"dayTurns.reduce((a,t)=>a+(t.llmCalls||0),0)",
	} {
		if !strings.Contains(src, want) {
			t.Fatalf("card aggregation missing: %s", want)
		}
	}
}

func TestIssue1537ZeroTTFTExcludedFromAvg(t *testing.T) {
	src := mustRead1537(t, "report_data.go")
	if !strings.Contains(src, "if t.TTFTMs > 0 {") {
		t.Fatal("zero TTFT must be excluded from the AvgTTFT denominator (#1537-D)")
	}
}

func TestIssue1537SendExactlyOnceViaDefer(t *testing.T) {
	src := mustRead1537(t, "report_scan.go")
	if !strings.Contains(src, "defer func() {") {
		t.Fatal("scan goroutine must send via defer (#1537-C)")
	}
	if strings.Contains(src, "ch <- fileResult{idx: i, sr: sr}\n\t\t\t}\n\t\t})") {
		t.Fatal("bare mid-closure send left behind - double send would overflow the buffer")
	}
}

func mustRead1537(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(name)
	if err != nil {
		t.Skipf("source layout changed: %v", err)
	}
	return string(b)
}
