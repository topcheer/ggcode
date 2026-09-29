package tui

import (
	"testing"

	"github.com/topcheer/ggcode/internal/knight"
)

// #2815: knight task/proposal result handlers must stop the spinner and
// clear the four status fields, aligning with update_done.go completion
// paths. Without Stop() the spinner tick chain keeps re-rendering forever.
func TestKnightResultStopsSpinner2815(t *testing.T) {
	m := newTestModel()
	m.setLoading(true)
	m.spinner.Start("Knight task")
	m.statusActivity = "Knight task"
	m.statusToolName = "probe"
	m.statusToolArg = "x.go"
	m.statusToolCount = 3

	m2v, _ := m.handleKnightTaskResultMsg(knightTaskResultMsg{
		Result: knight.TaskResult{TaskName: "demo"},
	})
	m2 := m2v.(Model)
	if m2.spinner.IsActive() {
		t.Fatal("spinner must be stopped after knight task result")
	}
	if m2.statusActivity != "" || m2.statusToolName != "" || m2.statusToolArg != "" || m2.statusToolCount != 0 {
		t.Fatalf("status fields must be cleared, got %+v", m2.statusActivity)
	}

	m3 := newTestModel()
	m3.setLoading(true)
	m3.spinner.Start("Knight proposal")
	m3.statusActivity = "Knight proposal"
	m4v, _ := m3.handleKnightProjectProposalResultMsg(knightProjectProposalResultMsg{})
	m4 := m4v.(Model)
	if m4.spinner.IsActive() {
		t.Fatal("spinner must be stopped after knight proposal result")
	}
	if m4.statusActivity != "" || m4.statusToolName != "" || m4.statusToolArg != "" || m4.statusToolCount != 0 {
		t.Fatal("status fields must be cleared after knight proposal result")
	}
}
