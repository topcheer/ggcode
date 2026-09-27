package im

import (
	"fmt"
	"strings"

	toolpkg "github.com/topcheer/ggcode/internal/tool"
)

// FormatAskUserPrompt formats an AskUserRequest into an IM-friendly prompt
// with per-question-type reply guidance. Shared by TUI and daemon modes.
func FormatAskUserPrompt(lang string, req toolpkg.AskUserRequest) string {
	multiQuestion := len(req.Questions) > 1
	lines := make([]string, 0, 16+len(req.Questions)*4)

	// Title
	title := strings.TrimSpace(req.Title)
	switch lang {
	case "zh-CN":
		if title != "" {
			lines = append(lines, "📋 **"+title+"**")
		} else {
			lines = append(lines, "📋 **需要补充信息**")
		}
	default:
		if title != "" {
			lines = append(lines, "📋 **"+title+"**")
		} else {
			lines = append(lines, "📋 **Input needed**")
		}
	}

	// Questions
	for idx, question := range req.Questions {
		qLines := formatQuestionBlock(lang, idx, question, multiQuestion)
		lines = append(lines, qLines...)
	}

	// Reply instructions
	lines = append(lines, "")
	lines = append(lines, formatReplyInstructions(lang, req.Questions, multiQuestion))

	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func formatQuestionBlock(lang string, idx int, q toolpkg.AskUserQuestion, multiQuestion bool) []string {
	prompt := strings.TrimSpace(firstNonEmptyStr(q.Prompt, q.Title))
	if prompt == "" {
		return nil
	}

	var lines []string

	// Question header
	if multiQuestion {
		lines = append(lines, fmt.Sprintf("**%d. %s**", idx+1, prompt))
	} else {
		lines = append(lines, fmt.Sprintf("**%s**", prompt))
	}

	// Choices with numbered labels
	for ci, choice := range q.Choices {
		label := strings.TrimSpace(choice.Label)
		if label == "" {
			continue
		}
		if multiQuestion {
			lines = append(lines, fmt.Sprintf("  %d%c. %s", idx+1, 'a'+ci, label))
		} else {
			lines = append(lines, fmt.Sprintf("  %d. %s", ci+1, label))
		}
	}

	// Question-type hint
	switch q.Kind {
	case toolpkg.AskUserKindText:
		placeholder := strings.TrimSpace(q.Placeholder)
		if placeholder != "" {
			switch lang {
			case "zh-CN":
				lines = append(lines, fmt.Sprintf("  _（输入文本，例如：%s）_", placeholder))
			default:
				lines = append(lines, fmt.Sprintf("  _(type text, e.g. %s)_", placeholder))
			}
		} else {
			switch lang {
			case "zh-CN":
				lines = append(lines, "  _（输入文本）_")
			default:
				lines = append(lines, "  _(type text)_")
			}
		}
	case toolpkg.AskUserKindSingle:
		if len(q.Choices) > 0 {
			switch lang {
			case "zh-CN":
				hint := fmt.Sprintf("  _（回复编号 %d-%d 或选项文本", 1, len(q.Choices))
				if q.AllowFreeform {
					hint += "，也可以直接输入其他内容"
				}
				hint += "）_"
				lines = append(lines, hint)
			default:
				hint := fmt.Sprintf("  _(reply %d-%d or option text", 1, len(q.Choices))
				if q.AllowFreeform {
					hint += ", or type freely"
				}
				hint += ")_"
				lines = append(lines, hint)
			}
		}
	case toolpkg.AskUserKindMulti:
		if len(q.Choices) > 0 {
			switch lang {
			case "zh-CN":
				hint := fmt.Sprintf("  _（可多选，回复编号如 \"%d,%d\" 或选项文本", 1, min(3, len(q.Choices)))
				if q.AllowFreeform {
					hint += "，也可以输入其他内容"
				}
				hint += "）_"
				lines = append(lines, hint)
			default:
				hint := fmt.Sprintf("  _(select multiple, e.g. \"%d,%d\" or option text", 1, min(3, len(q.Choices)))
				if q.AllowFreeform {
					hint += ", or type freely"
				}
				hint += ")_"
				lines = append(lines, hint)
			}
		}
	}

	return lines
}

func formatReplyInstructions(lang string, questions []toolpkg.AskUserQuestion, multiQuestion bool) string {
	if len(questions) == 0 {
		return ""
	}
	if !multiQuestion {
		return singleQuestionReplyHint(lang, questions[0])
	}
	return multiQuestionReplyInstructions(lang, questions)
}

// singleQuestionReplyHint renders the one-line reply guidance for a
// single-question prompt.
func singleQuestionReplyHint(lang string, q toolpkg.AskUserQuestion) string {
	switch lang {
	case "zh-CN":
		switch q.Kind {
		case toolpkg.AskUserKindText:
			return "💬 直接回复文本即可。"
		case toolpkg.AskUserKindSingle:
			return "💬 回复编号或选项文本。"
		case toolpkg.AskUserKindMulti:
			return "💬 回复多个编号（用逗号或空格分隔）或选项文本。"
		}
	default:
		switch q.Kind {
		case toolpkg.AskUserKindText:
			return "💬 Just reply with your text."
		case toolpkg.AskUserKindSingle:
			return "💬 Reply with the number or option text."
		case toolpkg.AskUserKindMulti:
			return "💬 Reply with multiple numbers (comma or space separated) or option text."
		}
	}
	return ""
}

// multiQuestionReplyInstructions renders the structured multi-question reply
// guidance: a header plus one example line per question.
func multiQuestionReplyInstructions(lang string, questions []toolpkg.AskUserQuestion) string {
	result := multiQuestionReplyHeader(lang, len(questions))
	for i, q := range questions {
		result += multiQuestionExampleLine(lang, i, q)
	}
	return strings.TrimSpace(result)
}

// multiQuestionReplyHeader renders the reply-format header; two-question
// prompts mention blank-line separation.
func multiQuestionReplyHeader(lang string, count int) string {
	switch lang {
	case "zh-CN":
		if count == 2 {
			return "💬 **回复格式：**\n每行回答一个问题，或用空行分隔。例如：\n"
		}
		return "💬 **回复格式：**\n按顺序逐行回答，每行对应一个问题。例如：\n"
	default:
		if count == 2 {
			return "💬 **Reply format:**\nAnswer one question per line, or separate with blank lines. Example:\n"
		}
		return "💬 **Reply format:**\nAnswer in order, one per line. Example:\n"
	}
}

// multiQuestionExampleLine renders the example reply for one question slot.
func multiQuestionExampleLine(lang string, i int, q toolpkg.AskUserQuestion) string {
	switch q.Kind {
	case toolpkg.AskUserKindSingle:
		return fmt.Sprintf("> %d\n", 1)
	case toolpkg.AskUserKindMulti:
		return fmt.Sprintf("> %d,%d\n", 1, 2)
	case toolpkg.AskUserKindText:
		return multiQuestionTextExample(lang, i)
	}
	return ""
}

// multiQuestionTextExample renders the freeform-text example for the i-th slot.
func multiQuestionTextExample(lang string, i int) string {
	switch lang {
	case "zh-CN":
		switch i {
		case 0:
			return "> 我的答案\n"
		case 1:
			return "> 另一个回答\n"
		default:
			return fmt.Sprintf("> 第%d个回答\n", i+1)
		}
	default:
		switch i {
		case 0:
			return "> my answer\n"
		case 1:
			return "> another answer\n"
		default:
			return fmt.Sprintf("> answer %d\n", i+1)
		}
	}
}
