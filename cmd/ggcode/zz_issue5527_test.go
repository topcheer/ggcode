package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/agentruntime"
)

// TestIssue5527WiringPins anchors the #5527 production wiring: both call
// sites that build SweepOptions must pass the config-backed dry-run fields.
// A regression that drops the wiring leaves the config flag inert (the exact
// gap the 2026-11-13 issue-review found after PR #5531 landed the mechanism).
func TestIssue5527WiringPins(t *testing.T) {
	anchors := []struct {
		file string
		need string
		what string
	}{
		{"cmd/ggcode/root.go", "cfg.MemoryCuratorSweepDryRunRounds()", "weekly ticker must wire config dry_run_rounds"},
		{"cmd/ggcode/root.go", "cfg.MemoryCuratorSweepForceDryRun()", "weekly ticker must wire config dry_run force-pin"},
		{"cmd/ggcode/memory_cmd.go", "cfg.MemoryCuratorSweepDryRunRounds()", "manual sweep must wire config dry_run_rounds"},
		{"cmd/ggcode/memory_cmd.go", "cfg.MemoryCuratorSweepForceDryRun()", "manual sweep must wire config dry_run force-pin"},
	}
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Dir(filepath.Dir(wd)) // cmd/ggcode -> repo root
	for _, a := range anchors {
		data, rerr := os.ReadFile(filepath.Join(root, a.file))
		if rerr != nil {
			t.Fatalf("%s: %v", a.file, rerr)
		}
		if !strings.Contains(string(data), a.need) {
			t.Errorf("%s: %s — anchor %q missing", a.file, a.what, a.need)
		}
	}
}

// TestIssue5527ManualSweepDryRunOutput pins the CLI surface: a dry round
// must say DRY-RUN loudly so operators can tell observation from apply.
func TestIssue5527ManualSweepDryRunOutput(t *testing.T) {
	rep := &agentruntime.SweepReport{DryRun: true}
	if s := dryRunSuffix(rep); !strings.Contains(s, "DRY-RUN") {
		t.Errorf("dry round suffix must carry DRY-RUN marker, got %q", s)
	}
	rep2 := &agentruntime.SweepReport{DryRun: false}
	if s := dryRunSuffix(rep2); strings.Contains(s, "DRY-RUN") {
		t.Errorf("applied round suffix must not carry DRY-RUN marker, got %q", s)
	}
	if s := dryRunSuffix(nil); strings.Contains(s, "DRY-RUN") {
		t.Errorf("nil report must not carry DRY-RUN marker, got %q", s)
	}
}
