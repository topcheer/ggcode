package agent

import (
	"regexp"
	"strconv"
	"strings"
	"sync"
)

// constraintAuditState implements the constraint-wise audit from AREX
// (arXiv 2607.21461): tasks that list MULTIPLE explicit requirements are
// audited item-by-item before the agent finishes, instead of relying on a
// single aggregate "did the work match the request" heuristic.
//
// ggcode already had the neighbors (all verified during r392 research):
//   - fulfillment_gate: aggregate multi-part heuristic (filesEdited >=
//     ceil(parts/2)) - fires once, no per-item structure
//   - constraint_amnesia: reminds that constraints exist as context grows,
//     no pass/fail audit
//   - criteria_drift: detects self-relaxed wording, not item coverage
//
// This audit covers ONLY structurally-listed requirements (numbered or
// bulleted lines in the user's task message): those decompose
// deterministically with zero extraction risk. Prose multi-requests stay
// with the fulfillment gate. The injected prompt forces a per-item binary
// verdict ([done]/[not done]) - the cheap structured audit AREX shows is
// the right control signal - and only unmet items need follow-up work.
type constraintAuditState struct {
	mu sync.Mutex
	// constraints holds the extracted requirement items; nil until a
	// qualifying task message is seen (or ruled out).
	constraints []string
	// fired gates the one-shot injection.
	fired bool
}

const (
	// constraintAuditMinItems: single-requirement tasks have no list to
	// audit (the fulfillment gate covers them).
	constraintAuditMinItems = 2
	// constraintAuditMaxItems caps pathological lists; beyond this the
	// message is likely a log/paste, not a task list.
	constraintAuditMaxItems = 12
)

func newConstraintAuditState() *constraintAuditState {
	return &constraintAuditState{}
}

// reset clears per-run state (constraints are re-extracted from each new
// task message).
func (c *constraintAuditState) reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.constraints = nil
	c.fired = false
}

var (
	// Numbered list item: "1." / "1、" / "1)" / "(1)" line starts, plus the
	// Chinese ordinal forms the comment has always claimed (#3093):
	// "步骤1：" and "一、/二、/十、" ordinals. Ordinal branches REQUIRE a
	// separator so prose like "一定要修复…" is not misread as a list item.
	// The spacing class tolerates full-width spaces.
	constraintNumberedRe = regexp.MustCompile(`^\s*(步骤\s*\d{1,3}[、.．:：]|[一二三四五六七八九十]{1,3}[、.．:：]|\d{1,3}[.)、]|[(（]\d{1,3}[)）])[\s　]*(.{3,200})$`)
	// Bulleted list item: "- " / "* " / "• ".
	constraintBulletRe = regexp.MustCompile(`^\s*[-*•]\s+(.{3,200})$`)
)

// buildConstraints extracts requirement items from a task message. Only
// structural lists qualify (>=2 items): mixed prose is left to the
// fulfillment gate. Fenced/indented code is skipped so pasted logs do not
// masquerade as requirements.
func buildConstraints(prompt string) []string {
	if prompt == "" || isReadOnlyTask(prompt) {
		return nil
	}
	inCode := false
	var numbered, bullets []string
	var numberedOrdinals []int
	for _, raw := range strings.Split(prompt, "\n") {
		trimmed := strings.TrimRight(raw, " \t")
		if strings.HasPrefix(strings.TrimSpace(trimmed), "```") {
			inCode = !inCode
			continue
		}
		if inCode {
			continue
		}
		// Skip indented (quoted/code) lines.
		if raw != strings.TrimLeft(raw, " \t") && strings.HasPrefix(raw, "    ") {
			continue
		}
		if m := constraintNumberedRe.FindStringSubmatch(trimmed); m != nil {
			numbered = append(numbered, strings.TrimSpace(m[2]))
			numberedOrdinals = append(numberedOrdinals, ordinalValue(m[1]))
			continue
		}
		if m := constraintBulletRe.FindStringSubmatch(trimmed); m != nil {
			bullets = append(bullets, strings.TrimSpace(m[1]))
		}
	}
	// A message usually mixes styles only incidentially; prefer the
	// dominant (longer) list, and require it to look like a task list.
	items := numbered
	if len(bullets) > len(numbered) {
		items = bullets
	} else if len(numbered) > 0 && !ordinalSequenceCoherent(numberedOrdinals) {
		// #3095: explanatory prose enumerations ("一、这是性能原因" /
		// "二、这是设计原因" as background, not requirements) match the
		// ordinal branch structurally. A genuine task list starts at 1/一
		// and increments strictly; prose picks arbitrary or isolated
		// ordinals. Incoherent => not a task list; the one-shot audit
		// injection (which forces [done]-checking each line) is skipped.
		return nil
	}
	if len(items) < constraintAuditMinItems || len(items) > constraintAuditMaxItems {
		return nil
	}
	return items
}

// ordinalValue converts a matched ordinal prefix (regex group 1) to its
// numeric value: Arabic forms directly, Chinese ordinals (一...九十九) via
// digit parse. Returns -1 when unparseable - treated as breaking coherence.
func ordinalValue(prefix string) int {
	prefix = strings.TrimSpace(prefix)
	prefix = strings.TrimPrefix(prefix, "步骤")
	prefix = strings.Trim(prefix, "（(）)、.．:： \u3000")
	if prefix == "" {
		return -1
	}
	if n, err := strconv.Atoi(prefix); err == nil {
		return n
	}
	return chineseOrdinal(prefix)
}

var chineseDigit = map[rune]int{'一': 1, '二': 2, '三': 3, '四': 4, '五': 5, '六': 6, '七': 7, '八': 8, '九': 9}

// chineseOrdinal parses 一..九十九 (task lists never go higher); -1 for
// anything else.
func chineseOrdinal(s string) int {
	if s == "十" {
		return 10
	}
	r := []rune(s)
	switch len(r) {
	case 1:
		if v, ok := chineseDigit[r[0]]; ok {
			return v
		}
		return -1
	case 2:
		if d, ok := chineseDigit[r[0]]; ok && r[1] == '十' {
			return d * 10
		}
		if r[0] == '十' {
			if d, ok := chineseDigit[r[1]]; ok {
				return 10 + d
			}
		}
		return -1
	case 3:
		d1, ok1 := chineseDigit[r[0]]
		d3, ok3 := chineseDigit[r[2]]
		if ok1 && r[1] == '十' && ok3 {
			return d1*10 + d3
		}
	}
	return -1
}

// ordinalSequenceCoherent reports whether the ordinals form a strict
// ascending run starting at 1 (1,2,3... or 一,二,三...). Any -1 breaks it.
func ordinalSequenceCoherent(ords []int) bool {
	for i, v := range ords {
		if v != i+1 {
			return false
		}
	}
	return true
}

// observe records the task message once per run; subsequent calls are
// no-ops (the first user message defines the task).
func (c *constraintAuditState) observe(prompt string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.constraints != nil || c.fired {
		return
	}
	c.constraints = buildConstraints(prompt)
}

// checkAndInject returns the per-item audit prompt when a multi-item task
// is about to finish without having been audited. One-shot per run.
func (c *constraintAuditState) checkAndInject() string {
	if c == nil {
		return ""
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.fired || len(c.constraints) < constraintAuditMinItems {
		return ""
	}
	c.fired = true
	var b strings.Builder
	b.WriteString("[constraint-audit] The task listed ")
	b.WriteString(itoaPivot(len(c.constraints)))
	b.WriteString(" explicit requirements:\n")
	for i, item := range c.constraints {
		b.WriteString("  ")
		b.WriteString(itoaPivot(i + 1))
		b.WriteString(". ")
		b.WriteString(item)
		b.WriteString("\n")
	}
	b.WriteString("Before finishing, output one line per item above: `[done]` or `[not done]` with a word of evidence. " +
		"For every [not done] item, either complete it now or state concretely why it is out of scope. " +
		"Do not summarize around items you skip.")
	return b.String()
}
