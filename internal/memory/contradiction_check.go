package memory

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
)

type ContradictionCheck struct {
	Conflicts []ContradictionConflict
}

type ContradictionConflict struct {
	ExistingKey string

	Subject string

	NewValue string

	ExistingValue string
}

func (cc ContradictionCheck) HasConflict() bool {
	return len(cc.Conflicts) > 0
}

const maxContradictionConflicts = 3

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

type claimMap map[string]string

var claimPatterns = []*regexp.Regexp{

	regexp.MustCompile(`(?im)^\s*\*?\s*([a-z][a-z0-9 _-]{1,40}?)\s*[:=]\s*(.+?)\s*$`),
}

var (
	polarityDo   = regexp.MustCompile(`(?i)\b(?:always\s+)?use\s+(\S[^\n.]{1,60}?)\s*(?:[.\n]|$)`)
	polarityDont = regexp.MustCompile(`(?i)\b(?:never|don't|do not|avoid)\s+(?:use\s+)?(\S[^\n.]{1,60}?)\s*(?:[.\n]|$)`)
)

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

			if _, exists := claims[subject]; !exists {
				claims[subject] = value
			}
		}
	}

	extractPolarityClaim(claims, polarityDont, content, true)
	extractPolarityClaim(claims, polarityDo, content, false)

	return claims
}

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

func normalizeSubject(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))

	for strings.Contains(s, "  ") {
		s = strings.ReplaceAll(s, "  ", " ")
	}
	return s
}

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

func claimsConflict(a, b string) bool {
	a = strings.TrimSpace(a)
	b = strings.TrimSpace(b)
	if a == "" || b == "" {
		return false
	}

	if strings.EqualFold(a, b) {
		return false
	}

	aNeg := strings.HasPrefix(a, "~")
	bNeg := strings.HasPrefix(b, "~")
	if aNeg != bNeg {
		aBase := strings.TrimPrefix(a, "~")
		bBase := strings.TrimPrefix(b, "~")

		if tokenOverlap(aBase, bBase) >= 0.85 {
			return true
		}

		return false
	}

	if isOppositeValue(a, b) {
		return true
	}

	if numericConflict(a, b) {
		return true
	}

	overlap := tokenOverlap(a, b)

	if plusListSubset(a, b) {
		return false
	}
	if overlap >= 0.3 && overlap < 0.85 {
		return true
	}

	if isSingleToken(a) && isSingleToken(b) {
		return true
	}

	return false
}

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

var versionNumRe = regexp.MustCompile(`\d+\.\d+(?:\.\d+)*`)

func numericConflict(a, b string) bool {
	aNums := versionNumRe.FindAllString(a, -1)
	bNums := versionNumRe.FindAllString(b, -1)
	if len(aNums) == 0 || len(bNums) == 0 {
		return false
	}

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

func tokenOverlap(a, b string) float64 {
	return jaccardSimilarity(tokenize(a), tokenize(b))
}

func plusListSubset(a, b string) bool {
	ia, ib := strings.Split(a, "+"), strings.Split(b, "+")
	if len(ia) == len(ib) {
		return false
	}
	small, large := ia, ib
	if len(ia) > len(ib) {
		small, large = ib, ia
	}
	seen := make(map[string]bool, len(large))
	for _, item := range large {
		seen[strings.TrimSpace(strings.ToLower(item))] = true
	}
	for _, item := range small {
		if !seen[strings.TrimSpace(strings.ToLower(item))] {
			return false
		}
	}
	return true
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max-3]) + "..."
}
