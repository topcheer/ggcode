package agent

import (
	"os"
	"strings"
	"testing"
)

// quotaRegistry is the #1826 registration mechanism: every quota-bearing
// detector field that holds a run-scoped injection quota MUST have an entry
// in resetGuidanceCounters. When you add a new detector with a quota field
// (fired/warned bool or warnCount/warnings int), add its field name here AND
// the reset entry - the source pin below fails the build's test stage if the
// reset entry goes missing, which is what let ~14 detectors silently stay
// muted after mid-run compaction for months (fixed family-by-family in
// #1465-A/#1572-C/#1605-A/#1646/#1651/#1843 before this registry).
var quotaRegistry = []string{
	// pre-#1826 entries (behavior pinned by earlier issues)
	"verifyDebt", "infoScent", "futileCycle", "editPropagation",
	"constraintAmnesia", "correctionSpiral", "errorRush", "bareEditStreak",
	"attentionFragment", "toolThermal", "strategyFixation", "queryConverge",
	"errorCompound", "successDeclare", "undoBlind", "cfDep", "editCoverage",
	"foresightCalib", "criteriaDrift", "editOscillation", "spiralState",
	"capBoundary", "overcorrection",
	// #1826 family closure
	"silentError", "scopeDrift", "errStrategyLoop", "commitHint",
	"convergenceLock", "giveupRevert", "inputUnderspec", "integrationMonitor",
	"iterPressure", "prematureCommit", "reproducerLifecycle", "reversibility",
	"toolRedundancy", "tunnelVision",
}

// Test1826QuotaRegistryPin asserts every registered quota detector has an
// entry in resetGuidanceCounters. This is the mechanical guard the file
// lacked: previously each gap was discovered issue-by-issue after a detector
// went silent post-compaction.
func Test1826QuotaRegistryPin(t *testing.T) {
	src, err := os.ReadFile("guidance_compact_reset.go")
	if err != nil {
		t.Fatalf("read guidance_compact_reset.go: %v", err)
	}
	body := string(src)
	for _, field := range quotaRegistry {
		if !strings.Contains(body, "a."+field+" ") {
			t.Errorf("quota detector %q has no reset entry in resetGuidanceCounters - add the entry (quota-only) or move the field to the documented whitelist", field)
		}
	}
}
