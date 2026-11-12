package agent

import (
	"strings"
	"testing"
	"time"
)

// TestIngestTeammateExperience_OutcomeMapping pins the anti-fabrication
// contract for teammate trust data: entries carry their real outcome;
// legacy entries without one are marked unverified and never counted as
// success (they used to be hardcoded Success:true).
func TestIngestTeammateExperience_OutcomeMapping(t *testing.T) {
	dir := t.TempDir()
	base := time.Now().Add(-time.Hour)
	writeLedger(t, dir, []map[string]any{
		{"ts": base, "team_id": "t1", "teammate": "tm-1", "digest": "did well", "outcome": "success"},
		{"ts": base.Add(time.Minute), "team_id": "t1", "teammate": "tm-2", "digest": "blew up", "outcome": "failed"},
		{"ts": base.Add(2 * time.Minute), "team_id": "t1", "teammate": "tm-3", "digest": "legacy entry"},
	})

	s := newTrajIntelState()
	s.ingestTeammateExperience(dir)
	if len(s.learnings) != 3 {
		t.Fatalf("want 3 learnings, got %d", len(s.learnings))
	}

	byMate := map[string]trajectoryLearning{}
	for _, l := range s.learnings {
		byMate[l.Task] = l
	}
	if !byMate["swarm/tm-1"].Success {
		t.Error("outcome=success must map to Success=true")
	}
	if byMate["swarm/tm-2"].Success {
		t.Error("outcome=failed must map to Success=false (the fabricated-success bug)")
	}
	if byMate["swarm/tm-3"].Success {
		t.Error("legacy no-outcome entry must NOT count as success")
	}
	if !strings.Contains(byMate["swarm/tm-3"].Insight, "(unverified outcome)") {
		t.Errorf("legacy entry must be marked unverified, got: %q", byMate["swarm/tm-3"].Insight)
	}
}
