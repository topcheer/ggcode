package tui

import "strings"

func formatUserFacingError(lang Language, err error) string {
	if err == nil {
		if lang == LangZhCN {
			return "错误"
		}
		return "Error"
	}
	raw := strings.TrimSpace(err.Error())
	trimmedChat := strings.TrimSpace(strings.TrimPrefix(raw, "chat error:"))

	switch {
	case isAnthropicSerializationError(raw):
		if lang == LangZhCN {
			return "Error: 发送给 Anthropic 之前，请求序列化失败。当前上下文里有一个 ggcode 生成的内容块无法被编码。可以先尝试 /compact 后重试；如果持续出现，这更像是 ggcode 的兼容性问题。"
		}
		return "Error: Request serialization failed before sending to Anthropic. The current conversation contains a ggcode-generated content block that could not be encoded. Try /compact and retry; if it keeps happening, this is likely a ggcode compatibility bug."
	case trimmedChat != raw:
		if lang == LangZhCN {
			return "Error: 模型请求失败：" + trimmedChat
		}
		return "Error: Model request failed: " + trimmedChat
	default:
		return "Error: " + raw
	}
}

func isAnthropicSerializationError(raw string) bool {
	if raw == "" {
		return false
	}
	return strings.Contains(raw, "MarshalJSON") &&
		(strings.Contains(raw, "anthropic.MessageParam") ||
			strings.Contains(raw, "anthropic.ContentBlockParamUnion"))
}
