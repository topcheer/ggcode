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

// BuildAskUserResponse builds an AskUserResponse from parsed per-question answers.
// This is the shared implementation used by daemon (and can be used by TUI).
func BuildAskUserResponse(req toolpkg.AskUserRequest, parsed []ParsedQuestionAnswer) toolpkg.AskUserResponse {
	resp := toolpkg.AskUserResponse{
		Status:        toolpkg.AskUserStatusSubmitted,
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
			answer.Answered = true
			answer.CompletionStatus = toolpkg.AskUserCompletionAnswered
			answer.SelectedChoiceIDs = sortedKeys(p.Selected)
			answer.SelectedChoices = labelsForIDs(q.Choices, p.Selected)

			if len(p.Selected) > 0 && p.Freeform != "" {
				answer.AnswerMode = toolpkg.AskUserAnswerModeSelectionAndFreeform
				answer.FreeformText = p.Freeform
			} else if len(p.Selected) > 0 {
				answer.AnswerMode = toolpkg.AskUserAnswerModeSelectionOnly
			} else if p.Freeform != "" || q.Kind == toolpkg.AskUserKindText {
				answer.AnswerMode = toolpkg.AskUserAnswerModeFreeformOnly
				answer.FreeformText = p.Freeform
			}

			resp.AnsweredCount++
		}

		resp.Answers = append(resp.Answers, answer)
	}

	return resp
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
