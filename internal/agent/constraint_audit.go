package agent

import (
	"regexp"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"
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
// This audit covers structurally-listed requirements (numbered or
// bulleted lines) - those decompose deterministically with zero
// extraction risk - and, since the DeCRIM result (arXiv 2410.06458:
// decomposing multi-constraint instructions into individually
// checkable units cuts silent drops by 7-8%), a conservative prose
// extractor for list-free multi-request messages: prose qualifies
// ONLY with >=2 verb-initial constraint clauses (imperative or
// 必须/不要-class modality). Narrative sentences almost never start
// with a bare verb, which is the false-positive control; interrogative
// sentences are dropped whole; fewer than 2 qualifying clauses keeps
// the message with the aggregate fulfillment gate (prefer missing the
// audit over auditing narrative). The injected prompt forces a
// per-item binary verdict ([done]/[not done]) - the cheap structured
// audit AREX shows is the right control signal - and only unmet items
// need follow-up work.
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

	// --- DeCRIM-style prose constraint extraction (arXiv 2410.06458) ---
	// Clause separators inside a sentence: Chinese comma/顿号 and English
	// coordinating conjunctions. Sentence terminators are handled by
	// splitProseSentences so interrogatives can be dropped whole.
	proseClauseSplitRe = regexp.MustCompile(`[，,、]|\s+(?:and\s+then|and|then)\s+|并且|以及|，然后|然后`)
	// Constraint-signal prefixes: a clause qualifies only when it STARTS
	// with an imperative/requirement verb (zh) or one of these en forms.
	// Bare "测试" is deliberately absent: sentence-initial 测试 is more
	// often narrative ("测试失败了") than imperative.
	zhProseConstraintPrefixes = []string{
		"支持", "新增", "增加", "添加", "修复", "编写", "补充", "包含",
		"确保", "保证", "使用", "采用", "处理", "实现", "必须", "不要",
		"禁止", "不能", "不可", "去掉", "移除", "删除", "更新", "重构",
		"还要", "还需", "还得", "别", "测试用", "测试要",
	}
	enProseConstraintStartRe = regexp.MustCompile(
		`(?i)^(add|support|fix|write|include|ensure|make|use|handle|implement|` +
			`update|remove|create|keep|refactor|test|don't|dont|do not|` +
			`must|should|shall|need to|needs to)\b`)
	// Polite fillers stripped before the prefix test.
	proseFillerPrefixes = []string{"请", "麻烦", "please"}
)

const (
	// proseConstraintMinClauses: a single qualifying clause carries no
	// list semantics (the fulfillment gate covers single asks).
	proseConstraintMinClauses = 2
	// proseConstraintMaxClauses caps extraction; beyond this the message
	// reads as narrative/notes, not a compact multi-request.
	proseConstraintMaxClauses = 8
	// Rune-length window for a qualifying clause: shorter fragments are
	// conversational ("别急"), longer ones are pasted content.
	proseClauseMinRunes = 4
	proseClauseMaxRunes = 200
)

// buildConstraints extracts requirement items from a task message.
// Structural lists (>=2 items) qualify directly; list-free prose falls
// through to extractProseConstraints, which requires >=2 verb-initial
// constraint clauses before the per-item audit activates. Fenced/indented
// code is skipped so pasted logs do not masquerade as requirements.
func buildConstraints(prompt string) []string {
	if prompt == "" || isReadOnlyTask(prompt) {
		return nil
	}
	inCode := false
	var numbered, bullets []string
	var numberedOrdinals []int
	var prose strings.Builder
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
			continue
		}
		prose.WriteString(trimmed)
		prose.WriteByte('\n')
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
		// ordinals. Incoherent => not a task list; fall through to the
		// prose extractor, whose verb-initial gate still screens such
		// narrative out.
		items = nil
	}
	if len(items) >= constraintAuditMinItems && len(items) <= constraintAuditMaxItems {
		return items
	}
	return extractProseConstraints(prose.String())
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

// splitProseSentences splits prose into sentences on terminators
// (。．！？!?；; and newline), dropping interrogative segments whole:
// a question is never a requirement. A ? both terminates its sentence
// and marks that sentence interrogative; the flag resets afterwards.
func splitProseSentences(s string) []string {
	var out []string
	start := 0
	interrogative := false
	for i, r := range s {
		switch r {
		case '。', '．', '！', '!', '；', ';', '\n', '？', '?', '.':
			seg := strings.TrimSpace(s[start:i])
			if seg != "" && !interrogative {
				out = append(out, seg)
			}
			start = i + utf8.RuneLen(r)
			interrogative = r == '？' || r == '?'
		}
	}
	if start < len(s) {
		seg := strings.TrimSpace(s[start:])
		if seg != "" && !interrogative {
			out = append(out, seg)
		}
	}
	return out
}

// extractProseConstraints pulls individually-checkable requirement units
// from list-free prose. A clause qualifies ONLY when it starts with a
// constraint verb (imperative or 必须/不要-class modality) after polite
// fillers are stripped; narrative sentences almost never start with a
// bare verb, which is the false-positive control. Activation requires
// >=2 qualifying clauses within [4,200] runes; anything else returns nil
// (the aggregate fulfillment gate remains the fallback).
func extractProseConstraints(nonCodeText string) []string {
	if strings.TrimSpace(nonCodeText) == "" {
		return nil
	}
	var clauses []string
	for _, sent := range splitProseSentences(nonCodeText) {
		for _, cl := range proseClauseSplitRe.Split(sent, -1) {
			cl = strings.TrimSpace(cl)
			for _, f := range proseFillerPrefixes {
				cl = strings.TrimPrefix(cl, f)
			}
			cl = strings.TrimSpace(cl)
			if n := len([]rune(cl)); n < proseClauseMinRunes || n > proseClauseMaxRunes {
				continue
			}
			if proseClauseIsConstraint(cl) {
				clauses = append(clauses, cl)
			}
		}
	}
	if len(clauses) < proseConstraintMinClauses || len(clauses) > proseConstraintMaxClauses {
		return nil
	}
	// Dedup (case-insensitive for en).
	seen := map[string]bool{}
	var out []string
	for _, cl := range clauses {
		key := strings.ToLower(cl)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, cl)
	}
	return out
}

// proseClauseIsConstraint reports whether a clause starts with a
// constraint signal (zh imperative/modality prefix or en imperative /
// deontic verb).
func proseClauseIsConstraint(cl string) bool {
	for _, p := range zhProseConstraintPrefixes {
		if strings.HasPrefix(cl, p) {
			return true
		}
	}
	return enProseConstraintStartRe.MatchString(cl)
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
