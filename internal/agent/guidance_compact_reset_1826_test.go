package agent

import (
	"os"
	"strings"
	"testing"
)

var quotaRegistry = []string{

	"verifyDebt", "infoScent", "futileCycle", "editPropagation",
	"constraintAmnesia", "correctionSpiral", "errorRush", "bareEditStreak",
	"attentionFragment", "toolThermal", "strategyFixation", "queryConverge",
	"errorCompound", "successDeclare", "undoBlind", "cfDep", "editCoverage",
	"foresightCalib", "criteriaDrift", "editOscillation", "spiralState",
	"capBoundary", "overcorrection",

	"silentError", "scopeDrift", "errStrategyLoop", "commitHint",
	"convergenceLock", "giveupRevert", "inputUnderspec", "integrationMonitor",
	"iterPressure", "prematureCommit", "reproducerLifecycle", "reversibility",
	"toolRedundancy", "tunnelVision",

	"batchCoupling", "taintInfluence", "orphanFile", "argSizeGuardFires",
	"crossDetectorConsensus", "scopeCreep", "searchParamGuard",
	"prematureSuccess", "falsePremise", "complexityGate", "permDenyStreak",
	"wtInvalidation", "toolEff", "patchExhaust",
}

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
