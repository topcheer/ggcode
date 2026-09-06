package im

import toolpkg "github.com/topcheer/ggcode/internal/tool"

func BuildAskUserResponseFromText(req toolpkg.AskUserRequest, text string) toolpkg.AskUserResponse {
	if len(req.Questions) == 1 {
		q := req.Questions[0]
		selected, freeform, err := ParseRemoteQuestionnaireAnswer(text, q)
		parsed := []ParsedQuestionAnswer{{
			QuestionIndex: 0,
			Selected:      selected,
			Freeform:      freeform,
			Error:         err,
		}}
		return BuildAskUserResponse(req, parsed)
	}

	lines := SplitNonEmptyLines(text)
	if len(lines) >= len(req.Questions) {
		parsed := ParseMultiQuestionReply(text, req.Questions)
		allOK := true
		for _, p := range parsed {
			if p.Error != nil {
				allOK = false
				break
			}
		}
		if allOK {
			return BuildAskUserResponse(req, parsed)
		}
	}

	// #1546: the OLD fallback fed the ENTIRE raw text to every question
	// independently - a single "1" reply to a two-choice-question survey
	// selected option 1 on BOTH questions (fabricated answers, no error),
	// and a multi-position misparse retroactively errored an already
	// answered q1 on fallback. The TUI path (ApplyRemoteQuestionnaire-
	// Answer) fills only the FIRST unanswered question; align the
	// broadcast fallback with that semantics - one reply answers one
	// question; the rest stay unanswered for the next exchange.
	parsed := make([]ParsedQuestionAnswer, len(req.Questions))
	for i := range parsed {
		parsed[i] = ParsedQuestionAnswer{QuestionIndex: i}
	}
	first := FirstUnansweredQuestionIndex(req, parsed)
	if first >= 0 && first < len(req.Questions) {
		selected, freeform, err := ParseRemoteQuestionnaireAnswer(text, req.Questions[first])
		parsed[first] = ParsedQuestionAnswer{
			QuestionIndex: first,
			Selected:      selected,
			Freeform:      freeform,
			Error:         err,
		}
	}
	return BuildAskUserResponse(req, parsed)
}
