package agent

import (
	"fmt"
	"strings"
)

// Plan-repair verdicts (#plan-repair-verdict, P1). Plan monitoring used to be
// fire-once prose: a single unaddressed-items warning per run, then silence,
// even when drift persisted or deepened. Frontier practice (ContextFlow
// arXiv:2605.19314 scoped updates; PhyAgentOS arXiv:2607.16636 three-value
// verdicts) models monitoring as a state machine: keep watching, and
// escalate when drift persists across consecutive checks.
type planVerdict int

const (
	verdictContinue planVerdict = iota // first stall: plain re-nudge
	verdictRepair                      // persistent drift: demand a revised plan
	verdictAbandon                     // deep stall: hand the decision to the user
)

const (
	// pdMaxVerdicts caps total emitted verdicts per run so a doomed loop
	// cannot spam (continue -> repair -> abandon, then silence).
	pdMaxVerdicts = 3
	// pdStallThreshold: unaddressed ratio at or above this counts as a
	// stalled check.
	pdStallThreshold = 0.5
)

// planVerdictFor maps a stalled-check streak to the escalation verdict.
// streak is 1-based (first consecutive stalled check == 1).
func planVerdictFor(streak int) planVerdict {
	switch {
	case streak <= 1:
		return verdictContinue
	case streak == 2:
		return verdictRepair
	default:
		return verdictAbandon
	}
}

// renderPlanVerdict builds the structured prompt for a verdict. The repair
// verdict explicitly demands a revised plan via todo_write (research P1
// item 3); the abandon verdict hands the decision to the user.
func renderPlanVerdict(v planVerdict, unaddressed []string, total int) string {
	var b strings.Builder
	switch v {
	case verdictContinue:
		b.WriteString("[Plan Drift Detection] ")
		if len(unaddressed) <= 3 {
			fmt.Fprintf(&b, "%d of %d plan items appear unaddressed:\n", len(unaddressed), total)
		} else {
			fmt.Fprintf(&b, "%d of %d plan items appear unaddressed (showing first 3):\n", len(unaddressed), total)
			unaddressed = unaddressed[:3]
		}
		for _, item := range unaddressed {
			b.WriteString("  - " + item + "\n")
		}
		b.WriteString("\nReview these plan items and address any gaps before completing.")
	case verdictRepair:
		fmt.Fprintf(&b, "[Plan Repair] Plan drift persisted across %d consecutive checks: %d of %d items still unaddressed (%s).\n",
			2, len(unaddressed), total, truncatePlanItem(strings.Join(unaddressed, "; "), 100))
		b.WriteString("This approach is not converging on the plan. Stop and revise explicitly:\n")
		b.WriteString("1. Write a REVISED plan with todo_write (keep completed items, drop or re-scope the stuck ones).\n")
		b.WriteString("2. State in one line what blocked the original plan.\n")
		b.WriteString("3. Continue with the revised plan; do not keep pushing the same failing approach.")
	case verdictAbandon:
		fmt.Fprintf(&b, "[Plan Escalation] Plan drift persisted across %d+ checks after a repair prompt. ", 3)
		b.WriteString("The current plan appears unachievable with this approach. Ask the user how to proceed ")
		b.WriteString("(re-scope the goal, accept partial completion, or change strategy) instead of burning further iterations.")
	}
	return b.String()
}
