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
	{"empty", ambEdgeCase, "expected behavior for empty input/collection"},
	{"nil case", ambEdgeCase, "whether nil should return error, empty result, or zero value"},
	{"zero value", ambEdgeCase, "whether zero-value means absence or a valid input"},
	{"negative", ambEdgeCase, "whether negative values are valid or should be rejected"},

	// Error handling mode
	{"error handling", ambErrorMode, "whether to return error, panic, or use default values on failure"},
	{"handle error", ambErrorMode, "whether to retry, return error, or fail fast"},
	{"on failure", ambErrorMode, "whether to retry, rollback, or propagate the error"},
	{"fallback", ambErrorMode, "what the fallback value/behavior should be"},

	// Vague scope
	{"all occurrences", ambScopeVague, "whether this includes nested/overlapping matches"},
	{"everything that", ambScopeVague, "the precise set of items to include"},
	{"cleanup", ambScopeVague, "which specific files/patterns to clean"},
	{"refactor", ambScopeVague, "the scope -- which files, how aggressive, preserving API or not"},
	{"optimize", ambScopeVague, "the target metric: speed, memory, readability, or binary size"},

	// Vague quantity
	{"some", ambQuantityVague, "how many items, and by what criteria"},
	{"a few", ambQuantityVague, "the exact count or selection criteria"},
	{"recent", ambQuantityVague, "the time window or count for 'recent'"},
	{"latest", ambQuantityVague, "how many of the latest items"},
	{"oldest", ambQuantityVague, "how many of the oldest items"},

	// Vague direction (for modifications)
	{"improve", ambDirectionVague, "improve toward what goal -- performance, readability, security?"},
	{"enhance", ambDirectionVague, "enhance in what way specifically"},
	{"better", ambDirectionVague, "better by what metric"},
	{"simplify", ambDirectionVague, "simplify toward what end -- fewer lines, fewer dependencies, clearer logic?"},

	// Vague naming
	{"rename", ambNamingVague, "the new name convention and whether to update all references"},
	{"rename to something", ambNamingVague, "the specific target name"},
}

// phrases that indicate this is a quick, unambiguous task -- skip detection
var quickTaskPhrases = []string{
	"what is", "what are", "how many", "how does", "how do",
	"help me understand", "explain", "describe",
	"is there", "are there", "where is", "where are",
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
	if len(strings.TrimSpace(userPrompt)) < 15 {
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

	// Detect ambiguity signals
	var signals []AmbiguitySignal
	seen := make(map[string]bool)
	for _, pat := range ambiguityPatterns {
		if strings.Contains(lower, pat.phrase) {
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
