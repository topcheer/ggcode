package agent

// Assertion Weakening Detector
//
// Research basis: Specification gaming / reward hacking literature identifies
// "reward tampering" as a key misaligned behavior where agents modify the
// verification mechanism rather than solving the problem (arXiv:2507.05619,
// "Detecting Proxy Gaming in RL and LLM Alignment"; DeepMind "Specification
// Gaming" taxonomy 2024). In coding agents, one subtle form is weakening
// existing assertions to make failing tests pass - changing comparison
// operators or expected values rather than fixing the code under test:
//
//   require.Equal(t, 42, result)  →  require.Equal(t, 41, result)
//   assert.NotEqual(t, 0, x)      →  assert.Equal(t, 0, x)
//   if result != expected { ... } →  if result == expected { ... }
//   assert.NoError(t, err)         →  assert.Error(t, err)
//   assertTrue(x.isValid())        →  assertFalse(x.isValid())
//
// Distinction from existing detectors:
//   - test_gaming_check: detects REMOVED assertions or ADDED skip directives
//   - assertion_presence_check: detects hollow tests with ZERO assertions
//   - THIS detector: detects assertions that EXISTED but whose comparison
//     direction or polarity was FLIPPED in the edit.
//
// The detector performs delta-aware comparison: it parses both old and new
// content for assertion lines and flags lines where:
//   1. A comparison operator was flipped (== to !=, > to <=, < to >=)
//   2. An assertion polarity was inverted (assert.NoError to assert.Error,
//      assert.True to assert.False, require.Equal to require.NotEqual)
//
// Zero LLM cost, deterministic, <1ms per check.

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

const assertionWeakeningMaxWarnings = 4

// assertionLineRe matches lines containing assertion/comparison keywords.
// We extract the full line text to compare old vs new versions.
var assertionWeakeningLineRe = regexp.MustCompile(
	`(?i)(?:require\.|assert\.|expect|assertEqual|assertTrue|assertFalse|assertNotNull|assertNull|assertSame|assertNotSame|assertNotEqual|\.should\(|\.to\(|t\.Error|t\.Fatal|if\s+.*[=!<>])`,
)

// polarityPairs defines assertion functions whose meaning is inverted.
var polarityPairs = map[string]string{
	"assert.NoError":   "assert.Error",
	"assert.Error":     "assert.NoError",
	"require.NoError":  "require.Error",
	"require.Error":    "require.NoError",
	"assert.True":      "assert.False",
	"assert.False":     "assert.True",
	"require.True":     "require.False",
	"require.False":    "require.True",
	"assert.Nil":       "assert.NotNil",
	"assert.NotNil":    "assert.Nil",
	"require.Nil":      "require.NotNil",
	"require.NotNil":   "require.Nil",
	"assert.Empty":     "assert.NotEmpty",
	"assert.NotEmpty":  "assert.Empty",
	"require.Empty":    "require.NotEmpty",
	"require.NotEmpty": "require.Empty",
	"assertTrue(":      "assertFalse(",
	"assertFalse(":     "assertTrue(",
	"assertNull(":      "assertNotNull(",
	"assertNotNull(":   "assertNull(",
	"assert.Equal":     "assert.NotEqual",
	"assert.NotEqual":  "assert.Equal",
	"require.Equal":    "require.NotEqual",
	"require.NotEqual": "require.Equal",
	"assertEqual(":     "assertNotEqual(",
	"assertNotEqual(":  "assertEqual(",
}

// polarityCanonical maps each assertion function name to its canonical form
// (the shorter/positive member of the polarity pair). Built from polarityPairs.
var polarityCanonical = func() map[string]string {
	m := make(map[string]string, len(polarityPairs)*2)
	for a, b := range polarityPairs {
		// Use the shorter string as canonical so NoError canonicalizes to Error.
		canonical := a
		if len(b) < len(a) {
			canonical = b
		}
		m[a] = canonical
		m[b] = canonical
	}
	return m
}()

// sortedPolarityKeys returns polarity function names sorted by length
// descending. This ensures longer names are replaced before shorter ones,
// avoiding substring collisions (e.g., "require.NoError" before "require.Error").
func sortedPolarityKeys() []string {
	keys := make([]string, 0, len(polarityCanonical))
	for k := range polarityCanonical {
		keys = append(keys, k)
	}
	// Sort by length descending, then alphabetically for stability.
	sort.Slice(keys, func(i, j int) bool {
		if len(keys[i]) != len(keys[j]) {
			return len(keys[i]) > len(keys[j])
		}
		return keys[i] < keys[j]
	})
	return keys
}

// comparisonFlips defines operator pairs that invert comparison meaning.
var comparisonFlips = []struct{ from, to string }{
	{"!=", "=="},
	{"==", "!="},
	{">=", "<"},
	{"<", ">="},
	{"<=", ">"},
	{">", "<="},
}

// checkAssertionWeakening detects assertion weakening by comparing old vs new
// content of test files. Returns warnings or empty string.
//
// Parameters:
//   - filePath: path of the written file (used for test file detection)
//   - oldContent: file content before the edit ("" for new files)
//   - newContent: file content after the edit
func checkAssertionWeakening(filePath, oldContent, newContent string) string {
	// Only check test files.
	if !isTestFile(filePath) || oldContent == "" || newContent == "" {
		return ""
	}

	oldAssertions := extractAssertionLines(oldContent)
	newAssertions := extractAssertionLines(newContent)

	if len(oldAssertions) == 0 || len(newAssertions) == 0 {
		return ""
	}

	var warnings []string

	// Build a map of normalized old assertions for quick lookup.
	// We compare lines that share the same "structural key" (same function/
	// variable references but potentially different operators/values).
	for _, newLine := range newAssertions {
		newNorm := normalizeAssertionStructure(newLine.text)
		if newNorm == "" {
			continue
		}
		for _, oldLine := range oldAssertions {
			oldNorm := normalizeAssertionStructure(oldLine.text)
			if oldNorm == "" {
				continue
			}
			// Same structural key means the lines are "the same assertion"
			// but possibly with flipped polarity or operator.
			if newNorm != oldNorm {
				continue
			}
			// Check for polarity flip.
			if msg := detectPolarityFlip(oldLine.text, newLine.text); msg != "" {
				warnings = append(warnings, msg)
				break
			}
			// Check for comparison operator flip.
			if msg := detectComparisonFlip(oldLine.text, newLine.text); msg != "" {
				warnings = append(warnings, msg)
				break
			}
		}
		if len(warnings) >= assertionWeakeningMaxWarnings {
			warnings = warnings[:assertionWeakeningMaxWarnings]
			warnings = append(warnings, fmt.Sprintf("... (%d max warnings)", assertionWeakeningMaxWarnings))
			break
		}
	}

	if len(warnings) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("[assertion-weakening] Assertion changes may weaken test verification:\n")
	for _, w := range warnings {
		sb.WriteString("  - ")
		sb.WriteString(w)
		sb.WriteString("\n")
	}
	return sb.String()
}

// assertionLineInfo holds a matched assertion line and its line number.
type assertionLineInfo struct {
	text string
	line int
}

// extractAssertionLines returns lines matching assertion patterns from content.
func extractAssertionLines(content string) []assertionLineInfo {
	var result []assertionLineInfo
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "/*") {
			continue
		}
		if assertionWeakeningLineRe.MatchString(line) {
			result = append(result, assertionLineInfo{text: trimmed, line: i + 1})
		}
	}
	return result
}

// normalizeAssertionStructure produces a structural key for an assertion line
// by removing comparison operators and replacing them with a placeholder.
// This allows matching "require.Equal(t, 42, result)" with
// "require.NotEqual(t, 41, result)" as structurally the same assertion.
func normalizeAssertionStructure(line string) string {
	norm := line

	// Remove numeric literals (they may change when weakening).
	norm = regexp.MustCompile(`\b\d+\.?\d*\b`).ReplaceAllString(norm, "N")

	// Remove string literals.
	norm = regexp.MustCompile(`"[^"]*"`).ReplaceAllString(norm, "S")

	// Normalize comparison operators to a single placeholder.
	norm = regexp.MustCompile(`[=!<>]+=?`).ReplaceAllString(norm, "OP")

	// Normalize polarity-flipped assertion function names to a canonical form.
	// Process longer keys first to avoid substring collisions (e.g.,
	// "require.NoError" contains "require.Error" as a substring).
	for _, key := range sortedPolarityKeys() {
		canonical := polarityCanonical[key]
		norm = strings.ReplaceAll(norm, key, canonical)
	}

	return strings.TrimSpace(norm)
}

// detectPolarityFlip checks if an assertion function was inverted (e.g.,
// assert.NoError to assert.Error).
func detectPolarityFlip(oldLine, newLine string) string {
	for canA, flipB := range polarityPairs {
		oldHasA := strings.Contains(oldLine, canA)
		oldHasB := strings.Contains(oldLine, flipB)
		newHasA := strings.Contains(newLine, canA)
		newHasB := strings.Contains(newLine, flipB)

		// A->B flip: old has A but not B, new has B but not A.
		if oldHasA && !oldHasB && newHasB && !newHasA {
			return fmt.Sprintf("Assertion polarity flipped: %q -> %q (line ~%s). This inverts the test's expectation.", canA, flipB, guessLineNumber(newLine))
		}
	}
	return ""
}

// detectComparisonFlip checks if a comparison operator was inverted
// (e.g., == to !=, > to <=).
func detectComparisonFlip(oldLine, newLine string) string {
	for _, pair := range comparisonFlips {
		// Strict A->B flip: old has "from" but NOT "to", new has "to" but NOT "from".
		// This prevents false positives on multi-condition lines where one
		// operator is retained and a different one changes.
		oldHasFrom := findOperatorNotInAssert(oldLine, pair.from) >= 0
		oldHasTo := findOperatorNotInAssert(oldLine, pair.to) >= 0
		newHasTo := findOperatorNotInAssert(newLine, pair.to) >= 0
		newHasFrom := findOperatorNotInAssert(newLine, pair.from) >= 0
		if oldHasFrom && !oldHasTo && newHasTo && !newHasFrom {
			return fmt.Sprintf("Comparison operator flipped: %q -> %q. This inverts the assertion's direction.", pair.from, pair.to)
		}
	}
	return ""
}

// findOperatorNotInAssert finds a comparison operator in a line, excluding
// operators that are part of assertion function names (e.g., "NotEqual").
// Returns the index of the operator, or -1 if not found.
func findOperatorNotInAssert(line, op string) int {
	// Skip operators that appear inside function names.
	// We look for the operator NOT preceded by a word character and NOT
	// followed by a word character (standalone comparison).
	re := regexp.MustCompile(`(?:^|[^\w])` + regexp.QuoteMeta(op) + `(?:[^\w]|$)`)
	loc := re.FindStringIndex(line)
	if loc == nil {
		return -1
	}
	return loc[0]
}

// guessLineNumber attempts to find a line number hint from the new content.
// Since we operate on trimmed lines, this is approximate.
func guessLineNumber(_ string) string {
	return "?"
}
