package agent

// constraint_followup.go implements the back half of the AREX loop
// (arXiv 2607.21461): the r392 constraint audit extracts the task's
// requirement list and forces a per-item [done]/[not done] verdict, but
// that signal was previously one-shot prompt text consumed by nothing.
// AREX's core differentiator is the CLOSED loop - unmet constraints drive
// targeted follow-up work, then the verdict is re-stated and re-checked.
//
// This file parses the assistant's verdict reply, keeps the unmet items,
// and injects (at most twice) a targeted follow-up prompt built ONLY from
// the unmet constraint texts - deterministic, zero extra LLM calls
// ("targeted" per AREX). After the bounded rounds, one closing prompt
// forces explicit disclosure of remaining gaps to the user (bounded
// recursion; cf. beam abstention - never loop forever on an unfixable
// item).
//
// Neighbor separation (verified r492): constraint_amnesia reminds that
// constraints exist; fulfillment_gate is aggregate; redundantReverify
// PREVENTS re-verification rather than scheduling it; claim_citation is
// static post-hoc linking. None closes the loop.

import (
	"regexp"
	"strings"
	"sync"
)

// constraintFollowupMaxRounds bounds the verify->follow-up->re-verify
// loop. Two rounds cover "addressed it now" and "second attempt"; beyond
// that the item is likely out of scope or undoable, and further nagging
// wastes iterations (diminishing-edit territory).
const constraintFollowupMaxRounds = 2

// constraintFollowupState tracks the closed-loop audit cycle.
type constraintFollowupState struct {
	mu sync.Mutex
	// round counts injected follow-up prompts so far.
	round int
	// finalNotice guards the one-shot closing prompt (after the last
	// round still shows unmet items, remaining gaps must be disclosed).
	finalNotice bool
}

func newConstraintFollowupState() *constraintFollowupState {
	return &constraintFollowupState{}
}

func (c *constraintFollowupState) reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.round = 0
	c.finalNotice = false
}

// verdictRe matches an audit verdict line: optional leading item number
// (matching the audit prompt's "N. item" enumeration), then the bracketed
// verdict marker. Tolerates markdown emphasis and both orders of number
// and marker.
var verdictLineRe = regexp.MustCompile(`(?m)^\s*(?:\d{1,3}[.)、]\s*)?\**\[(not done|done)\]\**\s*(.*)$`)

// unmetItem is one constraint the assistant verdict-marked [not done].
type unmetItem struct {
	// Num is the 1-based item number when the verdict line carried one;
	// 0 means the line was unnumbered.
	Num int
	// Text is the constraint text: the audit-listed item for numbered
	// verdicts, else the trailing evidence text on the verdict line.
	Text string
}

// parseUnmetItems extracts the [not done] entries from an audit verdict
// reply. Returns (unmet, isVerdictReply): isVerdictReply is false when
// the text contains no verdict lines at all - such a reply is not an
// answer to the audit prompt and must not consume a follow-up round.
func parseUnmetItems(assistantText string) ([]unmetItem, bool) {
	if strings.TrimSpace(assistantText) == "" {
		return nil, false
	}
	matches := verdictLineRe.FindAllStringSubmatch(assistantText, -1)
	if len(matches) == 0 {
		return nil, false
	}
	var unmet []unmetItem
	for _, m := range matches {
		if !strings.EqualFold(m[1], "not done") {
			continue
		}
		item := unmetItem{Text: strings.TrimSpace(m[2])}
		// Recover the numbered item when present: the audit prompt
		// enumerates "N. item", so a numbered verdict maps straight
		// back to the listed constraint text.
		if pre := numberedVerdictPrefix(assistantText, m[0]); pre > 0 {
			item.Num = pre
		}
		if item.Text == "" && item.Num == 0 {
			item.Text = "（见上文明细 / see item list above）"
		}
		unmet = append(unmet, item)
	}
	return unmet, true
}

// numberedVerdictPrefix extracts the leading item number, if any, from a
// matched verdict line. Returns 0 for unnumbered lines.
func numberedVerdictPrefix(_, matchedLine string) int {
	// #3799 A: the match includes the `^\s*` indentation - markdown
	// indented list replies (`  1. [not done] ...`) started parsing at a
	// space, broke immediately, and silently lost the constraint number,
	// degrading the follow-up prompt to quoting verdict-tail text instead
	// of the audited constraint line. Skip leading whitespace first.
	matchedLine = strings.TrimLeft(matchedLine, " \t")
	n := 0
	for _, r := range matchedLine {
		if r >= '0' && r <= '9' {
			n = n*10 + int(r-'0')
			continue
		}
		break
	}
	if n > 0 && n <= constraintAuditMaxItems {
		return n
	}
	return 0
}

// armed reports whether the audit fired (its verdict reply is the next
// assistant turn we might parse). Exposed on the audit state.
func (c *constraintAuditState) firedOnce() bool {
	if c == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.fired
}

// items returns the extracted constraint list (nil when none).
func (c *constraintAuditState) items() []string {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.constraints
}

// currentRound is a lock-safe read for logging.
func (f *constraintFollowupState) currentRound() int {
	if f == nil {
		return 0
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.round
}

// checkAndInject closes the AREX loop: parse the verdict reply, and when
// items are [not done], inject the targeted follow-up prompt. Bounded to
// constraintFollowupMaxRounds, then one explicit-gaps closing notice.
// Returns "" when there is nothing to inject (no verdict reply, all
// done, or budget exhausted).
func (f *constraintFollowupState) checkAndInject(assistantText string, audit *constraintAuditState) string {
	if f == nil || audit == nil || !audit.firedOnce() {
		return ""
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	unmet, isVerdict := parseUnmetItems(assistantText)
	if !isVerdict || len(unmet) == 0 {
		// All [done] (or not a verdict reply): the loop closes cleanly.
		return ""
	}
	list := audit.items()
	if f.round < constraintFollowupMaxRounds {
		f.round++
		var b strings.Builder
		b.WriteString("[constraint-followup] Verdict marks ")
		b.WriteString(itoaPivot(len(unmet)))
		b.WriteString(" item(s) [not done]. For EACH unmet item below, run one targeted search or tool call addressing ONLY that item, then re-state its verdict line:\n")
		for _, u := range unmet {
			b.WriteString("  - ")
			if u.Num > 0 && u.Num <= len(list) {
				b.WriteString("item ")
				b.WriteString(itoaPivot(u.Num))
				b.WriteString(": ")
				b.WriteString(list[u.Num-1])
			} else if u.Text != "" {
				b.WriteString(u.Text)
			} else {
				b.WriteString("unmet item")
			}
			b.WriteString("\n")
		}
		b.WriteString("Address them now; do not finish around them.")
		return b.String()
	}
	if !f.finalNotice {
		f.finalNotice = true
		var b strings.Builder
		b.WriteString("[constraint-followup] ")
		b.WriteString(itoaPivot(len(unmet)))
		b.WriteString(" item(s) remain [not done] after follow-up. Do not attempt further fixes: state each remaining gap explicitly in your final answer so the user knows what was not delivered and why.")
		return b.String()
	}
	return ""
}
