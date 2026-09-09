package memory

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
)

// ContradictionCheck detects when a new memory entry semantically conflicts
// with an existing entry - same topic, different (incompatible) values.
//
// This is distinct from DuplicateCheck (which detects near-identical keys) and
// from staleness (which detects broken paths or ancient entries). Contradiction
// detection catches the insidious case where the agent writes "build command:
// go build ./..." today and "build command: go build -tags goolm ./..." next
// week, leaving two mutually incompatible memories in the store.
//
// Research basis: "Memory for Autonomous LLM Agents" (arXiv:2603.07670, 2026)
// emphasizes contradiction detection and source attribution as necessary
// quality gates: "one bad write can pollute the store for many steps
// downstream."
type ContradictionCheck struct {
	// Conflicts holds detected contradictions, each linking the new entry to
	// an existing entry with the conflicting subject and both values.
	Conflicts []ContradictionConflict
}

// ContradictionConflict represents one detected contradiction.
type ContradictionConflict struct {
	// ExistingKey is the key of the memory that conflicts with the new entry.
	ExistingKey string
	// Subject is the shared topic (e.g. "build command", "test framework").
	Subject string
	// NewValue is the value stated in the new memory.
	NewValue string
	// ExistingValue is the value stated in the existing memory.
	ExistingValue string
}

// HasConflict reports whether any contradictions were detected.
func (cc ContradictionCheck) HasConflict() bool {
	return len(cc.Conflicts) > 0
}

// maxContradictionConflicts caps the number of conflicts reported to avoid
// flooding the tool result with an excessive warning.
const maxContradictionConflicts = 3

// FormatContradictionWarning returns a human-readable warning for detected
// contradictions. Returns empty if no conflicts.
func (cc ContradictionCheck) FormatContradictionWarning(newKey string) string {
	if !cc.HasConflict() {
		return ""
	}
	var b strings.Builder
	b.WriteString("Warning: memory ")
	b.WriteString(newKey)
	b.WriteString(" contradicts existing memor")
	if len(cc.Conflicts) == 1 {
		b.WriteString("y")
	} else {
		b.WriteString(fmt.Sprintf("%d conflicts", len(cc.Conflicts)))
	}
	b.WriteString(":\n")
	shown := cc.Conflicts
	if len(shown) > maxContradictionConflicts {
		shown = shown[:maxContradictionConflicts]
	}
	for _, c := range shown {
		b.WriteString(fmt.Sprintf("  - [%s] %q vs existing %q in %q\n",
			truncate(c.Subject, 40), truncate(c.NewValue, 50), truncate(c.ExistingValue, 50), c.ExistingKey))
	}
	b.WriteString("Resolve the conflict: update the older entry, delete the stale one, or use a distinct key if both are valid in different contexts.")
	return b.String()
}

// CheckContradiction inspects existing memories for semantic conflicts with a
// new entry being saved. A conflict is detected when the same "subject" (e.g.
// "build command") is assigned different values in two memories.
//
// The detection is deterministic and zero-LLM-cost: it extracts structured
// claims from free text using heuristics, then compares values for matching
// subjects. This avoids false positives from unrelated content while catching
// the common case of value drift across sessions.
func (am *AutoMemory) CheckContradiction(key, content string) ContradictionCheck {
	metas, err := am.collectMetas()
	if err != nil {
		debug.Log("memory", "contradiction check: failed to read dir %s: %v", am.dir, err)
		return ContradictionCheck{}
	}

	newClaims := extractClaims(content)
	if len(newClaims) == 0 {
		return ContradictionCheck{}
	}

	var cc ContradictionCheck

	for _, m := range metas {
		// Skip comparing against the same key (self-update case). m.Key is
		// the DISK filename (disambiguateKey output), so compare against the
		// same derivation - the old sanitizeKey(key) never matched keys that
		// got a hash suffix (CJK/spaces/dots), making every self-update of
		// such keys warn "contradicts itself" (#1280).
		if m.Key == disambiguateKey(key, sanitizeKey(key)) {
			continue
		}

		path := filepath.Join(am.dir, m.Key+".md")
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		existingClaims := extractClaims(string(data))
		if len(existingClaims) == 0 {
			continue
		}

		for subject, newVal := range newClaims {
			existingVal, ok := existingClaims[subject]
			if !ok {
				continue
			}
			// Same subject. Check if values conflict.
			if claimsConflict(newVal, existingVal) {
				cc.Conflicts = append(cc.Conflicts, ContradictionConflict{
					ExistingKey:   m.Key,
					Subject:       subject,
					NewValue:      newVal,
					ExistingValue: existingVal,
				})
			}
		}
	}

	return cc
}

// claim is a structured extraction: a (subject, value) pair derived from a
// directive or assignment statement in memory content.
type claimMap map[string]string // subject -> value

// claimPatterns extract (subject, value) pairs from memory text.
// These patterns target the common formats agents use to record settings,
// commands, and constraints.
var claimPatterns = []*regexp.Regexp{
	// "key: value" or "key = value" (colon/equals assignment)
	// e.g. "build command: go build ./..." or "framework = gin"
	regexp.MustCompile(`(?im)^\s*\*?\s*([a-z][a-z0-9 _-]{1,40}?)\s*[:=]\s*(.+?)\s*$`),
	// "use X" / "always use X" — directive form
	// Captures X as the value with subject "use directive"
	// Handled separately below for polarity conflicts.
}

// polarityPatterns detect opposite-polarity directives for the same target.
var (
	polarityDo   = regexp.MustCompile(`(?i)\b(?:always\s+)?use\s+(\S[^\n.]{1,60}?)\s*(?:[.\n]|$)`)
	polarityDont = regexp.MustCompile(`(?i)\b(?:never|don't|do not|avoid)\s+(?:use\s+)?(\S[^\n.]{1,60}?)\s*(?:[.\n]|$)`)
)

// extractClaims parses memory content into a map of subject->value claims.
// It focuses on structured directives that are likely to carry conflicting
// values across sessions.
func extractClaims(content string) claimMap {
	claims := make(claimMap)

	for _, pat := range claimPatterns {
		matches := pat.FindAllStringSubmatch(content, -1)
		for _, m := range matches {
			subject := normalizeSubject(m[1])
			value := strings.TrimSpace(m[2])
			if subject == "" || value == "" || isNoiseSubject(subject) {
				continue
			}
			// Keep the first occurrence for each subject (dominant claim).
			if _, exists := claims[subject]; !exists {
				claims[subject] = value
			}
		}
	}
	// Check negation directives FIRST so they take priority over the
	// broader polarityDo pattern (which would also match "use" inside
	// "never use").
	extractPolarityClaim(claims, polarityDont, content, true)
	extractPolarityClaim(claims, polarityDo, content, false)

	return claims
}

// extractPolarityClaim scans content with the given polarity regex and sets
// claims["use"] if a directive is found and "use" is not already claimed.
// When negate is true, the value is prefixed with "~" to mark it as a
// prohibition.
func extractPolarityClaim(claims claimMap, re *regexp.Regexp, content string, negate bool) {
	for _, m := range re.FindAllStringSubmatch(content, -1) {
		val := strings.TrimSpace(m[1])
		if val == "" || isNoiseSubject(val) {
			continue
		}
		if negate {
			val = "~" + val
		}
		if _, ok := claims["use"]; !ok {
			claims["use"] = val
		}
	}
}

// normalizeSubject converts a raw subject string into a canonical key for
// comparison: lowercase, trimmed, extra whitespace collapsed.
func normalizeSubject(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	// Collapse internal whitespace.
	for strings.Contains(s, "  ") {
		s = strings.ReplaceAll(s, "  ", " ")
	}
	return s
}

// noiseSubjects are subjects that appear in "key: value" format but are not
// meaningful constraints (e.g. markdown headers, section labels).
var noiseSubjects = map[string]bool{
	"":            true,
	"note":        true,
	"notes":       true,
	"warning":     true,
	"example":     true,
	"examples":    true,
	"todo":        true,
	"fixme":       true,
	"description": true,
	"summary":     true,
	"details":     true,
	"reference":   true,
	"references":  true,
	"source":      true,
	"sources":     true,
	"see":         true,
	"link":        true,
	"url":         true,
	"step":        true,
	"steps":       true,
	"file":        true,
	"files":       true,
	"path":        true,
	"paths":       true,
	"type":        true,
	"tags":        true,
	"tag":         true,
	"status":      true,
	"reason":      true,
	"reasons":     true,
	"problem":     true,
	"solution":    true,
	"lesson":      true,
	"lessons":     true,
	"key":         true,
	"value":       true,
	"content":     true,
}

func isNoiseSubject(s string) bool {
	return noiseSubjects[s]
}

// claimsConflict determines whether two values for the same subject are
// genuinely incompatible (a contradiction) rather than merely different
// phrasings of the same fact.
func claimsConflict(a, b string) bool {
	a = strings.TrimSpace(a)
	b = strings.TrimSpace(b)
	if a == "" || b == "" {
		return false
	}
	// Identical values are not a conflict.
	if strings.EqualFold(a, b) {
		return false
	}

	// Polarity conflict: one says "use X", other says "~X" (don't use X).
	aNeg := strings.HasPrefix(a, "~")
	bNeg := strings.HasPrefix(b, "~")
	if aNeg != bNeg {
		aBase := strings.TrimPrefix(a, "~")
		bBase := strings.TrimPrefix(b, "~")
		// #1777 case 2: an affirmative and a negated claim only contradict
		// when they target NEARLY the same thing ("use git" vs "~use git"),
		// not when their domains merely overlap ("use git" vs "never use
		// git rebase" is fully compatible). The old 0.3 overlap gate - fine
		// for the two-affirmative band below - was far too loose here and
		// deleted correct memories; polarity flips need the same near-
		// equivalence as that band's ceiling.
		if tokenOverlap(aBase, bBase) >= 0.85 {
			return true
		}
		// #1878: a mixed-polarity pair that is NOT near-equivalent is
		// compatible by design ("use git" vs "~use git rebase") - it must
		// not fall through to the two-affirmative band below. Without this
		// short-circuit the 0.85 threshold change had zero behavioral
		// effect: tokenize drops '~', so the band below saw the same
		// jaccard (2/3 ∈ [0.3, 0.85)) and still returned true.
		return false
	}

	// Negation keywords within values: "required" vs "optional", "enabled"
	// vs "disabled", "true" vs "false".
	if isOppositeValue(a, b) {
		return true
	}

	// Version / numeric value conflict: extract numbers/versions and compare.
	if numericConflict(a, b) {
		return true
	}

	// For assignment-style claims (subject like "build command"), different
	// values likely indicate a real conflict if they share enough tokens to
	// be about the same thing but diverge on specifics.
	// Require moderate overlap (same domain) but not near-identity (that's
	// a duplicate, handled elsewhere).
	overlap := tokenOverlap(a, b)
	if overlap >= 0.3 && overlap < 0.85 {
		return true
	}

	// Single-token values for the same subject are inherently conflicting
	// if they differ (e.g. "framework: gin" vs "framework: echo").
	if isSingleToken(a) && isSingleToken(b) {
		return true
	}

	return false
}

// isSingleToken reports whether a value consists of a single alphanumeric
// token (no spaces, no path separators), indicating a concrete setting like
// "gin", "echo", or "postgres".
func isSingleToken(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" || len(s) > 30 {
		return false
	}
	for _, r := range s {
		if r == ' ' || r == '/' || r == '\\' {
			return false
		}
	}
	return true
}

// oppositePairs are value pairs that are semantically contradictory.
var oppositePairs = [][2]string{
	{"required", "optional"},
	{"enabled", "disabled"},
	{"true", "false"},
	{"yes", "no"},
	{"on", "off"},
	{"always", "never"},
	{"public", "private"},
	{"internal", "external"},
	{"static", "dynamic"},
}

func isOppositeValue(a, b string) bool {
	aLow := strings.ToLower(a)
	bLow := strings.ToLower(b)
	for _, pair := range oppositePairs {
		if (aLow == pair[0] && bLow == pair[1]) || (aLow == pair[1] && bLow == pair[0]) {
			return true
		}
	}
	return false
}

// versionNumRe extracts version-like numeric tokens from a value.
// #1773 case 1: dotted numbers only (1.27, 2.0.1). Bare integers are
// usually non-version context - "go 1.27 (issue 4532)" vs "go 1.27
// (fixed in 4533)" used to compare {1.27,4532} vs {1.27,4533}, mutual
// absence flagged a conflict between two statements whose CORE version
// is identical. Bare-number drift ("3 retries" vs "5 retries") is a
// deliberate miss: without context those are just counts.
var versionNumRe = regexp.MustCompile(`\d+\.\d+(?:\.\d+)*`)

// numericConflict detects when two values contain different version numbers or
// numeric specifications, suggesting a version drift.
func numericConflict(a, b string) bool {
	aNums := versionNumRe.FindAllString(a, -1)
	bNums := versionNumRe.FindAllString(b, -1)
	if len(aNums) == 0 || len(bNums) == 0 {
		return false
	}
	// If both have version-like numbers and they differ, flag conflict.
	// #1592-C: the old cross-product loop flagged conflict when ANY pair
	// differed - order-permuted or superset sets of the SAME facts
	// ("go 1.27 with node 20" vs "node 20 paired with go 1.27", "8080
	// 8443" vs "8080") read as contradictions, and the injected warning
	// told the agent to delete the CORRECT memory. Compare as SETS:
	// conflicting only when each side holds a number the other lacks
	// (subset/superset/equal all pass).
	amissing := false
	for _, an := range aNums {
		found := false
		for _, bn := range bNums {
			if an == bn {
				found = true
				break
			}
		}
		if !found {
			amissing = true
			break
		}
	}
	bmissing := false
	for _, bn := range bNums {
		found := false
		for _, an := range aNums {
			if bn == an {
				found = true
				break
			}
		}
		if !found {
			bmissing = true
			break
		}
	}
	return amissing && bmissing
}

// tokenOverlap computes the Jaccard similarity between the token sets of two
// strings. Reuses tokenize from duplicate_check.go.
func tokenOverlap(a, b string) float64 {
	return jaccardSimilarity(tokenize(a), tokenize(b))
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-3] + "..."
}
