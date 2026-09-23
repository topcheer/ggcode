package agent

// Tests for the task completion evidence predicate (sa-183).

import (
	"testing"
)

func statsWithCommands(cmds []string, errs []string) *RunStats {
	s := newRunStats("test")
	s.CommandsRun = append(s.CommandsRun, cmds...)
	s.Errors = append(s.Errors, errs...)
	return s
}

func TestTaskVerificationEvidence_NilAgent(t *testing.T) {
	if TaskVerificationEvidence(nil)() {
		t.Error("nil agent must report no evidence")
	}
}

func TestTaskVerificationEvidence_NoRuns(t *testing.T) {
	a := &Agent{}
	if TaskVerificationEvidence(a)() {
		t.Error("agent with no runs must report no evidence")
	}
}

func TestTaskVerificationEvidence_LiveRunVerified(t *testing.T) {
	a := &Agent{}
	a.setLiveRunStats(statsWithCommands([]string{"go test ./..."}, nil))
	defer a.setLiveRunStats(nil)
	if !TaskVerificationEvidence(a)() {
		t.Error("live run with successful go test must report evidence")
	}
}

func TestTaskVerificationEvidence_LiveRunVerificationTool(t *testing.T) {
	a := &Agent{}
	s := newRunStats("test")
	s.ToolCalls = map[string]int{"lsp_diagnostics": 2}
	a.setLiveRunStats(s)
	defer a.setLiveRunStats(nil)
	if !TaskVerificationEvidence(a)() {
		t.Error("live run with lsp_diagnostics must report evidence")
	}
}

func TestTaskVerificationEvidence_LiveRunUnverified(t *testing.T) {
	a := &Agent{}
	s := newRunStats("test")
	s.ToolCalls = map[string]int{"read_file": 5}
	s.CommandsRun = []string{"ls -la"}
	a.setLiveRunStats(s)
	defer a.setLiveRunStats(nil)
	if TaskVerificationEvidence(a)() {
		t.Error("read-only session must report no evidence")
	}
}

func TestTaskVerificationEvidence_FailedTestCommandNotEvidence(t *testing.T) {
	a := &Agent{}
	a.setLiveRunStats(statsWithCommands(
		[]string{"go test ./..."},
		[]string{"go test ./... exit status 1 FAIL"},
	))
	defer a.setLiveRunStats(nil)
	if TaskVerificationEvidence(a)() {
		t.Error("failed verification command must not count (#1521)")
	}
}

func TestTaskVerificationEvidence_FallbackToLastRunStats(t *testing.T) {
	a := &Agent{}
	// No live run; last run carried evidence.
	a.mu.RLock()
	a.lastRunStats = statsWithCommands([]string{"make verify-ci"}, nil)
	a.mu.RUnlock()
	if !TaskVerificationEvidence(a)() {
		t.Error("between runs, last run's evidence must be used")
	}
	// Live run (unverified) takes precedence over last run's evidence.
	s := newRunStats("test")
	s.ToolCalls = map[string]int{"grep": 1}
	a.setLiveRunStats(s)
	defer a.setLiveRunStats(nil)
	if TaskVerificationEvidence(a)() {
		t.Error("live unverified run must shadow last run's evidence")
	}
}
