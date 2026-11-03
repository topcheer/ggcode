package agent

import (
	"regexp"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"
)

type constraintAuditState struct {
	mu          sync.Mutex
	constraints []string
	fired       bool
}

const (
	constraintAuditMinItems = 2
	constraintAuditMaxItems = 12
)

func newConstraintAuditState() *constraintAuditState {
	return &constraintAuditState{}
}

func (c *constraintAuditState) reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.constraints = nil
	c.fired = false
}

var (
	constraintNumberedRe = regexp.MustCompile(`^\s*(步骤\s*\d{1,3}[、.．:：]|[一二三四五六七八九十]{1,3}[、.．:：]|\d{1,3}[.)、]|[(（]\d{1,3}[)）])[\s　]*(.{3,200})$`)
	constraintBulletRe   = regexp.MustCompile(`^\s*[-*•]\s+(.{3,200})$`)

	proseClauseSplitRe        = regexp.MustCompile(`[，,、]|\s+(?:and\s+then|and|then)\s+|并且|以及|，然后|然后`)
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
	proseFillerPrefixes = []string{"请", "麻烦", "please"}
)

const (
	proseConstraintMinClauses = 2
	proseConstraintMaxClauses = 8
	proseClauseMinRunes       = 4
	proseClauseMaxRunes       = 200
)

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

	items := numbered
	if len(bullets) > len(numbered) {
		items = bullets
	} else if len(numbered) > 0 && !ordinalSequenceCoherent(numberedOrdinals) {

		items = nil
	}
	if len(items) >= constraintAuditMinItems && len(items) <= constraintAuditMaxItems {
		return items
	}
	return extractProseConstraints(prose.String())
}

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

func ordinalSequenceCoherent(ords []int) bool {
	for i, v := range ords {
		if v != i+1 {
			return false
		}
	}
	return true
}

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

func proseClauseIsConstraint(cl string) bool {
	for _, p := range zhProseConstraintPrefixes {
		if strings.HasPrefix(cl, p) {
			return true
		}
	}
	return enProseConstraintStartRe.MatchString(cl)
}

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
