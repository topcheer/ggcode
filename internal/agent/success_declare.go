package agent

import (
	"strings"
	"sync"

	"github.com/topcheer/ggcode/internal/debug"
)

const (
	sdMinActionsAfterDecl = 2

	sdMaxWarns = 1
)

var sdDeclPhrases = []string{
	"task is complete",
	"task complete",
	"task is done",
	"all done",
	"all set",
	"we're done",
	"i'm done",
	"i am done",
	"done",
	"work is complete",
	"implementation is complete",
	"implementation is finished",
	"the fix is ready",
	"the fix works",
	"changes are complete",
	"changes are ready",
	"everything is working",
	"everything works now",
	"the issue is resolved",
	"issue is fixed",
	"problem is solved",
	"successfully implemented",
	"successfully completed",
	"finished implementing",
}

var sdCaveatPhrases = []string{
	"once you",
	"after you",
	"if you",
	"when you",
	"before we can consider this done",
	"still need to",
	"next step",
	"remaining",
	"however",
	"but first",
	"caveat",
	"note that",

	"to be done", "to be finished", "to be completed",

	"let me verify", "verify if", "verifying if", "verifying whether",
	"check if", "checking if", "let me check",

	"make sure", "making sure",
}

var sdVerifyIntentPhrases = map[string]bool{
	"let me verify":     true,
	"verify if":         true,
	"verifying if":      true,
	"verifying whether": true,
	"check if":          true,
	"checking if":       true,
	"let me check":      true,
	"make sure":         true,
	"making sure":       true,
}

const sdCaveatWindow = 50

type successDeclareState struct {
	mu              sync.Mutex
	declarationIter int
	declarationTxt  string
	actionsSince    int
	warnCount       int
	fired           bool
}

func newSuccessDeclareState() *successDeclareState {
	return &successDeclareState{declarationIter: -1}
}

func (s *successDeclareState) reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.declarationIter = -1
	s.declarationTxt = ""
	s.actionsSince = 0
	s.warnCount = 0
	s.fired = false
}

func (s *successDeclareState) recordAssistantText(text string, iter int) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.declarationIter >= 0 {
		return
	}

	lower := strings.ToLower(text)
	declPhrase, declIdx := sdFindDeclaration(lower)
	if declIdx < 0 {
		return
	}

	if sdHasCaveatWindowed(lower, declIdx, declIdx+len(declPhrase)) {
		return
	}

	if sdHasContinuationWindowed(lower, declIdx+len(declPhrase)) {
		return
	}

	if sdHasNegationBefore(lower, declIdx, declIdx+len(declPhrase)) {
		return
	}

	s.declarationIter = iter

	snippet := text
	if len(snippet) > 120 {
		snippet = snippet[:120] + "..."
	}
	s.declarationTxt = snippet
	s.actionsSince = 0
	debug.Log("agent", "Success declaration recorded at iteration %d: %s", iter, snippet)
}

var sdNegationPatterns = []string{
	"isn't done", "not done", "aren't done", "not finished",
	"isn't complete", "not complete", "isn't working", "not working",
	"isn't fixed", "not fixed", "isn't resolved", "not resolved",
	"nothing is done", "not yet", "isn't ready", "not ready",
}

func sdHasNegationBefore(lowerText string, declIdx, declEnd int) bool {
	const lookbehind = 48
	start := declIdx - lookbehind
	if start < 0 {
		start = 0
	}
	if declEnd > len(lowerText) || declEnd < declIdx {
		declEnd = declIdx
	}
	window := lowerText[start:declEnd]
	for _, n := range sdNegationPatterns {
		from := 0
		for {
			idx := strings.Index(window[from:], n)
			if idx < 0 {
				break
			}
			idx += from

			if !strings.ContainsAny(window[idx+len(n):], ".;\n") {
				return true
			}
			from = idx + 1
		}
	}
	return false
}

func (s *successDeclareState) recordToolCall() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.declarationIter >= 0 {
		s.actionsSince++
	}
}

func (s *successDeclareState) maybeWarn(currentIter int) string {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.fired || s.warnCount >= sdMaxWarns {
		return ""
	}
	if s.declarationIter < 0 {
		return ""
	}

	if s.actionsSince < sdMinActionsAfterDecl {
		return ""
	}
	if currentIter <= s.declarationIter+1 {
		return ""
	}

	s.fired = true
	s.warnCount++

	return `[Metacognitive Calibration] You declared task completion at iteration ` +
		itoaSD(s.declarationIter+1) + `, but have since taken ` + itoaSD(s.actionsSince) +
		` additional tool call actions. This indicates your success declaration was premature: ` +
		`the task was not actually complete when you said it was. ` +
		`Before declaring completion, verify: (1) the build passes, (2) tests pass, ` +
		`(3) all requirements from the original request are met. ` +
		`Avoid claiming "done" while unresolved work remains. ` +
		`If additional work was discovered after your declaration, acknowledge the gap explicitly.`
}

func sdFindDeclaration(lowerText string) (string, int) {
	for _, phrase := range sdDeclPhrases {

		if phrase == "done" || phrase == "all set" {
			var idx int
			if phrase == "done" {
				idx = sdIndexDone(lowerText)
			} else {
				idx = sdIndexWord(lowerText, phrase)
			}
			if idx >= 0 {
				return phrase, idx
			}
			continue
		}
		if idx := strings.Index(lowerText, phrase); idx >= 0 {
			return phrase, idx
		}
	}
	return "", -1
}

func sdIndexWord(lowerText, word string) int {
	for from := 0; ; {
		idx := strings.Index(lowerText[from:], word)
		if idx < 0 {
			return -1
		}
		i := from + idx
		beforeOK := i == 0 || !isWordByte(lowerText[i-1])
		afterOK := i+len(word) >= len(lowerText) || !isWordByte(lowerText[i+len(word)])
		if beforeOK && afterOK {
			return i
		}
		from = i + len(word)
	}
}

func isWordByte(b byte) bool {
	return b == '_' || (b >= 'a' && b <= 'z') || (b >= '0' && b <= '9')
}

func sdIndexDone(lowerText string) int {
	for from := 0; ; {
		rel := sdIndexWord(lowerText[from:], "done")
		if rel < 0 {
			return -1
		}
		i := from + rel
		end := i + len("done")

		if end < len(lowerText) && lowerText[end] == '(' {
			from = end
			continue
		}
		if i > 0 && lowerText[i-1] == '.' {
			from = end
			continue
		}
		return i
	}
}

func sdContainsDeclaration(lowerText string) bool {
	_, idx := sdFindDeclaration(lowerText)
	return idx >= 0
}

var sdContinuationPhrases = []string{
	"next,",
	"next up",
	"then i",
	"now let's",
	"now lets",
	"moving on",
	"after that",
}

func sdHasContinuationWindowed(lowerText string, declEnd int) bool {
	end := declEnd + sdCaveatWindow
	if end > len(lowerText) {
		end = len(lowerText)
	}
	after := lowerText[declEnd:end]
	for _, c := range sdContinuationPhrases {
		if sdPhraseAtWordBoundary(after, c) {
			return true
		}
	}
	return false
}

func sdPhraseAtWordBoundary(s, phrase string) bool {
	isWord := func(b byte) bool {
		return b >= 'a' && b <= 'z' || b >= '0' && b <= '9'
	}
	for from := 0; ; {
		idx := strings.Index(s[from:], phrase)
		if idx < 0 {
			return false
		}
		idx += from
		beforeOK := idx == 0 || !isWord(s[idx-1])
		afterIdx := idx + len(phrase)
		afterOK := afterIdx >= len(s) || !isWord(s[afterIdx])
		if beforeOK && afterOK {
			return true
		}
		from = idx + 1
	}
}

func sdHasCaveatWindowed(lowerText string, declStart, declEnd int) bool {
	start := declStart - sdCaveatWindow
	if start < 0 {
		start = 0
	}
	end := declEnd + sdCaveatWindow
	if end > len(lowerText) {
		end = len(lowerText)
	}
	window := lowerText[start:end]
	for _, caveat := range sdCaveatPhrases {
		idx := strings.Index(window, caveat)
		if idx < 0 {
			continue
		}
		absIdx := start + idx

		negStart := absIdx - 12
		if negStart < 0 {
			negStart = 0
		}
		trimmed := strings.TrimSpace(lowerText[negStart:absIdx])
		negated := false
		for _, neg := range []string{"no", "not", "nothing", "none", "without", "zero"} {
			if trimmed == neg {
				negated = true
				break
			}

			if strings.HasSuffix(trimmed, neg) {
				at := len(trimmed) - len(neg) - 1
				b := trimmed[at]
				isWordByte := b >= 'a' && b <= 'z' || b >= '0' && b <= '9'
				if !isWordByte {
					negated = true
					break
				}
			}
		}
		if negated {
			continue
		}

		if absIdx < declStart {

			caveatEnd := absIdx + len(caveat)
			if caveatEnd >= declStart {

				return true
			}
			between := lowerText[caveatEnd:declStart]
			if !strings.ContainsAny(between, ".;\n") {
				return true
			}

			continue
		}

		between := lowerText[declEnd:absIdx]
		if !strings.ContainsAny(between, ".;\n") {
			return true
		}

		if sdVerifyIntentPhrases[caveat] {
			return true
		}

	}
	return false
}

func sdHasCaveat(lowerText string) bool {
	for _, caveat := range sdCaveatPhrases {
		if strings.Contains(lowerText, caveat) {
			return true
		}
	}
	return false
}

func itoaSD(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

func (s *successDeclareState) markUndelivered() {
	s.fired = false
	if s.warnCount > 0 {
		s.warnCount--
	}
}
