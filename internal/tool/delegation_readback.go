package tool

import (
	"fmt"
	"strings"
	"unicode"
)

// r453: Delegation Read-Back Handshake.
//
// Production-agent three-party handoff research (A2A task lifecycle
// consensus states, read-back/hear-back protocols from aviation safety,
// builder-validator separation) shows the highest-leverage failure window
// in delegation is BEFORE the child starts: the parent writes acceptance
// criteria, the child reads them, and nothing verifies the child's
// UNDERSTANDING until the final result. r384 (delegation_acceptance.go)
// guards the back end - the parent validates the result against the
// author's criteria. This file guards the front end: the child must
// restate the criteria in its own words before starting, and the
// restatement is checked for per-criterion coverage deterministically.

const readBackMarker = "=== DoD READ-BACK ==="

// maxReadBackNudges mirrors the inline-tool-call nudge cap: the handshake
// instruction is appended at spawn time only (once per task), so this
// guards against a task text that already embeds the marker (re-spawn of
// a literally re-sent task).
const maxReadBackNudges = 1

// readBackNudge returns the instruction block appended to a spawn task
// when the task carries recognizable acceptance criteria. Empty string
// when the task has no criteria section (nothing to read back) or the
// marker is already present (idempotent re-spawn).
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

// readBackCoverage classifies one criterion against the read-back text.
type readBackCoverage int

const (
	coverageMiss readBackCoverage = iota
	coverageFuzzy
	coveragePass
)

const (
	// overlap thresholds on content-word sets: >=0.6 covered, >=0.3 fuzzy.
	readBackPassThreshold  = 0.6
	readBackFuzzyThreshold = 0.3
)

// readBackReport is the deterministic per-criterion comparison of the
// child's restatement against the author's original criteria.
type readBackReport struct {
	Criteria []string
	Verdicts []readBackCoverage
	HadBlock bool // child produced a recognizable read-back block at all
}

// compareReadBack extracts the criteria from the task, locates the
// read-back block in the child's response, and scores per-criterion
// keyword overlap. Pure lexical, zero LLM cost.
func compareReadBack(task, response string) readBackReport {
	rep := readBackReport{Criteria: extractAcceptanceCriteria(task)}
	if len(rep.Criteria) == 0 {
		return rep
	}
	block := extractReadBackBlock(response)
	if block == "" {
		return rep // no block: HadBlock=false, all verdicts stay Miss
	}
	rep.HadBlock = true
	blockWords := contentWordSet(block)
	for _, c := range rep.Criteria {
		words := contentWordSet(c)
		if len(words) == 0 {
			rep.Verdicts = append(rep.Verdicts, coveragePass) // nothing scoreable; don't nag
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

// extractReadBackBlock returns the text between the read-back marker and
// the next markdown heading or end of response, whichever comes first.
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

// readBackReminder builds the report block prepended to a completed
// delegation result (before the acceptance checkpoint). Empty when the
// task carries no criteria. When the child never produced a read-back
// block the report says so - the parent then knows the front-end half of
// the handshake was skipped and treats the builder's self-report with the
// corresponding caution.
func readBackReminder(task, result string) string {
	if result == "" || strings.Contains(result, "=== DoD READ-BACK REPORT") {
		return "" // already reported (re-poll)
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

// contentWordSet lowercases and tokenizes text into content words
// (length > 2, minus common English stop words). Punctuation splits
// tokens; English plurals are normalized to their stem (passes -> pass)
// so a restatement does not have to copy the author's exact inflection.
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
		if len(f) <= 2 || stop[f] {
			continue
		}
		words[f] = true
		// crude plural normalization: register both -s and -es stems so
		// "pass" matches "passes" and "file" matches "files".
		if stem := strings.TrimSuffix(f, "es"); len(stem) > 2 {
			words[stem] = true
		}
		if stem := strings.TrimSuffix(f, "s"); len(stem) > 2 {
			words[stem] = true
		}
	}
	return words
}
