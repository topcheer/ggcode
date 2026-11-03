package agent

import (
	"fmt"
	"regexp"
	"strings"
	"sync"

	"github.com/topcheer/ggcode/internal/debug"
)

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

type AmbiguitySignal struct {
	Phrase     string
	Category   string
	Suggestion string
}

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

var ambiguityPatterns = []struct {
	phrase     string
	category   string
	suggestion string
}{

	{"remove duplicate", ambDupHandling, "whether to keep one copy of each element or remove ALL elements that appear more than once"},
	{"deduplicate", ambDupHandling, "whether to keep first or last occurrence"},
	{"dedup", ambDupHandling, "whether to keep first or last occurrence"},
	{"unique elements", ambDupHandling, "whether to keep one copy of each or only elements that appear exactly once"},

	{"sort the", ambSortOrder, "the sort order (ascending/descending) and stability requirement"},
	{"sort it", ambSortOrder, "the sort order (ascending/descending)"},
	{"order by", ambSortOrder, "ascending or descending, and tie-breaking behavior"},
	{"sorted list", ambSortOrder, "ascending or descending order"},

	{"empty input", ambEdgeCase, "expected behavior for empty input/collection"},
	{"empty list", ambEdgeCase, "expected behavior for an empty list"},
	{"empty array", ambEdgeCase, "expected behavior for an empty array"},
	{"empty string", ambEdgeCase, "expected behavior for an empty string"},
	{"nil case", ambEdgeCase, "whether nil should return error, empty result, or zero value"},
	{"zero value", ambEdgeCase, "whether zero-value means absence or a valid input"},
	{"negative numbers", ambEdgeCase, "whether negative values are valid or should be rejected"},
	{"negative values", ambEdgeCase, "whether negative values are valid or should be rejected"},

	{"error handling", ambErrorMode, "whether to return error, panic, or use default values on failure"},
	{"handle error", ambErrorMode, "whether to retry, return error, or fail fast"},
	{"on failure", ambErrorMode, "whether to retry, rollback, or propagate the error"},
	{"fallback when", ambErrorMode, "what the fallback value/behavior should be"},
	{"fallback if", ambErrorMode, "what the fallback value/behavior should be"},
	{"fallback value", ambErrorMode, "what the fallback value/behavior should be"},
	{"fallback mechanism", ambErrorMode, "what the fallback behavior should be"},
	{"fallback to", ambErrorMode, "what the fallback value/behavior should be"},
	{"add a fallback", ambErrorMode, "what the fallback value/behavior should be"},

	{"all occurrences", ambScopeVague, "whether this includes nested/overlapping matches"},
	{"everything that", ambScopeVague, "the precise set of items to include"},

	{"clean up some", ambScopeVague, "which specific files/patterns to clean"},
	{"clean up the", ambScopeVague, "which specific files/patterns to clean"},
	{"refactor", ambScopeVague, "the scope -- which files, how aggressive, preserving API or not"},
	{"optimize for", ambScopeVague, "the target metric: speed, memory, readability, or binary size"},

	{"some of the", ambQuantityVague, "how many items, and by what criteria"},
	{"a few", ambQuantityVague, "the exact count or selection criteria"},

	{"latest items", ambQuantityVague, "how many of the latest items"},
	{"latest entries", ambQuantityVague, "how many entries and by what cutoff"},
	{"latest results", ambQuantityVague, "how many results and by what cutoff"},

	{"improve the", ambDirectionVague, "improve toward what goal -- performance, readability, security?"},
	{"improve its", ambDirectionVague, "improve toward what goal -- performance, readability, security?"},
	{"enhance", ambDirectionVague, "enhance in what way specifically"},

	{"make it better", ambDirectionVague, "better by what metric"},
	{"make this better", ambDirectionVague, "better by what metric"},
	{"improve it", ambDirectionVague, "improve toward what goal -- performance, readability, security?"},
	{"fix the bug", ambDirectionVague, "which bug and what does 'fixed' mean here -- error gone, test added, or root cause documented?"},
	{"simplify the", ambDirectionVague, "simplify toward what end -- fewer lines, fewer dependencies, clearer logic?"},
	{"simplify it", ambDirectionVague, "simplify toward what end -- fewer lines, fewer dependencies, clearer logic?"},

	{"rename the", ambNamingVague, "the new name convention and whether to update all references"},
	{"rename it", ambNamingVague, "the new name convention and whether to update all references"},
	{"rename to something", ambNamingVague, "the specific target name"},

	{"优化一下", ambDirectionVague, "improve toward what goal -- performance, readability, security?"},
	{"优化这个", ambDirectionVague, "improve toward what goal -- performance, readability, security?"},
	{"去掉重复", ambScopeVague, "which duplicates exactly, and dedupe by what key"},
	{"去重一下", ambScopeVague, "which duplicates exactly, and dedupe by what key"},
	{"帮我去重", ambScopeVague, "which duplicates exactly, and dedupe by what key"},
	{"排个序", ambSortOrder, "ascending or descending, and by which key"},

	{"排序一下", ambSortOrder, "ascending or descending, and by which key"},
	{"帮我排序", ambSortOrder, "ascending or descending, and by which key"},
	{"重命名一下", ambNamingVague, "the new name convention and whether to update all references"},
	{"改个名", ambNamingVague, "the new name convention and whether to update all references"},
	{"最新的几个", ambQuantityVague, "how many of the latest items, and by what cutoff"},
	{"大概几个", ambQuantityVague, "the exact count or selection criteria"},
	{"大概多少", ambQuantityVague, "the exact count or selection criteria"},
	{"随便挑", ambScopeVague, "which specific item or criteria"},
	{"随便选", ambScopeVague, "which specific item or criteria"},
}

var quickTaskPhrases = []string{
	"what is", "what are", "how many", "how does", "how do",
	"help me understand", "explain", "describe",
	"is there", "are there", "where is", "where are",
}

func ambPhraseIndex(text, phrase string) int {
	for from := 0; ; {
		idx := strings.Index(text[from:], phrase)
		if idx < 0 {
			return -1
		}
		i := from + idx
		end := i + len(phrase)
		beforeOK := i == 0 || !isWordByte(text[i-1])
		afterOK := end >= len(text) || !isWordByte(text[end])
		if !afterOK && end < len(text) && text[end] == 's' &&
			(end+1 >= len(text) || !isWordByte(text[end+1])) {

			afterOK = true
		}
		if beforeOK && afterOK {
			return i
		}
		from = end
	}
}

func ambContainsPhrase(text, phrase string) bool {
	return ambPhraseIndex(text, phrase) >= 0
}

var ambDirectionRe = regexp.MustCompile(`(?i)\b(asc|desc|ascending|descending)\b`)

var ambiguitySuppressors = map[string][]*regexp.Regexp{

	"sort the": {ambDirectionRe},
	"order by": {ambDirectionRe},

	"improve the": {regexp.MustCompile(`\bof\b.*\bby\b`)},

	"rename the": {
		regexp.MustCompile(`\bto\s+["'\x60][^"'\x60]+["'\x60]`),
		regexp.MustCompile(`\bto\s+[^\s.,;!?]*\.[A-Za-z0-9]+\b`),
		regexp.MustCompile(`\bto\s+[A-Z][A-Za-z0-9_-]*\b`),
	},

	"帮我排序":  {regexp.MustCompile(`升序|降序|倒序|正序|从大到小|从小到大`)},
	"重命名一下": {regexp.MustCompile(`改成|改为|重命名为`)},
	"改个名":   {regexp.MustCompile(`改成|改为|重命名为`)},
}

func ambBreakAt(text string, i int) bool {
	b := text[i]
	if b == '.' && i+1 < len(text) && isWordByte(text[i+1]) {
		return false
	}
	return b == '.' || b == '!' || b == '?' || b == '\n' || b == ';'
}

func ambSentenceAt(text string, start, end int) string {
	left := start
	for left > 0 && !ambBreakAt(text, left-1) {
		left--
	}
	right := end
	for right < len(text) && !ambBreakAt(text, right) {
		right++
	}
	return text[left:right]
}

func ambDisambiguated(phrase, sentence string) bool {
	for _, re := range ambiguitySuppressors[phrase] {
		if re.MatchString(sentence) {
			return true
		}
	}
	return false
}

func (a *Agent) checkAmbiguityPoints(userPrompt string) string {
	a.ambiguityPoint.mu.Lock()
	defer a.ambiguityPoint.mu.Unlock()

	if a.ambiguityPoint.fired {
		return ""
	}

	if len(userPrompt) == 0 {
		return ""
	}

	if len(strings.TrimSpace(userPrompt)) < 6 {
		return ""
	}

	lower := strings.ToLower(userPrompt)
	trimmed := strings.TrimSpace(lower)

	for _, q := range quickTaskPhrases {
		if strings.HasPrefix(trimmed, q) {
			return ""
		}
	}

	var signals []AmbiguitySignal
	seen := make(map[string]bool)
	for _, pat := range ambiguityPatterns {
		i := ambPhraseIndex(lower, pat.phrase)
		if i < 0 {
			continue
		}

		if ambDisambiguated(pat.phrase, ambSentenceAt(lower, i, i+len(pat.phrase))) {
			continue
		}
		key := pat.category
		if seen[key] {
			continue
		}
		seen[key] = true
		signals = append(signals, AmbiguitySignal{
			Phrase:     pat.phrase,
			Category:   pat.category,
			Suggestion: pat.suggestion,
		})
	}

	if len(signals) == 0 {
		return ""
	}

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
