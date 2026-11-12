package swarm

import (
	"strings"
	"testing"
)

func TestAggregateResultsFusesWithoutConflict(t *testing.T) {
	rep := AggregateResults(map[string]string{
		"tm-2": "added e2e probes in internal/agent/probe_test.go",
		"tm-1": "refactored auth in internal/agent/auth.go",
	})
	if rep == nil {
		t.Fatal("expected report")
	}
	if rep.TeammateCount != 2 {
		t.Fatalf("count: %+v", rep)
	}
	if len(rep.Files) != 2 || rep.Files[0] != "internal/agent/auth.go" {
		t.Fatalf("files must be sorted: %v", rep.Files)
	}
	if rep.NeedsAdjudication || len(rep.Conflicts) != 0 {
		t.Fatalf("disjoint files must not conflict: %+v", rep.Conflicts)
	}
	if !strings.Contains(rep.Fused, "### tm-1") || !strings.Contains(rep.Fused, "No file-level conflicts") {
		t.Fatalf("fusion missing labels: %s", rep.Fused)
	}
}

func TestAggregateResultsFlagsSameFileConflict(t *testing.T) {
	rep := AggregateResults(map[string]string{
		"tm-1": "rewrote handler in internal/server/handler.go, all green",
		"tm-2": "also patched internal/server/handler.go with a different fix",
	})
	if !rep.NeedsAdjudication {
		t.Fatal("same-file overlap must flag adjudication")
	}
	if len(rep.Conflicts) != 1 || rep.Conflicts[0].Path != "internal/server/handler.go" {
		t.Fatalf("conflict detail wrong: %+v", rep.Conflicts)
	}
	if len(rep.Conflicts[0].Teammates) != 2 || rep.Conflicts[0].Teammates[0] != "tm-1" {
		t.Fatalf("owners must be sorted: %v", rep.Conflicts[0].Teammates)
	}
	if !strings.Contains(rep.Fused, "adjudication required") || !strings.Contains(rep.Fused, "do NOT treat overlapping edits as merged") {
		t.Fatalf("fusion must annotate conflicts: %s", rep.Fused)
	}
}

func TestAggregateResultsEmpty(t *testing.T) {
	if rep := AggregateResults(map[string]string{}); rep != nil {
		t.Fatal("empty input must return nil")
	}
	if rep := AggregateResults(map[string]string{"tm-1": ""}); rep == nil {
		t.Fatal("blank text still counts as a teammate output; expected report")
	}
}
