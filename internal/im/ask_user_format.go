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
		q := questions[0]
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

	// Multi-question: provide structured reply guidance
	switch lang {
	case "zh-CN":
		result := "💬 **回复格式：**\n"
		if len(questions) == 2 {
			result += "每行回答一个问题，或用空行分隔。例如：\n"
		} else {
			result += "按顺序逐行回答，每行对应一个问题。例如：\n"
		}
		for i, q := range questions {
			switch q.Kind {
			case toolpkg.AskUserKindSingle:
				result += fmt.Sprintf("> %d\n", 1)
			case toolpkg.AskUserKindMulti:
				result += fmt.Sprintf("> %d,%d\n", 1, 2)
			case toolpkg.AskUserKindText:
				switch i {
				case 0:
					result += "> 我的答案\n"
				case 1:
					result += "> 另一个回答\n"
				default:
					result += fmt.Sprintf("> 第%d个回答\n", i+1)
				}
			}
		}
		return strings.TrimSpace(result)
	default:
		result := "💬 **Reply format:**\n"
		if len(questions) == 2 {
			result += "Answer one question per line, or separate with blank lines. Example:\n"
		} else {
			result += "Answer in order, one per line. Example:\n"
		}
		for i, q := range questions {
			switch q.Kind {
			case toolpkg.AskUserKindSingle:
				result += fmt.Sprintf("> %d\n", 1)
			case toolpkg.AskUserKindMulti:
				result += fmt.Sprintf("> %d,%d\n", 1, 2)
			case toolpkg.AskUserKindText:
				switch i {
				case 0:
					result += "> my answer\n"
				case 1:
					result += "> another answer\n"
				default:
					result += fmt.Sprintf("> answer %d\n", i+1)
				}
			}
		}
		return strings.TrimSpace(result)
	}
}
