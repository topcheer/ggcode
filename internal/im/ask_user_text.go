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

	parsed := make([]ParsedQuestionAnswer, len(req.Questions))
	for i, q := range req.Questions {
		selected, freeform, err := ParseRemoteQuestionnaireAnswer(text, q)
		parsed[i] = ParsedQuestionAnswer{
			QuestionIndex: i,
			Selected:      selected,
			Freeform:      freeform,
			Error:         err,
		}
	}
	return BuildAskUserResponse(req, parsed)
}
