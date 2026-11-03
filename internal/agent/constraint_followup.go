package agent

import (
	"regexp"
	"strings"
	"sync"
)

const constraintFollowupMaxRounds = 2

type constraintFollowupState struct {
	mu          sync.Mutex
	round       int
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

var verdictLineRe = regexp.MustCompile(`(?m)^\s*(?:\d{1,3}[.)、]\s*)?\**\[(not done|done)\]\**\s*(.*)$`)

type unmetItem struct {
	Num  int
	Text string
}

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

func numberedVerdictPrefix(_, matchedLine string) int {

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

func (c *constraintAuditState) firedOnce() bool {
	if c == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.fired
}

func (c *constraintAuditState) items() []string {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.constraints
}

func (f *constraintFollowupState) currentRound() int {
	if f == nil {
		return 0
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.round
}

func (f *constraintFollowupState) checkAndInject(assistantText string, audit *constraintAuditState) string {
	if f == nil || audit == nil || !audit.firedOnce() {
		return ""
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	unmet, isVerdict := parseUnmetItems(assistantText)
	if !isVerdict || len(unmet) == 0 {

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
