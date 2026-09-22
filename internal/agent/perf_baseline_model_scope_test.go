package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	ctxpkg "github.com/topcheer/ggcode/internal/context"
	"github.com/topcheer/ggcode/internal/provider"
)

// perfModelCM mirrors fakePerfCM from zz_issue1148_test.go: a minimal
// ContextManager whose only exercised method is Add.
type perfModelCM struct {
	ctxpkg.ContextManager
	added []provider.Message
}

func (f *perfModelCM) Add(msg provider.Message) { f.added = append(f.added, msg) }

// TestPerfModelMatches pins the compatibility semantics of the model-scope
// predicate (sa-33): unknown identities (legacy baselines, embedders) match
// everything; known identities match only themselves.
func TestPerfModelMatches(t *testing.T) {
	cases := []struct {
		entry, current string
		want           bool
	}{
		{"", "", true},
		{"anthropic/claude/opus", "", true}, // embedder, no identity injected
		{"", "anthropic/claude/opus", true}, // legacy baseline entry
		{"a/b/m1", "a/b/m1", true},          // exact match
		{"a/b/m1", "a/b/m2", false},         // different model
		{"a/ep1/m1", "a/ep2/m1", false},     // same model, different endpoint
	}
	for i, c := range cases {
		if got := perfModelMatches(c.entry, c.current); got != c.want {
			t.Errorf("case %d: perfModelMatches(%q,%q)=%v, want %v", i, c.entry, c.current, got, c.want)
		}
	}
}

// TestPerfBaseline_ModelSwitchSuppressesWarning guards sa-33: after a
// mid-session /model switch, the new model's runs must not be compared
// against a baseline recorded under the old model. Cross-model
// iteration/duration deltas are expected variance, not regressions.
// Exercises the production path end-to-end: persisted mixed-model window,
// fresh load (hasBaseline=false), model-scoped filtering.
func TestPerfBaseline_ModelSwitchSuppressesWarning(t *testing.T) {
	tmp := t.TempDir()
	var runs []perfBaselineEntry
	for i := 0; i < 6; i++ {
		// Old-model baseline: cheap successful runs.
		runs = append(runs, perfBaselineEntry{Model: "a/b/m1", Iterations: 10, ToolCalls: 20, DurationSec: 60, Success: true, RunID: "old"})
	}
	for i := 0; i < 3; i++ {
		// New-model runs that LOOK like a 2x regression on iterations.
		runs = append(runs, perfBaselineEntry{Model: "a/b/m2", Iterations: 20, ToolCalls: 40, DurationSec: 120, Success: true, RunID: "new"})
	}
	if err := os.MkdirAll(filepath.Join(tmp, ".ggcode"), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := savePerfBaselineForTest(tmp, runs); err != nil {
		t.Fatalf("save: %v", err)
	}

	cm := &perfModelCM{}
	a := &Agent{
		contextManager: cm,
		modelID:        "a/b/m2",
		workingDir:     tmp,
		perfBaseline:   newPerfBaselineState(),
	}

	a.maybeInjectPerfRegression()

	if a.perfBaseline.warnCount != 0 {
		t.Fatalf("cross-model comparison must not fire a regression warning, got warnCount=%d (added=%v)", a.perfBaseline.warnCount, cm.added)
	}
	if len(cm.added) != 0 {
		t.Fatalf("no message expected after model switch, got: %v", cm.added)
	}
}

// TestPerfBaseline_SameModelBaselineStillDetects ensures the scoping does not
// over-suppress: enough same-model history still produces the warning.
func TestPerfBaseline_SameModelBaselineStillDetects(t *testing.T) {
	hist := []perfBaselineEntry{
		{Model: "a/b/m2", Iterations: 10, ToolCalls: 20, DurationSec: 60, Success: true},
		{Model: "a/b/m2", Iterations: 10, ToolCalls: 20, DurationSec: 60, Success: true},
		{Model: "a/b/m2", Iterations: 10, ToolCalls: 20, DurationSec: 60, Success: true},
		{Model: "a/b/m2", Iterations: 10, ToolCalls: 20, DurationSec: 60, Success: true},
		{Model: "a/b/m2", Iterations: 10, ToolCalls: 20, DurationSec: 60, Success: true},
		{Model: "a/b/m2", Iterations: 20, ToolCalls: 40, DurationSec: 120, Success: true},
		{Model: "a/b/m2", Iterations: 20, ToolCalls: 40, DurationSec: 120, Success: true},
		{Model: "a/b/m2", Iterations: 20, ToolCalls: 40, DurationSec: 120, Success: true},
	}
	cm := &perfModelCM{}
	a := &Agent{
		contextManager: cm,
		modelID:        "a/b/m2",
		perfBaseline: &perfBaselineState{
			historical:  hist,
			baselineMid: perfBaselineEntry{Iterations: 10, ToolCalls: 20, DurationSec: 60},
			hasBaseline: true,
		},
	}

	a.maybeInjectPerfRegression()

	if a.perfBaseline.warnCount != 1 {
		t.Fatalf("same-model regression should still warn, got warnCount=%d (added=%v)", a.perfBaseline.warnCount, cm.added)
	}
	if len(cm.added) != 1 {
		t.Fatalf("expected exactly one advisory message, got %d", len(cm.added))
	}
}

// TestPerfBaseline_LegacyEmptyModelEntriesComparable ensures pre-stamping
// baselines (empty Model) keep working against an agent with an injected
// identity: unknown entries are treated as comparable, preserving old behavior.
func TestPerfBaseline_LegacyEmptyModelEntriesComparable(t *testing.T) {
	hist := []perfBaselineEntry{
		{Iterations: 10, ToolCalls: 20, DurationSec: 60, Success: true},
		{Iterations: 10, ToolCalls: 20, DurationSec: 60, Success: true},
		{Iterations: 10, ToolCalls: 20, DurationSec: 60, Success: true},
		{Iterations: 10, ToolCalls: 20, DurationSec: 60, Success: true},
		{Iterations: 10, ToolCalls: 20, DurationSec: 60, Success: true},
		{Iterations: 20, ToolCalls: 40, DurationSec: 120, Success: true},
		{Iterations: 20, ToolCalls: 40, DurationSec: 120, Success: true},
		{Iterations: 20, ToolCalls: 40, DurationSec: 120, Success: true},
	}
	cm := &perfModelCM{}
	a := &Agent{
		contextManager: cm,
		modelID:        "a/b/m1",
		perfBaseline: &perfBaselineState{
			historical:  hist,
			baselineMid: perfBaselineEntry{Iterations: 10, ToolCalls: 20, DurationSec: 60},
			hasBaseline: true,
		},
	}

	a.maybeInjectPerfRegression()

	if a.perfBaseline.warnCount != 1 {
		t.Fatalf("legacy (model-less) baselines must stay comparable, got warnCount=%d", a.perfBaseline.warnCount)
	}
}

// TestRecordPerfBaseline_StoresModel verifies the recorded entry carries the
// RunStats model identity into the persisted JSON.
func TestRecordPerfBaseline_StoresModel(t *testing.T) {
	tmp := t.TempDir()
	stats := newRunStats("test prompt")
	stats.recordToolCall("read_file")
	stats.recordToolCall("read_file")
	stats.recordToolCall("read_file")
	stats.Model = "vendorX/epY/modelZ"

	recordPerfBaseline(tmp, stats)

	data, err := os.ReadFile(filepath.Join(tmp, ".ggcode", perfBaselineFile))
	if err != nil {
		t.Fatalf("baseline file not written: %v", err)
	}
	var d perfBaselineData
	if err := json.Unmarshal(data, &d); err != nil {
		t.Fatalf("failed to parse baseline: %v", err)
	}
	if len(d.Runs) != 1 {
		t.Fatalf("expected 1 recorded run, got %d", len(d.Runs))
	}
	if d.Runs[0].Model != "vendorX/epY/modelZ" {
		t.Fatalf("entry.Model = %q, want %q", d.Runs[0].Model, "vendorX/epY/modelZ")
	}
}

// TestPerfBaseline_LoadingPathScopesByModel exercises the load-time filtering
// (not the pre-frozen snapshot path): a persisted mixed-model window scoped
// to the current model yields fewer than perfBaselineMinRuns samples, so the
// injection stays silent even with hasBaseline=false.
func TestPerfBaseline_LoadingPathScopesByModel(t *testing.T) {
	tmp := t.TempDir()
	var runs []perfBaselineEntry
	for i := 0; i < 6; i++ {
		runs = append(runs, perfBaselineEntry{Model: "a/b/m1", Iterations: 10, ToolCalls: 20, DurationSec: 60, Success: true, RunID: "old"})
	}
	for i := 0; i < 3; i++ {
		runs = append(runs, perfBaselineEntry{Model: "a/b/m2", Iterations: 25, ToolCalls: 40, DurationSec: 200, Success: true, RunID: "new"})
	}
	if err := os.MkdirAll(filepath.Join(tmp, ".ggcode"), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := savePerfBaselineForTest(tmp, runs); err != nil {
		t.Fatalf("save: %v", err)
	}

	cm := &perfModelCM{}
	a := &Agent{
		contextManager: cm,
		modelID:        "a/b/m2",
		workingDir:     tmp,
		perfBaseline:   newPerfBaselineState(),
	}

	a.maybeInjectPerfRegression()

	if a.perfBaseline.warnCount != 0 || len(cm.added) != 0 {
		t.Fatalf("scoped window (<%d same-model runs) must stay silent, got warnCount=%d added=%v", perfBaselineMinRuns, a.perfBaseline.warnCount, cm.added)
	}
}

// savePerfBaselineForTest writes through the production saver, which trims to
// the rolling window; exposed so the loading-path test writes realistic data.
func savePerfBaselineForTest(workingDir string, runs []perfBaselineEntry) error {
	savePerfBaseline(workingDir, runs)
	_, err := os.Stat(filepath.Join(workingDir, ".ggcode", perfBaselineFile))
	return err
}
