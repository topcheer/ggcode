package im

import (
	"fmt"
	"strconv"
	"strings"

	toolpkg "github.com/topcheer/ggcode/internal/tool"
)

// ParseRemoteQuestionnaireAnswer parses a raw IM text reply against a single
// question definition. It returns the selected choice IDs (for single/multi),
// a freeform text (for text or unmatched input), and whether parsing succeeded.
//
// This is the shared implementation used by both TUI and daemon modes.
func ParseRemoteQuestionnaireAnswer(raw string, question toolpkg.AskUserQuestion) (selected map[string]struct{}, freeform string, err error) {
	text := strings.TrimSpace(raw)
	switch question.Kind {
	case toolpkg.AskUserKindText:
		return nil, text, nil
	case toolpkg.AskUserKindSingle, toolpkg.AskUserKindMulti:
		selected, matched, err := parseRemoteSelections(text, question)
		if err != nil {
			return nil, "", err
		}
		if matched {
			return selected, "", nil
		}
		if question.AllowFreeform {
			return nil, text, nil
		}
		return nil, "", fmt.Errorf("reply with a choice number")
	default:
		if question.AllowFreeform {
			return nil, text, nil
		}
		return nil, "", fmt.Errorf("unsupported question kind %q", question.Kind)
	}
}

// ParseMultiQuestionReply splits a multi-line IM reply into per-question
// answers and parses each one. It returns a slice of results in the same
// order as the questions slice. If there are fewer lines than questions,
// remaining questions are left unanswered (empty results).
//
// This is the shared implementation used by both TUI and daemon modes.
func ParseMultiQuestionReply(raw string, questions []toolpkg.AskUserQuestion) []ParsedQuestionAnswer {
	lines := SplitNonEmptyLines(raw)
	results := make([]ParsedQuestionAnswer, len(questions))
	for i, q := range questions {
		if i < len(lines) {
			selected, freeform, err := ParseRemoteQuestionnaireAnswer(lines[i], q)
			results[i] = ParsedQuestionAnswer{
				QuestionIndex: i,
				Selected:      selected,
				Freeform:      freeform,
				Error:         err,
			}
		} else {
			results[i] = ParsedQuestionAnswer{QuestionIndex: i}
		}
	}
	return results
}

// ParsedQuestionAnswer holds the result of parsing a single question's reply.
type ParsedQuestionAnswer struct {
	QuestionIndex int
	Selected      map[string]struct{}
	Freeform      string
	Error         error
}

// SplitNonEmptyLines splits text into non-empty trimmed lines.
func SplitNonEmptyLines(text string) []string {
	var lines []string
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

func BuildAskUserAnswer(question toolpkg.AskUserQuestion, selected map[string]struct{}, freeform string) toolpkg.AskUserAnswer {
	selectedIDs := sortedKeys(selected)
	answer := toolpkg.AskUserAnswer{
		ID:                question.ID,
		Title:             question.Title,
		Kind:              question.Kind,
		CompletionStatus:  toolpkg.AskUserCompletionUnanswered,
		AnswerMode:        toolpkg.AskUserAnswerModeNone,
		Answered:          false,
		SelectedChoiceIDs: selectedIDs,
		SelectedChoices:   labelsForIDs(question.Choices, selected),
		FreeformText:      freeform,
	}

	switch {
	case len(selectedIDs) == 0 && freeform == "":
		answer.AnswerMode = toolpkg.AskUserAnswerModeNone
		answer.CompletionStatus = toolpkg.AskUserCompletionUnanswered
	case len(selectedIDs) == 0 && freeform != "":
		answer.AnswerMode = toolpkg.AskUserAnswerModeFreeformOnly
		if question.Kind == toolpkg.AskUserKindText {
			answer.CompletionStatus = toolpkg.AskUserCompletionAnswered
		} else {
			answer.CompletionStatus = toolpkg.AskUserCompletionPartial
		}
	case len(selectedIDs) > 0 && freeform == "":
		answer.AnswerMode = toolpkg.AskUserAnswerModeSelectionOnly
		answer.CompletionStatus = toolpkg.AskUserCompletionAnswered
	default:
		answer.AnswerMode = toolpkg.AskUserAnswerModeSelectionAndFreeform
		answer.CompletionStatus = toolpkg.AskUserCompletionAnswered
	}
	answer.Answered = answer.CompletionStatus == toolpkg.AskUserCompletionAnswered
	return answer
}

// BuildAskUserResponse builds an AskUserResponse from parsed per-question answers.
// This is the shared implementation used by daemon (and can be used by TUI).
func BuildAskUserResponse(req toolpkg.AskUserRequest, parsed []ParsedQuestionAnswer) toolpkg.AskUserResponse {
	return BuildAskUserResponseWithStatus(req, parsed, toolpkg.AskUserStatusSubmitted)
}

func BuildAskUserResponseWithStatus(req toolpkg.AskUserRequest, parsed []ParsedQuestionAnswer, status string) toolpkg.AskUserResponse {
	resp := toolpkg.AskUserResponse{
		Status:        status,
		Title:         req.Title,
		QuestionCount: len(req.Questions),
		AnsweredCount: 0,
		Answers:       make([]toolpkg.AskUserAnswer, 0, len(req.Questions)),
	}

	for i, q := range req.Questions {
		answer := toolpkg.AskUserAnswer{
			ID:               q.ID,
			Title:            q.Title,
			Kind:             q.Kind,
			CompletionStatus: toolpkg.AskUserCompletionUnanswered,
			AnswerMode:       toolpkg.AskUserAnswerModeNone,
			Answered:         false,
		}

		if i < len(parsed) && parsed[i].Error == nil {
			p := parsed[i]
			answer = BuildAskUserAnswer(q, p.Selected, p.Freeform)
			if answer.Answered {
				resp.AnsweredCount++
			}
		}

		resp.Answers = append(resp.Answers, answer)
	}

	return resp
}

func AnsweredCount(req toolpkg.AskUserRequest, parsed []ParsedQuestionAnswer) int {
	count := 0
	for i, q := range req.Questions {
		if i < len(parsed) && BuildAskUserAnswer(q, parsed[i].Selected, parsed[i].Freeform).Answered {
			count++
		}
	}
	return count
}

func FirstUnansweredQuestionIndex(req toolpkg.AskUserRequest, parsed []ParsedQuestionAnswer) int {
	for i, q := range req.Questions {
		if i >= len(parsed) || !BuildAskUserAnswer(q, parsed[i].Selected, parsed[i].Freeform).Answered {
			return i
		}
	}
	return -1
}

func ApplyRemoteQuestionnaireAnswer(req toolpkg.AskUserRequest, parsed []ParsedQuestionAnswer, raw string) ([]ParsedQuestionAnswer, bool, int, error) {
	text := strings.TrimSpace(raw)
	if text == "" {
		return nil, false, -1, fmt.Errorf("empty answer")
	}
	if len(parsed) < len(req.Questions) {
		next := make([]ParsedQuestionAnswer, len(req.Questions))
		copy(next, parsed)
		for i := len(parsed); i < len(req.Questions); i++ {
			next[i] = ParsedQuestionAnswer{QuestionIndex: i}
		}
		parsed = next
	} else {
		next := make([]ParsedQuestionAnswer, len(parsed))
		copy(next, parsed)
		parsed = next
	}

	unansweredCount := 0
	firstUnanswered := FirstUnansweredQuestionIndex(req, parsed)
	for i, q := range req.Questions {
		if !BuildAskUserAnswer(q, parsed[i].Selected, parsed[i].Freeform).Answered {
			unansweredCount++
		}
	}

	if unansweredCount > 1 {
		rawLines := SplitNonEmptyLines(text)
		if len(rawLines) > 1 && len(rawLines) <= unansweredCount {
			applied := 0
			qi := firstUnanswered
			for _, line := range rawLines {
				for qi < len(req.Questions) {
					if !BuildAskUserAnswer(req.Questions[qi], parsed[qi].Selected, parsed[qi].Freeform).Answered {
						break
					}
					qi++
				}
				if qi >= len(req.Questions) {
					break
				}
				selected, freeform, err := ParseRemoteQuestionnaireAnswer(line, req.Questions[qi])
				if err != nil {
					break
				}
				if selected != nil {
					parsed[qi].Selected = selected
				}
				if freeform != "" || req.Questions[qi].Kind == toolpkg.AskUserKindText || req.Questions[qi].AllowFreeform {
					parsed[qi].Freeform = freeform
				}
				applied++
				qi++
			}
			if applied > 0 {
				nextIdx := FirstUnansweredQuestionIndex(req, parsed)
				return parsed, nextIdx < 0, nextIdx, nil
			}
		}
	}

	idx := firstUnanswered
	if idx < 0 {
		return parsed, true, -1, fmt.Errorf("no active question")
	}
	selected, freeform, err := ParseRemoteQuestionnaireAnswer(text, req.Questions[idx])
	if err != nil {
		return parsed, false, idx, err
	}
	if selected != nil {
		parsed[idx].Selected = selected
	}
	if freeform != "" || req.Questions[idx].Kind == toolpkg.AskUserKindText || req.Questions[idx].AllowFreeform {
		parsed[idx].Freeform = freeform
	}
	nextIdx := FirstUnansweredQuestionIndex(req, parsed)
	return parsed, nextIdx < 0, nextIdx, nil
}

// --- internal helpers ---

func parseRemoteSelections(raw string, question toolpkg.AskUserQuestion) (map[string]struct{}, bool, error) {
	text := strings.TrimSpace(raw)
	if text == "" || len(question.Choices) == 0 {
		return nil, false, nil
	}
	tokens := strings.FieldsFunc(text, func(r rune) bool {
		switch r {
		case ',', '，', '、', ';', '；', '\n', '\t', ' ':
			return true
		default:
			return false
		}
	})
	if len(tokens) == 0 {
		return nil, false, nil
	}

	allNumeric := true
	selected := make(map[string]struct{})
	for _, token := range tokens {
		n, err := strconv.Atoi(token)
		if err != nil {
			allNumeric = false
			break
		}
		if n < 1 || n > len(question.Choices) {
			return nil, false, fmt.Errorf("choice %d is out of range", n)
		}
		selected[question.Choices[n-1].ID] = struct{}{}
	}
	if allNumeric {
		if question.Kind == toolpkg.AskUserKindSingle && len(selected) > 1 {
			return nil, false, fmt.Errorf("pick one choice")
		}
		return selected, true, nil
	}

	needle := normalizeRemoteAnswerToken(text)
	for _, choice := range question.Choices {
		if normalizeRemoteAnswerToken(choice.ID) == needle || normalizeRemoteAnswerToken(choice.Label) == needle {
			return map[string]struct{}{choice.ID: {}}, true, nil
		}
	}
	return nil, false, nil
}

func normalizeRemoteAnswerToken(s string) string {
	s = strings.TrimSpace(strings.ToLower(s))
	s = strings.ReplaceAll(s, " ", "")
	s = strings.ReplaceAll(s, "\n", "")
	s = strings.ReplaceAll(s, "\t", "")
	return s
}

func sortedKeys(m map[string]struct{}) []string {
	if len(m) == 0 {
		return nil
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

func labelsForIDs(choices []toolpkg.AskUserChoice, selected map[string]struct{}) []string {
	if len(selected) == 0 {
		return nil
	}
	labels := make([]string, 0, len(selected))
	for _, c := range choices {
		if _, ok := selected[c.ID]; ok {
			labels = append(labels, c.Label)
		}
	}
	return labels
}
