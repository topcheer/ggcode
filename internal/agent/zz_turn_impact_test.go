package agent

import (
	"fmt"
	"strings"
	"testing"
)

func TestTurnImpactReportAggregatesAndTiers(t *testing.T) {
	r := newTurnImpactRecorder()
	if rep := r.report(); rep != nil {
		t.Fatal("empty recorder must report nil")
	}

	r.recordFileEdit("edit_file", "/w/b.go")
	r.recordFileEdit("write_file", "/w/a.go")
	r.recordFileEdit("edit_file", "/w/b.go") // dedup, first tool wins
	r.recordShellMutation("go test ./...", false)
	r.recordShellMutation("git push --force origin main", true)

	rep := r.report()
	if rep == nil {
		t.Fatal("expected report")
	}
	if len(rep.Files) != 2 || rep.Files[0] != "/w/a.go" || rep.Files[1] != "/w/b.go" {
		t.Fatalf("files must be sorted+deduped: %v", rep.Files)
	}
	if rep.FirstTools["/w/b.go"] != "edit_file" {
		t.Fatalf("first tool must win: %v", rep.FirstTools)
	}
	if rep.ShellCount != 2 || rep.Destructive != 1 {
		t.Fatalf("shell counts wrong: %+v", rep)
	}
	if rep.RiskTier != "high" {
		t.Fatalf("destructive shell must pin high tier, got %s", rep.RiskTier)
	}

	txt := rep.render()
	for _, want := range []string{"Turn Impact", "Risk tier: high", "/w/a.go", "destructive: 1"} {
		if !strings.Contains(txt, want) {
			t.Fatalf("render missing %q:\n%s", want, txt)
		}
	}
}

func TestTurnImpactTiersWithoutDestructive(t *testing.T) {
	r := newTurnImpactRecorder()
	r.recordShellMutation("go build ./...", false)
	if rep := r.report(); rep.RiskTier != "medium" {
		t.Fatalf("shell-only must be medium, got %s", rep.RiskTier)
	}

	r2 := newTurnImpactRecorder()
	for i := 0; i < 7; i++ {
		r2.recordFileEdit("edit_file", fmt.Sprintf("/w/f%d.go", i))
	}
	r2.recordFileEdit("edit_file", "/w/g.go")
	if rep := r2.report(); rep.RiskTier != "medium" {
		t.Fatalf("many files must be medium, got %s", rep.RiskTier)
	}

	r3 := newTurnImpactRecorder()
	r3.recordFileEdit("edit_file", "/w/one.go")
	if rep := r3.report(); rep.RiskTier != "low" {
		t.Fatalf("single file must be low, got %s", rep.RiskTier)
	}
}

func TestShellDestructiveConservative(t *testing.T) {
	cases := []struct {
		cmd  string
		want bool
	}{
		{"git push origin main", true},
		{"git reset --hard HEAD~1", true},
		{"rm -rf build/", true},
		{"go test -tags goolm ./...", false},
		{"ls -la", false},
		{"GIT PUSH via uppercase", true},
	}
	for _, c := range cases {
		if got := shellDestructive(c.cmd); got != c.want {
			t.Errorf("shellDestructive(%q)=%v want %v", c.cmd, got, c.want)
		}
	}
}

func TestTurnImpactResetClears(t *testing.T) {
	r := newTurnImpactRecorder()
	r.recordFileEdit("edit_file", "/w/x.go")
	r.reset()
	if rep := r.report(); rep != nil {
		t.Fatal("reset must clear all records")
	}
}
