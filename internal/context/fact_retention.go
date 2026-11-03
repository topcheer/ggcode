package context

import (
	"regexp"
	"strings"
	"unicode"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/provider"
)

var constraintMarkers = []string{
	"don't", "do not", "never", "must ", "must not", "always ", "forbidden",
	"不要", "必须", "别用", "别改", "不能", "禁止", "务必", "只能",
}

var filePathRe = regexp.MustCompile(`(?:^|[\s"'(` + "`" + `(=,:])(([\w\-./]+\.(?:go|md|ts|tsx|py|rs|java|js|jsx|yaml|yml|toml|json|mod|sql|sh)))`)

const (
	minPathOccurrences = 3
	maxConstraintLines = 8
	maxRetentionChars  = 800
)

func userConstraintSource(msgs []provider.Message) string {
	var b strings.Builder
	for _, msg := range msgs {
		if msg.Role != "user" {
			continue
		}
		for _, block := range msg.Content {
			if block.Type != "text" {
				continue
			}
			text := strings.TrimSpace(block.Text)
			if text == "" {
				continue
			}
			b.WriteString(text)
			b.WriteByte('\n')
		}
	}
	return b.String()
}

func extractConstraintLines(payload string) []string {
	var out []string
	seen := map[string]bool{}
	for _, raw := range strings.Split(payload, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || len(line) > 200 || seen[line] {
			continue
		}
		low := strings.ToLower(line)
		for _, m := range constraintMarkers {
			if strings.Contains(low, m) {
				seen[line] = true
				out = append(out, line)
				break
			}
		}
		if len(out) >= maxConstraintLines {
			break
		}
	}
	return out
}

func topRecurringPaths(payload string) []string {
	counts := map[string]int{}
	for _, m := range filePathRe.FindAllStringSubmatch(payload, -1) {
		p := m[1]
		if len(p) < 4 || strings.Count(p, ".") < 1 {
			continue
		}
		counts[p]++
	}
	type pc struct {
		path string
		n    int
	}
	var list []pc
	for p, n := range counts {
		if n >= minPathOccurrences {
			list = append(list, pc{p, n})
		}
	}
	for i := 1; i < len(list); i++ {
		for j := i; j > 0 && (list[j].n > list[j-1].n || (list[j].n == list[j-1].n && list[j].path < list[j-1].path)); j-- {
			list[j], list[j-1] = list[j-1], list[j]
		}
	}
	var out []string
	for _, e := range list {
		if len(out) >= 12 {
			break
		}
		out = append(out, e.path)
	}
	return out
}

func normForContain(s string) string {
	low := strings.ToLower(s)
	var b strings.Builder
	for _, r := range low {
		if unicode.IsSpace(r) {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func applyFactRetention(summary, payload, userSrc string) string {
	if summary == "" || payload == "" {
		return summary
	}
	summaryNorm := normForContain(summary)
	var facts []string
	for _, line := range extractConstraintLines(userSrc) {
		if !strings.Contains(summaryNorm, normForContain(line)) {
			facts = append(facts, line)
		}
	}
	for _, p := range topRecurringPaths(payload) {
		if !strings.Contains(summary, p) {
			facts = append(facts, "recurring file: "+p)
		}
	}
	if len(facts) == 0 {
		return summary
	}
	var b strings.Builder
	b.WriteString(summary)
	b.WriteString("\n\n## Auto-preserved Facts\nRe-attached deterministically from the pre-compaction transcript (the summary above dropped them; treat as binding):\n")
	appended := 0
	written := 0
	for _, f := range facts {
		if appended+len(f) > maxRetentionChars {
			break
		}
		b.WriteString("- ")
		b.WriteString(f)
		b.WriteString("\n")
		appended += len(f)
		written++
	}
	out := b.String()
	debug.Log("ctx", "fact retention: %d/%d candidate facts re-attached (%d chars appended)", written, len(facts), appended)
	return out
}
