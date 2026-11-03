package tool

import (
	"fmt"
	"strings"
	"unicode"
)

const readBackMarker = "=== DoD READ-BACK ==="

func stripReadBackNudge(task string) string {
	if clean, _, ok := strings.Cut(task, readBackMarker); ok {

		return strings.TrimRight(clean, " \t\n")
	}
	return task
}

const maxReadBackNudges = 1

func readBackNudge(task string) string {
	if task == "" || strings.Count(task, readBackMarker) >= maxReadBackNudges {
		return ""
	}
	items := extractAcceptanceCriteria(task)
	if len(items) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\n")
	b.WriteString(readBackMarker)
	b.WriteString("\nMANDATORY FIRST STEP - read-back handshake: before doing ANY work, your first response must restate the acceptance criteria above in your own words, one line per criterion, as:\n")
	for i := range items {
		fmt.Fprintf(&b, "%d. <your restatement + how you will verify it>\n", i+1)
	}
	b.WriteString("If any criterion is ambiguous or unachievable, say so in the restatement instead of guessing. Then proceed with the task.")
	return b.String()
}

type readBackCoverage int

const (
	coverageMiss readBackCoverage = iota
	coverageFuzzy
	coveragePass
)

const (
	readBackPassThreshold  = 0.6
	readBackFuzzyThreshold = 0.3
)

type readBackReport struct {
	Criteria []string
	Verdicts []readBackCoverage
	HadBlock bool
}

func compareReadBack(task, response string) readBackReport {
	rep := readBackReport{Criteria: extractAcceptanceCriteria(task)}
	if len(rep.Criteria) == 0 {
		return rep
	}
	block := extractReadBackBlock(response)
	if block == "" {
		return rep
	}
	rep.HadBlock = true
	blockWords := contentWordSet(block)
	for _, c := range rep.Criteria {
		words := contentWordSet(c)
		if len(words) == 0 {
			rep.Verdicts = append(rep.Verdicts, coveragePass)
			continue
		}
		hit := 0
		for w := range words {
			if blockWords[w] {
				hit++
			}
		}
		overlap := float64(hit) / float64(len(words))
		switch {
		case overlap >= readBackPassThreshold:
			rep.Verdicts = append(rep.Verdicts, coveragePass)
		case overlap >= readBackFuzzyThreshold:
			rep.Verdicts = append(rep.Verdicts, coverageFuzzy)
		default:
			rep.Verdicts = append(rep.Verdicts, coverageMiss)
		}
	}
	return rep
}

func extractReadBackBlock(response string) string {
	idx := strings.Index(response, readBackMarker)
	if idx < 0 {
		return ""
	}
	rest := response[idx+len(readBackMarker):]
	if end := strings.Index(rest, "\n#"); end >= 0 {
		rest = rest[:end]
	}
	return rest
}

func readBackReminder(task, result string) string {
	if result == "" || strings.Contains(result, "=== DoD READ-BACK REPORT") {
		return ""
	}
	rep := compareReadBack(task, result)
	if len(rep.Criteria) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("=== DoD READ-BACK REPORT (front-end handshake) ===\n")
	if !rep.HadBlock {
		b.WriteString("The sub-agent never restated the acceptance criteria (no read-back block). Its understanding of the task contract was NOT verified before it started.\n")
	} else {
		pass, fuzzy, miss := 0, 0, 0
		for _, v := range rep.Verdicts {
			switch v {
			case coveragePass:
				pass++
			case coverageFuzzy:
				fuzzy++
			case coverageMiss:
				miss++
			}
		}
		fmt.Fprintf(&b, "Read-back handshake coverage: %d/%d criteria restated, %d fuzzy, %d missing.\n", pass, len(rep.Criteria), fuzzy, miss)
		for i, v := range rep.Verdicts {
			if v == coveragePass {
				continue
			}
			label := "FUZZY"
			if v == coverageMiss {
				label = "MISSING"
			}
			fmt.Fprintf(&b, "  criterion %d %s: %s\n", i+1, label, rep.Criteria[i])
		}
		if miss > 0 {
			b.WriteString("Criteria the sub-agent never acknowledged - verify those extra carefully in the acceptance checkpoint below.\n")
		}
	}
	return b.String()
}

func contentWordSet(s string) map[string]bool {
	stop := map[string]bool{
		"the": true, "and": true, "for": true, "are": true, "but": true,
		"not": true, "you": true, "with": true, "this": true, "that": true,
		"from": true, "have": true, "must": true, "your": true, "will": true,
		"when": true, "then": true, "into": true, "any": true, "all": true,
		"its": true, "was": true, "has": true, "how": true, "per": true,
	}
	words := map[string]bool{}
	for _, f := range strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}) {

		if hasCJKRun(f) {
			addCJKBigrams(words, f)
			continue
		}
		if len(f) <= 2 || stop[f] {
			continue
		}
		words[f] = true

		if stem := strings.TrimSuffix(f, "es"); len(stem) > 2 {
			words[stem] = true
		}
		if stem := strings.TrimSuffix(f, "s"); len(stem) > 2 {
			words[stem] = true
		}
	}
	return words
}

func hasCJKRun(tok string) bool {
	for _, r := range tok {
		if unicode.Is(unicode.Han, r) || unicode.Is(unicode.Hiragana, r) ||
			unicode.Is(unicode.Katakana, r) || unicode.Is(unicode.Hangul, r) {
			return true
		}
	}
	return false
}

func addCJKBigrams(words map[string]bool, tok string) {
	var run []rune
	flush := func() {
		for _, r := range run {
			words[string(r)] = true
		}
		for i := 0; i+1 < len(run); i++ {
			words[string(run[i:i+2])] = true
		}
		run = run[:0]
	}
	isCJK := func(r rune) bool {
		return unicode.Is(unicode.Han, r) || unicode.Is(unicode.Hiragana, r) ||
			unicode.Is(unicode.Katakana, r) || unicode.Is(unicode.Hangul, r)
	}
	for _, r := range tok {
		if isCJK(r) {
			run = append(run, r)
			continue
		}
		flush()
	}
	flush()
}
