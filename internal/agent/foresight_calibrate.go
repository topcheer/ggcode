package agent

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/provider"
)

var foresightPredictionRe = regexp.MustCompile(`(?i)(?:this\s+(?:should|will|should)\s+(?:contain|return|show|reveal|have|include)|the\s+(?:test|build|command|file|function|api|response)\s+(?:should|will|must|might|may)\s+(?:pass|fail|succeed|work|return|contain|show|be)|i\s+expect\s+(?:this|the|it)\s+to|this\s+will\s+(?:likely\s+)?(?:pass|fail|work|succeed|return|contain)|the\s+output\s+(?:should|will)\s+be|should\s+(?:now\s+)?(?:pass|work|succeed|compile|build)|(?:should|will|might|may)\s+(?:it\s+to\s+)?be\s+empty|(?:might|may)\s+(?:it\s+to\s+)?(?:fail|error|throw|crash|not\s+(?:work|exist|be\s+found|compile|pass))|probably\s+(?:returns?|contains?|is)|likely\s+(?:returns?|contains?|is|will))`)

var foresightSuccessPredRe = regexp.MustCompile(`(?i)(?:should|will|must|expect)\s+(?:it\s+to\s+)?(?:pass|succeed|work|compile|build\s+successfully|return\s+(?:the\s+)?(?:expected\s+)?(?:value|result|data|content|json)|contain\s+(?:the\s+)?(?:config|definition|function|key|field)|be\s+(?:present|found|there|valid|correct))|will\s+(?:likely\s+)?(?:pass|succeed|work)`)

var foresightEmptyPredRe = regexp.MustCompile(`(?i)(?:should|will|must|expect)\s+(?:it\s+to\s+)?be\s+empty|expect\s+(?:this|the|it)\s+to\s+return\s+no\s+(?:results?|matches?)`)

var foresightFailurePredRe = regexp.MustCompile(`(?i)(?:should|will|might|may|expect)\s+(?:it\s+to\s+)?(?:fail|error|not\s+(?:work|exist|be\s+found|compile|pass)|throw|crash|return\s+(?:an?\s+)?error|be\s+(?:missing|absent|null|undefined))`)

var foresightResultFailureRe = regexp.MustCompile(`(?i)(?:exit\s+code\s+[1-9]|exit\s+status\s+[1-9]|status[:\s]+(?:4\d\d|5\d\d)|panic:|fatal:|traceback\s+\(|compilation\s+failed|\bIsError\b|tool_result_error)`)

var foresightResultEmptyRe = regexp.MustCompile(`(?i)^(?:$|no\s+(?:results?|matches?|content|output|data)\s+found$|(?:is\s+)?empty$|0\s+(?:bytes|lines|matches|results)$|nothing\s+(?:found|returned|matched)$)`)

func foresightResultEmpty(content string) bool {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return true
	}
	if len(trimmed) > 80 {

		return false
	}
	return foresightResultEmptyRe.MatchString(trimmed)
}

type foresightPrediction struct {
	iteration      int
	predictedOK    bool
	predictedEmpty bool
	snippet        string
	toolName       string
	expectCheck    string
}

type foresightCalibrateState struct {
	predictions    []foresightPrediction
	mismatches     int
	warnCount      int
	expectWarns    int
	protocolTaught bool
}

func newForesightCalibrateState() *foresightCalibrateState {
	return &foresightCalibrateState{}
}

func (s *foresightCalibrateState) recordPrediction(assistantText string, toolCalls []provider.ToolCallDelta, iteration int) {
	if s == nil || len(toolCalls) == 0 {
		return
	}

	if decls := parseExpectDeclarations(assistantText); len(decls) > 0 {
		called := make(map[string]bool, len(toolCalls))
		for _, tc := range toolCalls {
			called[tc.Name] = true
		}

		recorded := 0
		for _, d := range decls {
			if d.toolName != "" {
				if !called[d.toolName] {
					continue
				}
				s.recordExpect(d.toolName, d.check, iteration)
				recorded++
			} else if len(toolCalls) == 1 {
				s.recordExpect(toolCalls[0].Name, d.check, iteration)
				recorded++
			}
		}
		if recorded > 0 {
			return
		}
	}

	if !foresightPredictionRe.MatchString(assistantText) {
		return
	}

	var predictedOK bool
	var predictedEmpty bool
	hasSuccess := foresightSuccessPredRe.MatchString(assistantText)
	hasFailure := foresightFailurePredRe.MatchString(assistantText)
	hasEmpty := foresightEmptyPredRe.MatchString(assistantText)
	if hasEmpty && hasFailure {

		return
	}
	if hasEmpty && !hasFailure {

		predictedOK = true
		predictedEmpty = true
	} else if hasSuccess && !hasFailure {
		predictedOK = true
	} else if hasFailure && !hasSuccess {
		predictedOK = false
	} else {

		return
	}

	if len(toolCalls) > 1 {
		return
	}
	snippet := extractForesightSnippet(assistantText)
	s.predictions = append(s.predictions, foresightPrediction{
		iteration:      iteration,
		predictedOK:    predictedOK,
		predictedEmpty: predictedEmpty,
		snippet:        snippet,
		toolName:       toolCalls[0].Name,
	})
	s.capPending()
}

func (s *foresightCalibrateState) recordExpect(toolName, check string, iteration int) {
	s.predictions = append(s.predictions, foresightPrediction{
		iteration:   iteration,
		predictedOK: expectCheckPositive(check),
		snippet:     "EXPECT: " + toolName + " " + check,
		toolName:    toolName,
		expectCheck: check,
	})
	s.capPending()
}

func (s *foresightCalibrateState) capPending() {

	if len(s.predictions) > 30 {
		s.predictions = s.predictions[len(s.predictions)-30:]
	}
}

func extractForesightSnippet(text string) string {
	loc := foresightPredictionRe.FindStringIndex(text)
	if loc == nil {
		return ""
	}

	start := loc[0]
	end := loc[1]

	for start > 0 && text[start-1] != '.' && text[start-1] != '\n' && start > loc[0]-80 {
		start--
	}

	for end < len(text) && text[end] != '.' && text[end] != '\n' && end < loc[1]+80 {
		end++
	}
	snippet := strings.TrimSpace(text[start:end])
	if len(snippet) > 120 {

		runes := []rune(snippet)
		cut := 117
		if cut > len(runes) {
			cut = len(runes)
		}
		snippet = string(runes[:cut]) + "..."
	}
	return snippet
}

func (s *foresightCalibrateState) checkCalibration(toolName string, resultContent string, isError bool, iteration int) string {
	if s == nil || len(s.predictions) == 0 {
		return ""
	}

	{
		kept := s.predictions[:0]
		for _, p := range s.predictions {
			if iteration-p.iteration <= 1 {
				kept = append(kept, p)
			}
		}
		s.predictions = kept
	}
	if len(s.predictions) == 0 {
		return ""
	}

	matched := -1
	fallback := -1
	for i, p := range s.predictions {
		if p.iteration == iteration && (p.toolName == toolName || p.toolName == "") {
			matched = i
			break
		}
		if p.toolName == toolName && fallback < 0 && iteration-p.iteration <= 1 {

			fallback = i
		}
	}
	if matched < 0 {
		matched = fallback
	}
	if matched < 0 {
		return ""
	}

	pred := s.predictions[matched]

	s.predictions = append(s.predictions[:matched], s.predictions[matched+1:]...)

	if pred.expectCheck != "" {
		passed, detail := evaluateExpectCheck(pred.expectCheck, resultContent, isError)
		if passed {
			return ""
		}
		if !expectCheckPositive(pred.expectCheck) {
			debug.Log("expect-declare", "Iteration %d: declared-fail check unexpectedly passed (%s)", iteration, pred.snippet)
			return ""
		}
		if s.expectWarns >= 4 {
			return ""
		}
		s.expectWarns++
		debug.Log("expect-declare", "Iteration %d: declared expectation violated: %s", iteration, detail)
		return fmt.Sprintf(
			"[expectation] DECLARED \"%s\" but the actual result contradicts it: %s. Do NOT build on this step as if it succeeded -- re-read the result above and reconcile your expectation before proceeding.",
			pred.snippet, detail,
		)
	}

	actualOK := !isError && !foresightResultFailureRe.MatchString(resultContent)
	actualEmpty := foresightResultEmpty(resultContent)

	mismatch := false
	mismatchDetail := ""

	if !actualOK {
		if pred.predictedOK {

			mismatch = true
			mismatchDetail = "predicted success but the result indicates failure"
		}
	} else if pred.predictedEmpty {

		if !actualEmpty {
			mismatch = true
			mismatchDetail = "predicted the result to be empty but it has content"
		}
	} else if pred.predictedOK && actualEmpty {

		mismatch = true
		mismatchDetail = "predicted content but the result is empty or not found"
	} else if !pred.predictedOK && !actualEmpty {

		mismatch = true
		mismatchDetail = "predicted failure but the result succeeded"
	}

	if mismatch {
		s.mismatches++
		debug.Log("foresight-calibrate", "Iteration %d: prediction-observation mismatch #%d (%s)", iteration, s.mismatches, mismatchDetail)

		if s.mismatches >= 3 && s.warnCount < 2 {
			s.warnCount++
			return fmt.Sprintf(
				"[foresight] Predictions wrong %d times (e.g., \"%s\" -- %s). Verify assumptions by reading actual state.",
				s.mismatches, pred.snippet, mismatchDetail,
			)
		}
	}

	return ""
}

func (s *foresightCalibrateState) reset() {
	if s == nil {
		return
	}
	s.predictions = nil
	s.mismatches = 0
	s.warnCount = 0
	s.expectWarns = 0
	s.protocolTaught = false
}
