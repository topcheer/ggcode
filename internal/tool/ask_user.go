package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
)

const (
	AskUserKindSingle = "single"
	AskUserKindMulti  = "multi"
	AskUserKindText   = "text"

	AskUserStatusSubmitted = "submitted"
	AskUserStatusCancelled = "cancelled"

	AskUserCompletionUnanswered = "unanswered"
	AskUserCompletionPartial    = "partial"
	AskUserCompletionAnswered   = "answered"

	AskUserAnswerModeNone                 = "none"
	AskUserAnswerModeFreeformOnly         = "freeform_only"
	AskUserAnswerModeSelectionOnly        = "selection_only"
	AskUserAnswerModeSelectionAndFreeform = "selection_and_freeform"
)

type AskUserChoice struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

type AskUserQuestion struct {
	ID            string          `json:"id"`
	Title         string          `json:"title"`
	Prompt        string          `json:"prompt"`
	Kind          string          `json:"kind"`
	Choices       []AskUserChoice `json:"choices,omitempty"`
	AllowFreeform bool            `json:"allow_freeform,omitempty"`
	Placeholder   string          `json:"placeholder,omitempty"`
}

type AskUserRequest struct {
	Title     string            `json:"title,omitempty"`
	Questions []AskUserQuestion `json:"questions"`
}

type AskUserAnswer struct {
	ID                string   `json:"id"`
	Title             string   `json:"title"`
	Kind              string   `json:"kind"`
	CompletionStatus  string   `json:"completion_status"`
	AnswerMode        string   `json:"answer_mode"`
	Answered          bool     `json:"answered"`
	SelectedChoiceIDs []string `json:"selected_choice_ids,omitempty"`
	SelectedChoices   []string `json:"selected_choices,omitempty"`
	FreeformText      string   `json:"freeform_text,omitempty"`
}

type AskUserResponse struct {
	Status        string          `json:"status"`
	Title         string          `json:"title,omitempty"`
	QuestionCount int             `json:"question_count"`
	AnsweredCount int             `json:"answered_count"`
	Answers       []AskUserAnswer `json:"answers"`
}

type AskUserHandler func(context.Context, AskUserRequest) (AskUserResponse, error)

type AskUserTool struct {
	mu      sync.RWMutex
	handler AskUserHandler
}

func NewAskUserTool() *AskUserTool {
	return &AskUserTool{}
}

func (t *AskUserTool) Name() string { return "ask_user" }

func (t *AskUserTool) Description() string {
	return "Ask the user one or more structured clarification questions in the interactive TUI and wait for a unified response."
}

func (t *AskUserTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"title": {
				"type": "string",
				"description": "Optional questionnaire title shown above the question tabs."
			},
			"questions": {
				"type": "array",
				"description": "Questions to ask in one batch submission.",
				"minItems": 1,
				"items": {
					"type": "object",
					"properties": {
						"id": {
							"type": "string",
							"description": "Stable identifier for the question."
						},
						"title": {
							"type": "string",
							"description": "Short tab title for the question."
						},
						"prompt": {
							"type": "string",
							"description": "Full prompt shown in the questionnaire body."
						},
						"kind": {
							"type": "string",
							"enum": ["single", "multi", "text"],
							"description": "single = single-select plus optional notes, multi = multi-select plus optional notes, text = freeform only."
						},
						"choices": {
							"type": "array",
							"description": "Choices for single or multi questions.",
							"items": {
								"type": "object",
								"properties": {
									"id": {
										"type": "string"
									},
									"label": {
										"type": "string"
									}
								},
								"required": ["label"]
							}
						},
						"allow_freeform": {
							"type": "boolean",
							"description": "Whether the user may also enter freeform notes. Defaults to true for single and multi questions."
						},
						"placeholder": {
							"type": "string",
							"description": "Optional placeholder for the freeform input."
						}
					},
					"required": ["title", "prompt", "kind"]
				}
			}
		},
		"required": ["questions"]
	}`)
}

func (t *AskUserTool) SetHandler(handler AskUserHandler) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.handler = handler
}

func (t *AskUserTool) Execute(ctx context.Context, input json.RawMessage) (Result, error) {
	var req AskUserRequest
	if err := json.Unmarshal(input, &req); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("invalid input: %v", err)}, nil
	}
	normalized, err := normalizeAskUserRequest(req)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("invalid ask_user request: %v", err)}, nil
	}
	t.mu.RLock()
	handler := t.handler
	t.mu.RUnlock()
	if handler == nil {
		return Result{IsError: true, Content: "ask_user is only available in interactive TUI sessions"}, nil
	}
	resp, err := handler(ctx, normalized)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("ask_user failed: %v", err)}, nil
	}
	if strings.TrimSpace(resp.Status) == "" {
		resp.Status = AskUserStatusSubmitted
	}
	if strings.TrimSpace(resp.Title) == "" {
		resp.Title = normalized.Title
	}
	if resp.QuestionCount == 0 {
		resp.QuestionCount = len(normalized.Questions)
	}
	if resp.AnsweredCount == 0 && len(resp.Answers) > 0 {
		for _, answer := range resp.Answers {
			if answer.Answered {
				resp.AnsweredCount++
			}
		}
	}
	data, err := json.MarshalIndent(resp, "", "  ")
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("marshal ask_user response: %v", err)}, nil
	}
	return Result{Content: string(data)}, nil
}

func normalizeAskUserRequest(req AskUserRequest) (AskUserRequest, error) {
	req.Title = strings.TrimSpace(req.Title)
	if len(req.Questions) == 0 {
		return AskUserRequest{}, fmt.Errorf("at least one question is required")
	}
	seenQuestions := make(map[string]struct{}, len(req.Questions))
	out := AskUserRequest{
		Title:     req.Title,
		Questions: make([]AskUserQuestion, 0, len(req.Questions)),
	}
	for i, raw := range req.Questions {
		q, err := normalizeAskUserQuestion(i, raw)
		if err != nil {
			return AskUserRequest{}, err
		}
		if _, exists := seenQuestions[q.ID]; exists {
			return AskUserRequest{}, fmt.Errorf("duplicate question id %q", q.ID)
		}
		seenQuestions[q.ID] = struct{}{}
		out.Questions = append(out.Questions, q)
	}
	return out, nil
}

func normalizeAskUserQuestion(index int, q AskUserQuestion) (AskUserQuestion, error) {
	q.ID = strings.TrimSpace(q.ID)
	if q.ID == "" {
		q.ID = fmt.Sprintf("question_%d", index+1)
	}
	q.Title = strings.TrimSpace(q.Title)
	q.Prompt = strings.TrimSpace(q.Prompt)
	if q.Title == "" {
		if q.Prompt != "" {
			q.Title = q.Prompt
		} else {
			q.Title = fmt.Sprintf("Question %d", index+1)
		}
	}
	if q.Prompt == "" {
		q.Prompt = q.Title
	}
	kind, err := normalizeAskUserKind(q.Kind)
	if err != nil {
		return AskUserQuestion{}, fmt.Errorf("%s: %w", q.ID, err)
	}
	q.Kind = kind
	q.Placeholder = strings.TrimSpace(q.Placeholder)
	if q.Kind == AskUserKindSingle || q.Kind == AskUserKindMulti {
		if !q.AllowFreeform {
			q.AllowFreeform = true
		}
		if len(q.Choices) == 0 {
			return AskUserQuestion{}, fmt.Errorf("%s: choices are required for %s questions", q.ID, q.Kind)
		}
		seenChoices := make(map[string]struct{}, len(q.Choices))
		choices := make([]AskUserChoice, 0, len(q.Choices))
		for i, rawChoice := range q.Choices {
			choice := AskUserChoice{
				ID:    strings.TrimSpace(rawChoice.ID),
				Label: strings.TrimSpace(rawChoice.Label),
			}
			if choice.Label == "" {
				return AskUserQuestion{}, fmt.Errorf("%s: choice %d label is required", q.ID, i+1)
			}
			if choice.ID == "" {
				choice.ID = fmt.Sprintf("choice_%d", i+1)
			}
			if _, exists := seenChoices[choice.ID]; exists {
				return AskUserQuestion{}, fmt.Errorf("%s: duplicate choice id %q", q.ID, choice.ID)
			}
			seenChoices[choice.ID] = struct{}{}
			choices = append(choices, choice)
		}
		q.Choices = choices
		return q, nil
	}
	q.AllowFreeform = true
	q.Choices = nil
	return q, nil
}

func normalizeAskUserKind(kind string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case AskUserKindSingle, "single_select", "single-select", "single_choice", "single-choice", "single_with_freeform":
		return AskUserKindSingle, nil
	case AskUserKindMulti, "multiple", "multi_select", "multi-select", "multiple_choice", "multiple-choice", "multi_with_freeform":
		return AskUserKindMulti, nil
	case AskUserKindText, "freeform", "text_only", "text-only":
		return AskUserKindText, nil
	default:
		return "", fmt.Errorf("unsupported kind %q", kind)
	}
}
