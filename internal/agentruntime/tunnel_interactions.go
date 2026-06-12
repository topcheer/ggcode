package agentruntime

import (
	"strings"

	"github.com/topcheer/ggcode/internal/permission"
	"github.com/topcheer/ggcode/internal/tool"
	"github.com/topcheer/ggcode/internal/tunnel"
)

func BuildTunnelAskUserQuestions(req tool.AskUserRequest) []tunnel.AskUserQuestion {
	questions := make([]tunnel.AskUserQuestion, len(req.Questions))
	for i, q := range req.Questions {
		choices := make([]tunnel.AskUserChoice, len(q.Choices))
		for j, c := range q.Choices {
			choices[j] = tunnel.AskUserChoice{ID: c.ID, Label: c.Label}
		}
		questions[i] = tunnel.AskUserQuestion{
			ID:            q.ID,
			Prompt:        q.Prompt,
			Kind:          q.Kind,
			Choices:       choices,
			AllowFreeform: q.AllowFreeform,
			Placeholder:   q.Placeholder,
		}
	}
	return questions
}

func BuildTunnelAskUserAnswers(resp tool.AskUserResponse) []tunnel.AskUserAnswer {
	answers := make([]tunnel.AskUserAnswer, len(resp.Answers))
	for i, answer := range resp.Answers {
		answers[i] = tunnel.AskUserAnswer{
			QuestionID:   answer.ID,
			ChoiceIDs:    append([]string(nil), answer.SelectedChoiceIDs...),
			FreeformText: answer.FreeformText,
		}
	}
	return answers
}

func BuildAskUserResponseFromTunnel(req tool.AskUserRequest, status string, answers []tunnel.AskUserAnswer) tool.AskUserResponse {
	normalizedStatus := strings.TrimSpace(status)
	if normalizedStatus == "" {
		normalizedStatus = tool.AskUserStatusSubmitted
	}
	answerByQuestion := make(map[string]tunnel.AskUserAnswer, len(answers))
	for _, answer := range answers {
		answerByQuestion[answer.QuestionID] = answer
	}
	out := tool.AskUserResponse{
		Status:        normalizedStatus,
		Title:         req.Title,
		QuestionCount: len(req.Questions),
		Answers:       make([]tool.AskUserAnswer, 0, len(req.Questions)),
	}
	for _, question := range req.Questions {
		raw := answerByQuestion[question.ID]
		answer := BuildAskUserAnswer(question, raw.ChoiceIDs, raw.FreeformText)
		if answer.Answered {
			out.AnsweredCount++
		}
		out.Answers = append(out.Answers, answer)
	}
	return out
}

func BuildAskUserAnswer(question tool.AskUserQuestion, selectedIDs []string, freeform string) tool.AskUserAnswer {
	selectedSet := make(map[string]struct{}, len(selectedIDs))
	for _, id := range selectedIDs {
		selectedSet[id] = struct{}{}
	}
	orderedIDs := make([]string, 0, len(selectedSet))
	orderedLabels := make([]string, 0, len(selectedSet))
	for _, choice := range question.Choices {
		if _, ok := selectedSet[choice.ID]; ok {
			orderedIDs = append(orderedIDs, choice.ID)
			orderedLabels = append(orderedLabels, choice.Label)
		}
	}
	freeform = strings.TrimSpace(freeform)
	answerMode := tool.AskUserAnswerModeNone
	completionStatus := tool.AskUserCompletionUnanswered
	switch {
	case len(orderedIDs) == 0 && freeform == "":
		answerMode = tool.AskUserAnswerModeNone
		completionStatus = tool.AskUserCompletionUnanswered
	case len(orderedIDs) == 0 && freeform != "":
		answerMode = tool.AskUserAnswerModeFreeformOnly
		if question.Kind == tool.AskUserKindText {
			completionStatus = tool.AskUserCompletionAnswered
		} else {
			completionStatus = tool.AskUserCompletionPartial
		}
	case len(orderedIDs) > 0 && freeform == "":
		answerMode = tool.AskUserAnswerModeSelectionOnly
		completionStatus = tool.AskUserCompletionAnswered
	default:
		answerMode = tool.AskUserAnswerModeSelectionAndFreeform
		completionStatus = tool.AskUserCompletionAnswered
	}
	return tool.AskUserAnswer{
		ID:                question.ID,
		Title:             question.Title,
		Kind:              question.Kind,
		CompletionStatus:  completionStatus,
		AnswerMode:        answerMode,
		Answered:          completionStatus == tool.AskUserCompletionAnswered,
		SelectedChoiceIDs: orderedIDs,
		SelectedChoices:   orderedLabels,
		FreeformText:      freeform,
	}
}

func ApprovalDecisionFromTunnel(decision string) permission.Decision {
	switch decision {
	case tunnel.DecisionAllow, tunnel.DecisionAlwaysAllow, "always":
		return permission.Allow
	default:
		return permission.Deny
	}
}

func TunnelDecisionFromApproval(decision permission.Decision) string {
	switch decision {
	case permission.Allow:
		return tunnel.DecisionAllow
	case permission.Deny:
		return tunnel.DecisionDeny
	default:
		return decision.String()
	}
}
