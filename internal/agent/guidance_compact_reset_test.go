package agent

import (
	"testing"
)

func TestResetGuidanceCounters1651Extension(t *testing.T) {
	a := &Agent{
		trajectoryHealth: &trajectoryHealthState{warnings: 2},
		planAbandon:      &planAbandonState{warnings: 1},
		delegationOrch:   &delegationState{orphanWarnCount: 1, serialWarnCount: 2, overDelWarned: true},
		fixAmnesia:       newFixAmnesiaState(),
		driftRecurrence:  &driftRecurrenceState{fired: true, warned: true},
		crossFileImpact:  &crossFileImpactState{fired: true},
		serialRead:       &serialReadState{fired: true},
		diskSpace:        &diskSpaceState{fired: true},
	}
	a.fixAmnesia.mu.Lock()
	a.fixAmnesia.warned["build"] = true
	a.fixAmnesia.mu.Unlock()
	a.resetGuidanceCounters()
	if a.trajectoryHealth.warnings != 0 || a.planAbandon.warnings != 0 {
		t.Fatal("int counters must reset")
	}
	if a.delegationOrch.orphanWarnCount != 0 || a.delegationOrch.serialWarnCount != 0 || a.delegationOrch.overDelWarned {
		t.Fatal("delegation quotas must reset")
	}
	if len(a.fixAmnesia.warned) != 0 {
		t.Fatal("fixAmnesia warned map must clear")
	}
	if a.driftRecurrence.fired || a.driftRecurrence.warned || a.crossFileImpact.fired || a.serialRead.fired || a.diskSpace.fired {
		t.Fatal("quota bools must reset")
	}
}

func TestResetGuidanceCounters1646RightTracker(t *testing.T) {
	a := &Agent{
		overcorrection: &overcorrectionState{warnCount: 2},
	}
	a.resetGuidanceCounters()
	if a.overcorrection.warnCount != 0 {
		t.Fatal("overcorrection warnCount must reset (#1646-2)")
	}
}
