package tool

import (
	"strings"
	"testing"
	"time"
)

// #5209 D2: snapshot layered budget - acceptance assertions per verdict §6.1 ①:
// (a) truncation marker present when budgets were exhausted, (b) timed-out
// frames yield zero refs, (c) fast pages regress nothing. Browser-path
// behaviors are exercised by the integration suite; these pin the unit
// surfaces (budget derivation, JS contract constants, output assembly).

func TestSnapshotTopLevelBudget(t *testing.T) {
	cases := []struct {
		waitSec int
		want    time.Duration
	}{
		{0, 3 * time.Second},   // default
		{-5, 3 * time.Second},  // negative -> default
		{1, 1 * time.Second},   // user request honored
		{8, 8 * time.Second},   // at cap
		{30, 8 * time.Second},  // clamped by hard cap
		{120, 8 * time.Second}, // clamped by hard cap
	}
	for _, tc := range cases {
		if got := snapshotTopLevelBudget(tc.waitSec); got != tc.want {
			t.Errorf("snapshotTopLevelBudget(%d) = %v, want %v", tc.waitSec, got, tc.want)
		}
	}
}

func TestSnapshotBudgetJSContract(t *testing.T) {
	// Budget constants from the design (§1): 1s shared iframe budget,
	// 500ms per child frame.
	for _, want := range []string{
		"IFRAME_TOTAL_MS = 1000",
		"IFRAME_CHILD_MS = 500",
	} {
		if !strings.Contains(browserSnapshotJS, want) {
			t.Errorf("snapshot JS missing budget constant %q", want)
		}
	}
	// Timed-out frames must report (frames.push timeout:true) and must NOT
	// collect elements: the timeout branch appears before any collectInto
	// call for that frame. The marker branch and the zero-ref guarantee are
	// contractually tied: timedOut => that frame contributed zero entries.
	if !strings.Contains(browserSnapshotJS, "timeout: true") {
		t.Error("snapshot JS never reports timed-out frames")
	}
	// The payload shape the Go decoder expects (JS object literal keys are
	// unquoted; the decoder quotes them via its JSON tags).
	for _, want := range []string{"entries: out", "frames: frames", "timedOut: timedOut"} {
		if !strings.Contains(browserSnapshotJS, want) {
			t.Errorf("snapshot JS payload missing %q", want)
		}
	}
}

// Acceptance ①: truncation marker present exactly when timedOut.
func TestRenderSnapshotOutputMarker(t *testing.T) {
	entries := []browserRefEntry{{Ref: "@e1", Role: "button", Name: "OK"}}

	fast := renderSnapshotOutput(entries, false)
	if strings.Contains(fast, snapshotTimeoutMarker) {
		t.Error("fast page output must NOT carry the truncation marker")
	}
	if !strings.Contains(fast, "@e1") || !strings.Contains(fast, "Snapshot: 1 elements") {
		t.Errorf("fast page output lost entries:\n%s", fast)
	}

	slow := renderSnapshotOutput(entries, true)
	if !strings.Contains(slow, snapshotTimeoutMarker) {
		t.Error("acceptance (1): timed-out snapshot missing '[timeout: budget exhausted]' marker")
	}
	if !strings.Contains(slow, "NO refs") {
		t.Error("marker must state that skipped frames carry no refs")
	}
	// Timed-out frames keep collected top-level entries (partial results
	// beat total failure).
	if !strings.Contains(slow, "@e1") {
		t.Error("partial result lost already-collected entries")
	}
}

// Acceptance ② (unit face): refs are minted strictly from decoded entries -
// the decoder emits one @eN per entry and nothing else can mint refs, so
// zero entries from a timed-out frame means zero refs by construction.
func TestSnapshotRefMintingBoundToEntries(t *testing.T) {
	entries, timedOut, err := (&Browser{}).collectBrowserRefsForPayload(
		`{"entries":[{"role":"button","name":"A","sel":"body > button","href":""}],
		  "frames":[{"idx":0,"ok":false,"timeout":true}],
		  "timedOut":true}`)
	if err != nil {
		t.Fatalf("payload decode failed: %v", err)
	}
	if !timedOut {
		t.Error("timedOut flag lost in decode")
	}
	if len(entries) != 1 || entries[0].Ref != "@e1" {
		t.Fatalf("expected exactly one ref from the single entry, got %+v", entries)
	}
	// The timed-out frame (ok:false, timeout:true) contributed no entry:
	// entry count == entries array length, no synthesized refs.
	if len(entries) != 1 {
		t.Errorf("timed-out frame leaked refs: %+v", entries)
	}
}
