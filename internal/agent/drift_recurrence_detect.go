package agent

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
)

const (
	driftRecurrenceNewDirThreshold = 4

	driftRecurrenceMinEditsPostWarn = 3

	driftRecurrencePostWarnWindow = 8
)

type driftRecurrenceState struct {
	preWarnDirs     map[string]bool
	preWarnBaseline int

	postWarnDirs map[string]bool

	warned bool

	warnIteration int

	postWarnEdits int

	postWarnVerifies int

	fired bool

	currentIteration int
}

func newDriftRecurrenceState() *driftRecurrenceState {
	return &driftRecurrenceState{
		preWarnDirs:  make(map[string]bool),
		postWarnDirs: make(map[string]bool),
	}
}

func (d *driftRecurrenceState) reset() {
	d.preWarnDirs = make(map[string]bool)
	d.postWarnDirs = make(map[string]bool)
	d.warned = false
	d.warnIteration = 0
	d.postWarnEdits = 0
	d.postWarnVerifies = 0
	d.fired = false
	d.currentIteration = 0
}

func (d *driftRecurrenceState) recordIteration(iteration int) {
	d.currentIteration = iteration
}

func (d *driftRecurrenceState) recordEdit(filePath string) {
	if filePath == "" {
		return
	}
	fp := filepath.Clean(filePath)
	dir := filepath.Dir(fp)
	sig := dirSignature(dir)
	if sig == "" {
		return
	}

	if d.warned {
		d.postWarnDirs[sig] = true
		d.postWarnEdits++
	} else {
		d.preWarnDirs[sig] = true
	}
}

func (d *driftRecurrenceState) recordVerification() {
	if d.warned {
		d.postWarnVerifies++
	}
}

func (d *driftRecurrenceState) markWarning(iteration int) {
	if !d.warned {
		d.warned = true
		d.warnIteration = iteration

		d.preWarnBaseline = len(d.preWarnDirs)
	}
}

func (d *driftRecurrenceState) check() string {
	if d.fired || !d.warned {
		return ""
	}

	if d.currentIteration-d.warnIteration > driftRecurrencePostWarnWindow {
		return ""
	}

	if d.postWarnEdits < driftRecurrenceMinEditsPostWarn {
		return ""
	}

	newDirs := 0
	for pd := range d.postWarnDirs {
		if !d.preWarnDirs[pd] {
			newDirs++
		}
	}

	verifyRatio := 0.0
	if d.postWarnEdits > 0 {
		verifyRatio = float64(d.postWarnVerifies) / float64(d.postWarnEdits)
	}

	if newDirs < driftRecurrenceNewDirThreshold {
		if newDirs <= 1 {
			return ""
		}

		if d.preWarnBaseline == 0 && newDirs <= 2 {
			return ""
		}

		if !(d.postWarnVerifies == 0) {
			return ""
		}
	}

	d.fired = true

	debug.Log("drift_recurrence",
		"drift recurrence: %d new dirs post-warning, %d edits, %d verifies (ratio=%.2f)",
		newDirs, d.postWarnEdits, d.postWarnVerifies, verifyRatio)

	var sb strings.Builder
	header := fmt.Sprintf(
		"[Drift Recurrence] After receiving a drift warning at iteration %d, "+
			"you have continued the same pattern: %d new directories touched, "+
			"%d edits with only %d verification actions.",
		d.warnIteration, newDirs, d.postWarnEdits, d.postWarnVerifies)
	sb.WriteString(header)

	if newDirs >= driftRecurrenceNewDirThreshold {
		sb.WriteString(" Your file scope is still expanding despite the warning.")
	}
	if verifyRatio == 0 && d.postWarnEdits >= driftRecurrenceMinEditsPostWarn {
		sb.WriteString(" You have not run ANY verification (build/test/lint) since the warning.")
	}

	sb.WriteString(
		"\n\nThis suggests the earlier warning was acknowledged but not acted upon. " +
			"Performative compliance -- saying 'you're right' then continuing the same behavior -- " +
			"wastes iterations and compounds errors. STOP and genuinely reassess: " +
			"(1) re-read the original task requirements, " +
			"(2) identify which changes are actually necessary vs. optional, " +
			"(3) run verification (build/test) before making any more edits, " +
			"(4) if the task genuinely requires this scope, explain WHY before continuing.")

	return sb.String()
}

func (a *Agent) driftRecurrenceRecord(toolName string, fileHint string, args string, ok bool) {
	if a.driftRecurrence == nil {
		return
	}

	if productiveEditTools[toolName] && ok {
		a.driftRecurrence.recordEdit(fileHint)
	}
	if toolName == "run_command" {
		if classifyDebtAction(toolName, args) == debtVerifying {
			a.driftRecurrence.recordVerification()
		}
		return
	}
	if verificationDebtTools[toolName] == debtVerifying {
		a.driftRecurrence.recordVerification()
	}
}

func (a *Agent) driftRecurrenceMarkWarn(iteration int) {
	if a.driftRecurrence != nil {
		a.driftRecurrence.markWarning(iteration)
	}
}

func (a *Agent) driftRecurrenceCheck() string {
	if a.driftRecurrence == nil {
		return ""
	}
	return a.driftRecurrence.check()
}

func (a *Agent) resetDriftRecurrence() {
	if a.driftRecurrence != nil {
		a.driftRecurrence.reset()
	}
}
