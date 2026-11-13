package agent

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
)

var actionVerbs = []string{
	"add", "create", "implement", "fix", "repair", "build",
	"refactor", "update", "modify", "change", "remove", "delete",
	"write", "generate", "convert", "migrate", "optimize", "extract",
	"rename", "replace", "insert", "move", "split", "merge",
}

var questionIndicators = []string{
	"what ", "why ", "how does", "how do", "explain", "is there",
	"are there", "can you tell", "show me", "list all", "where is",
	"where are", "find ", "search for", "describe",
}

var readOnlyIndicators = []string{
	"check", "review", "inspect", "audit", "scan", "examine",
	"verify", "monitor", "query", "lookup", "report", "summarize",
	"定时审查", "检查", "审查", "查询", "确认", "汇总",
}

type fulfillmentGateState struct {
	fired bool
}

func newFulfillmentGateState() *fulfillmentGateState {
	return &fulfillmentGateState{}
}

func (f *fulfillmentGateState) reset() {
	f.fired = false
}

func (a *Agent) checkFulfillmentGate(userPrompt string, runStats *RunStats, assistantText string) string {
	if a.fulfillmentGate.fired {
		return ""
	}

	prompt := strings.ToLower(userPrompt)
	if len(strings.TrimSpace(prompt)) < 10 {
		return ""
	}

	if isInformationalRequest(prompt) {
		debug.Log("fulfillment-gate", "request appears informational, skipping")
		return ""
	}

	if isReadOnlyTask(prompt) {
		debug.Log("fulfillment-gate", "request appears read-only/audit task, skipping")
		return ""
	}

	actions := detectActions(prompt)
	if len(actions) == 0 {
		debug.Log("fulfillment-gate", "no action verbs detected in request, skipping")
		return ""
	}

	requestKeywords := extractRequestKeywords(prompt)
	if len(requestKeywords) == 0 {

		debug.Log("fulfillment-gate", "no extractable keywords in request, skipping")
		return ""
	}

	filesEdited := runStats.FilesEdited
	hasEdits := len(filesEdited) > 0
	hasCommands := len(runStats.CommandsRun) > 0

	if !hasEdits && !hasCommands && len(runStats.ToolCalls) > 0 {
		a.fulfillmentGate.fired = true
		debug.Log("fulfillment-gate", "action requested but no edits or commands — injecting reminder")
		return fmt.Sprintf(
			"Before finishing: your request included action verbs (%s) but no files were edited and no commands were run. "+
				"If the user asked you to make changes, verify you have completed all requested modifications. "+
				"If you determined no changes are needed, clearly explain why in your response.",
			strings.Join(actions[:min(3, len(actions))], ", "),
		)
	}

	if hasEdits && len(requestKeywords) >= 1 {
		matched := countFileKeywordMatches(requestKeywords, filesEdited)

		if matched == 0 && len(filesEdited) <= 5 {
			a.fulfillmentGate.fired = true
			debug.Log("fulfillment-gate", "edited files have no keyword overlap with request — injecting reminder")
			return fmt.Sprintf(
				"Before finishing: you edited %d file(s) but none match the key terms in the user's request (%s). "+
					"Verify you addressed the correct files and that all requested changes are complete.",
				len(filesEdited), strings.Join(requestKeywords[:min(3, len(requestKeywords))], ", "),
			)
		}
	}

	parts := detectMultiPart(prompt)
	if parts >= 2 && hasEdits {

		expected := (parts + 1) / 2
		if expected < 1 {
			expected = 1
		}
		if len(filesEdited) < expected {
			a.fulfillmentGate.fired = true
			debug.Log("fulfillment-gate", "multi-part request (%d parts) but only %d files edited — injecting reminder", parts, len(filesEdited))
			return fmt.Sprintf(
				"Before finishing: the user's request appears to have %d distinct parts. "+
					"Verify each part was addressed — only %d file(s) were edited. "+
					"If some parts were addressed within the same file or don't require code changes, this is fine.",
				parts, len(filesEdited),
			)
		}
	}

	debug.Log("fulfillment-gate", "passed: actions=%d keywords=%d files=%d parts=%d",
		len(actions), len(requestKeywords), len(filesEdited), parts)
	return ""
}

func isInformationalRequest(prompt string) bool {

	prompt = strings.ToLower(prompt)
	questionCount := 0
	for _, indicator := range questionIndicators {
		if strings.Contains(prompt, indicator) {
			questionCount++
		}
	}
	if questionCount == 0 {
		return false
	}

	actionCount := 0
	for _, verb := range actionVerbs {
		if containsWordOutsideRefs(prompt, verb) {
			actionCount++
		}
	}
	return actionCount == 0 || questionCount > actionCount
}

func isReadOnlyTask(prompt string) bool {
	readOnlyCount := 0
	for _, indicator := range readOnlyIndicators {
		if containsWord(prompt, indicator) {
			readOnlyCount++
		}
	}
	if readOnlyCount == 0 {
		return false
	}

	actionCount := 0
	for _, verb := range actionVerbs {
		if containsWordOutsideRefs(prompt, verb) {
			actionCount++
		}
	}
	return readOnlyCount > actionCount
}

func containsWord(text, word string) bool {
	idx := strings.Index(strings.ToLower(text), strings.ToLower(word))
	for idx >= 0 {
		end := idx + len(word)
		leftOK := idx == 0 || !isAlphaNum(text[idx-1])
		rightOK := end >= len(text) || !isAlphaNum(text[end])
		if leftOK && rightOK {
			return true
		}
		next := strings.Index(strings.ToLower(text[end:]), strings.ToLower(word))
		if next < 0 {
			break
		}
		idx = end + next
	}
	return false
}

// containsWordOutsideRefs reports whether word appears as a standalone word
// OUTSIDE quoted spans and bracketed issue-reference spans. Verbs inside
// quotes (e.g. quoted commit titles) or parentheses carrying #NNNN
// references are quoted material, not instructions aimed at the current
// session (#5554).
func containsWordOutsideRefs(text, word string) bool {
	lower := strings.ToLower(text)
	w := strings.ToLower(word)
	idx := strings.Index(lower, w)
	for idx >= 0 {
		end := idx + len(w)
		leftOK := idx == 0 || !isAlphaNum(text[idx-1])
		rightOK := end >= len(text) || !isAlphaNum(text[end])
		if leftOK && rightOK && !inReferenceContext(text, idx, end) {
			return true
		}
		next := strings.Index(lower[end:], w)
		if next < 0 {
			break
		}
		idx = end + next
	}
	return false
}

var issueRefRe = regexp.MustCompile(`#\d{2,6}`)

// inReferenceContext reports whether text[start:end] sits inside a quoted
// span (straight or curly single/double quotes) or inside a bracket span
// that contains an #NNNN issue reference. Both carry verbs belonging to
// foreign material, not to the current session (#5554).
func inReferenceContext(text string, start, end int) bool {
	for _, q := range []string{"'", "\""} {
		if insideSameQuoteSpan(text, q, start) {
			return true
		}
	}
	for _, pair := range [2][2]string{{"“", "”"}, {"‘", "’"}} {
		if insideQuotePairSpan(text, pair[0], pair[1], start) {
			return true
		}
	}
	for _, pair := range [2][2]string{{"(", ")"}, {"（", "）"}} {
		if insideBracketWithRef(text, pair[0], pair[1], start, end) {
			return true
		}
	}
	return false
}

// insideSameQuoteSpan: odd count of the same quote char before start means
// the position sits between an open and close of that quote type.
func insideSameQuoteSpan(text, q string, start int) bool {
	return strings.Count(text[:start], q)%2 == 1
}

// insideQuotePairSpan: an unclosed opening curly quote exists before start.
func insideQuotePairSpan(text, open, close string, start int) bool {
	lastOpen := strings.LastIndex(text[:start], open)
	if lastOpen < 0 {
		return false
	}
	return strings.Index(text[lastOpen+len(open):start], close) < 0
}

// insideBracketWithRef: start..end lies inside a bracket span whose full
// span contains an #NNNN issue reference (e.g. a fullwidth-bracket
// citation quoting a commit title). Bare parentheses without a reference
// are NOT exempt, so plain
// instructions like "fix (urgent) #1234" still count.
func insideBracketWithRef(text, open, close string, start, end int) bool {
	lastOpen := strings.LastIndex(text[:start], open)
	if lastOpen < 0 {
		return false
	}
	if strings.Index(text[lastOpen:start], close) >= 0 {
		return false
	}
	nextClose := strings.Index(text[end:], close)
	if nextClose < 0 {
		return false
	}
	span := text[lastOpen : end+nextClose+len(close)]
	return issueRefRe.MatchString(span)
}

func isAlphaNum(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9') || b == '_'
}

func detectActions(prompt string) []string {
	var found []string
	seen := make(map[string]bool)
	for _, verb := range actionVerbs {
		if containsWordOutsideRefs(prompt, verb) && !seen[verb] {
			found = append(found, verb)
			seen[verb] = true
		}
	}
	return found
}

func extractRequestKeywords(prompt string) []string {
	var keywords []string
	seen := make(map[string]bool)

	words := strings.Fields(prompt)
	for _, word := range words {
		clean := strings.Trim(word, "`\"'()[]{},.;:!?")
		clean = strings.ToLower(clean)

		if strings.Contains(clean, ".") && len(clean) > 3 {
			parts := strings.Split(clean, "/")
			name := parts[len(parts)-1]
			if !seen[name] && len(name) > 3 {
				keywords = append(keywords, name)
				seen[name] = true
			}
		}

		if isIdentifier(clean) && len(clean) > 4 && !isStopWord(clean) {
			if !seen[clean] {
				keywords = append(keywords, clean)
				seen[clean] = true
			}
		}
	}
	return keywords
}

func countFileKeywordMatches(keywords, files []string) int {
	matched := 0
	for _, kw := range keywords {
		for _, f := range files {
			if strings.Contains(strings.ToLower(f), kw) {
				matched++
				break
			}
		}
	}
	return matched
}

func detectMultiPart(prompt string) int {
	count := 1

	conjunctions := []string{" and ", " then ", " also ", " after that "}
	for _, conj := range conjunctions {
		if strings.Contains(prompt, conj) {
			count++
		}
	}

	for i := 2; i <= 9; i++ {
		marker := fmt.Sprintf("%d. ", i)
		if strings.Contains(prompt, marker) {
			count++
		}
	}
	if strings.Contains(prompt, "- ") || strings.Contains(prompt, "* ") {
		count++
	}

	actions := detectActions(prompt)
	if len(actions) > count {
		count = len(actions)
	}

	if count > 5 {
		count = 5
	}
	return count
}

func isIdentifier(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') ||
			(c >= '0' && c <= '9') || c == '_' || c == '-') {
			return false
		}
	}
	return true
}

func isStopWord(s string) bool {
	stopWords := map[string]bool{
		"the": true, "this": true, "that": true, "with": true, "from": true,
		"have": true, "should": true, "would": true, "could": true,
		"there": true, "their": true, "about": true, "which": true,
		"when": true, "where": true, "what": true, "these": true,
		"those": true, "being": true, "having": true, "using": true,
		"after": true, "before": true, "needs": true, "need": true,
		"make": true, "sure": true, "into": true, "some": true,
		"will": true, "must": true, "does": true, "doesn": true,
		"don": true, "isn": true, "wasn": true, "aren": true,
		"they": true, "them": true, "your": true, "code": true,
		"file": true, "files": true, "test": true, "tests": true,
		"function": true, "method": true, "class": true, "struct": true,
		"package": true, "module": true, "error": true, "handle": true,
		"system": true, "here": true, "please": true, "check": true,
	}
	return stopWords[s]
}
