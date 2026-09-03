package agent

import (
	"fmt"
	"strings"
	"sync"

	"github.com/topcheer/ggcode/internal/debug"
)

// Ambiguity Point Detector -- Pre-Run Intent Disambiguation
//
// Research basis: "Intent Formalization: A Grand Challenge for Reliable Coding
// in the Age of AI Agents" (Lahiri, arXiv:2603.17150, Microsoft Research, 2026).
//
// The paper's core insight: AI-generated code is "plausible by construction"
// but NOT "correct by construction." The INTENT GAP -- the semantic distance
// between what a user means and what the agent produces -- is amplified when
// the agent silently picks one interpretation among many valid ones.
//
// The "remove duplicates" example: one interpretation keeps first occurrences
// [1,2,3,2,4]→[1,2,3,4], another removes ALL duplicates [1,2,3,2,4]→[1,3,4].
// No LLM can know which the user intended -- yet it picks one silently.
//
// TiCoder (Lahiri et al., 2022; FSE 2024) demonstrates that identifying
// "points of ambiguity" and disambiguating them BEFORE generating code
// improves correctness from 40% to 84%. The key: target tests at inputs where
// different candidate implementations would produce different outputs.
//
// We can't generate candidate implementations cheaply. Instead, we use
// deterministic heuristics to detect COMMON ambiguity patterns in the user's
// request and inject a gentle reminder to clarify before proceeding.
//
// The detector fires ONCE at the start of a run, before the agent has done
// any work. It uses zero LLM tokens -- all detection is regex-based.
//
// Distinct from existing detectors:
//   - assumption_tracker: detects assumptions in ASSISTANT text mid-run
//   - fulfillment_gate: checks work matches request at completion
//   - This detector: checks the REQUEST itself for inherent ambiguity

const maxAmbiguityWarnings = 1

// ambiguityPointState tracks whether the detector has already fired.
type ambiguityPointState struct {
	mu    sync.Mutex
	fired bool
}

func newAmbiguityPointState() *ambiguityPointState {
	return &ambiguityPointState{}
}

func (s *ambiguityPointState) reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.fired = false
}

// AmbiguitySignal represents one detected ambiguity pattern in a user request.
type AmbiguitySignal struct {
	Phrase     string // the ambiguous phrase matched
	Category   string // semantic category
	Suggestion string // what to clarify
}

// AmbiguityCategory constants describe the type of ambiguity.
const (
	ambDupHandling    = "duplicate-handling"
	ambSortOrder      = "sort-ordering"
	ambEdgeCase       = "edge-case-behavior"
	ambErrorMode      = "error-handling"
	ambScopeVague     = "vague-scope"
	ambQuantityVague  = "vague-quantity"
	ambDirectionVague = "vague-direction"
	ambNamingVague    = "vague-naming"
)

// ambiguityPatterns maps regex-like patterns to their ambiguity signal.
// These are phrases that have well-documented multiple interpretations.
var ambiguityPatterns = []struct {
	phrase     string
	category   string
	suggestion string
}{
	// Duplicate handling: "remove duplicates" can mean dedup OR remove-all
	{"remove duplicate", ambDupHandling, "whether to keep one copy of each element or remove ALL elements that appear more than once"},
	{"deduplicate", ambDupHandling, "whether to keep first or last occurrence"},
	{"dedup", ambDupHandling, "whether to keep first or last occurrence"},
	{"unique elements", ambDupHandling, "whether to keep one copy of each or only elements that appear exactly once"},

	// Sort ordering: ascending vs descending, stable vs unstable
	{"sort the", ambSortOrder, "the sort order (ascending/descending) and stability requirement"},
	{"sort it", ambSortOrder, "the sort order (ascending/descending)"},
	{"order by", ambSortOrder, "ascending or descending, and tie-breaking behavior"},
	{"sorted list", ambSortOrder, "ascending or descending order"},

	// Edge case behavior
	{"empty input", ambEdgeCase, "expected behavior for empty input/collection"},
	{"empty list", ambEdgeCase, "expected behavior for an empty list"},
	{"empty array", ambEdgeCase, "expected behavior for an empty array"},
	{"empty string", ambEdgeCase, "expected behavior for an empty string"},
	{"nil case", ambEdgeCase, "whether nil should return error, empty result, or zero value"},
	{"zero value", ambEdgeCase, "whether zero-value means absence or a valid input"},
	{"negative numbers", ambEdgeCase, "whether negative values are valid or should be rejected"},
	{"negative values", ambEdgeCase, "whether negative values are valid or should be rejected"},

	// Error handling mode
	{"error handling", ambErrorMode, "whether to return error, panic, or use default values on failure"},
	{"handle error", ambErrorMode, "whether to retry, return error, or fail fast"},
	{"on failure", ambErrorMode, "whether to retry, rollback, or propagate the error"},
	{"fallback when", ambErrorMode, "what the fallback value/behavior should be"},
	{"fallback if", ambErrorMode, "what the fallback value/behavior should be"},
	{"fallback value", ambErrorMode, "what the fallback value/behavior should be"},
	{"fallback mechanism", ambErrorMode, "what the fallback behavior should be"},
	{"fallback to", ambErrorMode, "what the fallback value/behavior should be"},
	{"add a fallback", ambErrorMode, "what the fallback value/behavior should be"},

	// Vague scope
	{"all occurrences", ambScopeVague, "whether this includes nested/overlapping matches"},
	{"everything that", ambScopeVague, "the precise set of items to include"},
	// "cleanup" narrowed to vague-quantifier usage; bare "cleanup"
	// collided with identifiers like cleanupConn (#381). "refactor" kept as
	// a word-boundary bare match — "Refactor the authentication module" is
	// genuinely vague scope, and the boundary check no longer hits
	// "refactored"/"refactoring" prose.
	{"clean up some", ambScopeVague, "which specific files/patterns to clean"},
	{"clean up the", ambScopeVague, "which specific files/patterns to clean"},
	{"refactor", ambScopeVague, "the scope -- which files, how aggressive, preserving API or not"},
	{"optimize for", ambScopeVague, "the target metric: speed, memory, readability, or binary size"},

	// Vague quantity
	{"some of the", ambQuantityVague, "how many items, and by what criteria"},
	{"a few", ambQuantityVague, "the exact count or selection criteria"},
	{"recent", ambQuantityVague, "the time window or count for 'recent'"},
	{"latest", ambQuantityVague, "how many of the latest items"},
	// #1438-A: bare 'latest'/'better' are extremely common ordinary
	// words - 'upgrade to the latest version' has NO quantity ambiguity
	// yet hit the quantity suggestion (category mismatch). Narrowed to
	// phrase-level shapes that actually imply an unresolved count/choice;
	// the bare forms are dropped.
	{"latest items", ambQuantityVague, "how many of the latest items"},
	{"latest entries", ambQuantityVague, "how many entries and by what cutoff"},
	{"latest results", ambQuantityVague, "how many results and by what cutoff"},
	{"oldest", ambQuantityVague, "how many of the oldest items"},

	// Vague direction (for modifications)
	{"improve the", ambDirectionVague, "improve toward what goal -- performance, readability, security?"},
	{"improve its", ambDirectionVague, "improve toward what goal -- performance, readability, security?"},
	{"enhance", ambDirectionVague, "enhance in what way specifically"},
	// #1438-A: bare 'better' is an ordinary high-frequency word - a
	// benchmark sentence whose metric is IN the sentence still hit it.
	// Narrowed to unresolved-choice shapes.
	{"make it better", ambDirectionVague, "better by what metric"},
	{"make this better", ambDirectionVague, "better by what metric"},
	{"improve it", ambDirectionVague, "improve toward what goal -- performance, readability, security?"},
	{"fix the bug", ambDirectionVague, "which bug and what does 'fixed' mean here -- error gone, test added, or root cause documented?"},
	{"simplify the", ambDirectionVague, "simplify toward what end -- fewer lines, fewer dependencies, clearer logic?"},
	{"simplify it", ambDirectionVague, "simplify toward what end -- fewer lines, fewer dependencies, clearer logic?"},

	// Vague naming
	{"rename the", ambNamingVague, "the new name convention and whether to update all references"},
	{"rename it", ambNamingVague, "the new name convention and whether to update all references"},
	{"rename to something", ambNamingVague, "the specific target name"},

	// #1438-B: the table had zero CJK patterns - the primary user language
	// (Simplified Chinese UI, README_zh-CN) had 0% coverage. Chinese bytes
	// pass the length gate; only the pattern side was missing. These mirror
	// the highest-value English entries (the L21-23 comment's own examples).
	{"优化一下", ambDirectionVague, "improve toward what goal -- performance, readability, security?"},
	{"优化这个", ambDirectionVague, "improve toward what goal -- performance, readability, security?"},
	{"去掉重复", ambScopeVague, "which duplicates exactly, and dedupe by what key"},
	{"去重", ambScopeVague, "which duplicates exactly, and dedupe by what key"},
	{"排个序", ambSortOrder, "ascending or descending, and by which key"},
	{"排序", ambSortOrder, "ascending or descending, and by which key"},
	{"重命名", ambNamingVague, "the new name convention and whether to update all references"},
	{"改名", ambNamingVague, "the new name convention and whether to update all references"},
	{"最新的", ambQuantityVague, "how many of the latest items, and by what cutoff"},
	{"最近的", ambQuantityVague, "the time window or count for 'recent'"},
	{"随便", ambScopeVague, "which specific item or criteria"},
	{"大概", ambQuantityVague, "the exact count or selection criteria"},
}

// phrases that indicate this is a quick, unambiguous task -- skip detection
var quickTaskPhrases = []string{
	"what is", "what are", "how many", "how does", "how do",
	"help me understand", "explain", "describe",
	"is there", "are there", "where is", "where are",
}

// ambContainsPhrase matches phrase in text with word boundaries on both
// sides. Bare strings.Contains matched "recent" inside "recently" and
// "cleanup" inside identifier names, firing clarify guidance on ordinary
// dev prompts (#381).
func ambContainsPhrase(text, phrase string) bool {
	for from := 0; ; {
		idx := strings.Index(text[from:], phrase)
		if idx < 0 {
			return false
		}
		i := from + idx
		end := i + len(phrase)
		beforeOK := i == 0 || !isWordByte(text[i-1])
		afterOK := end >= len(text) || !isWordByte(text[end])
		if !afterOK && end < len(text) && text[end] == 's' &&
			(end+1 >= len(text) || !isWordByte(text[end+1])) {
			// Tolerate a plural suffix: "remove duplicate" should match
			// "remove duplicates". Only 's' — "recent" must still NOT match
			// "recently" (#381).
			afterOK = true
		}
		if beforeOK && afterOK {
			return true
		}
		from = end
	}
}

// checkAmbiguityPoints scans the user's initial request for known ambiguity
// patterns. Returns a guidance message if ambiguity is detected, or "".
//
// The message is injected as a user message before the agent starts its main
// work loop, nudging it to clarify ambiguous points with the user rather than
// silently guessing.
func (a *Agent) checkAmbiguityPoints(userPrompt string) string {
	a.ambiguityPoint.mu.Lock()
	defer a.ambiguityPoint.mu.Unlock()

	if a.ambiguityPoint.fired {
		return ""
	}

	if len(userPrompt) == 0 {
		return ""
	}

	// Don't fire for very short prompts (single-word commands like "run", "test")
	// #1438-C: the 15-char gate overshot - "make it better" (14) and
	// "simplify it" (11), the textbook vague-direction prototypes, were
	// all skipped. 6 chars still blocks bare one-word commands while
	// letting two-word vague directions through.
	if len(strings.TrimSpace(userPrompt)) < 6 {
		return ""
	}

	lower := strings.ToLower(userPrompt)
	trimmed := strings.TrimSpace(lower)

	// Skip if this starts with an informational/query phrase
	for _, q := range quickTaskPhrases {
		if strings.HasPrefix(trimmed, q) {
			return ""
		}
	}

	// Detect ambiguity signals. Bare substring matching used to hit normal
	// dev prose — "recent" matched "recently", "cleanup" matched function
	// names like cleanupConn — firing spurious clarify guidance almost every
	// run (#381). Word-boundary matching plus narrowed phrasing for the
	// worst offenders.
	var signals []AmbiguitySignal
	seen := make(map[string]bool)
	for _, pat := range ambiguityPatterns {
		if ambContainsPhrase(lower, pat.phrase) {
			key := pat.category
			if seen[key] {
				continue // one signal per category
			}
			seen[key] = true
			signals = append(signals, AmbiguitySignal{
				Phrase:     pat.phrase,
				Category:   pat.category,
				Suggestion: pat.suggestion,
			})
		}
	}

	// Need at least 1 signal to fire
	if len(signals) == 0 {
		return ""
	}

	// Cap at 3 distinct signals to avoid overwhelming
	if len(signals) > 3 {
		signals = signals[:3]
	}

	a.ambiguityPoint.fired = true

	var sb strings.Builder
	sb.WriteString("[Ambiguity Detection] The request contains phrases with multiple valid interpretations. ")
	sb.WriteString("Research shows that silently choosing one interpretation is a leading cause of intent mismatch ")
	sb.WriteString("(Lahiri, arXiv:2603.17150). Before proceeding, briefly clarify:\n\n")

	for i, sig := range signals {
		sb.WriteString(fmt.Sprintf("  %d. \"%s\" -- clarify %s\n", i+1, sig.Phrase, sig.Suggestion))
	}

	sb.WriteString("\n")
	sb.WriteString("If the user's intent is already clear from context, proceed without asking. ")
	sb.WriteString("But if the above points genuinely have multiple valid answers, a brief clarification ")
	sb.WriteString("prevents wasted iterations from choosing the wrong interpretation.")

	debug.Log("agent", "ambiguity point detector: %d signals detected, injecting guidance", len(signals))
	return sb.String()
}
