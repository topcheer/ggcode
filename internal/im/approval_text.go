package im

import "fmt"

func FormatApprovalRequest(lang ToolLanguage, toolName, rawArgs string) string {
	present := DescribeTool(lang, toolName, rawArgs)
	detail := FormatToolInline(present.DisplayName, present.Detail)
	switch lang {
	case ToolLangZhCN:
		return fmt.Sprintf("🔒 需要审批: %s\n\n回复 y 允许 · a 总是允许 · n 拒绝", detail)
	default:
		return fmt.Sprintf("🔒 Approval required: %s\n\nReply y allow · a always allow · n deny", detail)
	}
}

func FormatApprovalResult(lang ToolLanguage, toolName, decision string) string {
	present := DescribeTool(lang, toolName, "")
	detail := FormatToolInline(present.DisplayName, present.Detail)
	switch lang {
	case ToolLangZhCN:
		switch decision {
		case "allow":
			return fmt.Sprintf("✅ 已允许: %s", detail)
		case "always":
			return fmt.Sprintf("✅ 已总是允许: %s", detail)
		case "deny":
			return fmt.Sprintf("❌ 已拒绝: %s", detail)
		}
	default:
		switch decision {
		case "allow":
			return fmt.Sprintf("✅ Allowed: %s", detail)
		case "always":
			return fmt.Sprintf("✅ Always allowed: %s", detail)
		case "deny":
			return fmt.Sprintf("❌ Denied: %s", detail)
		}
	}
	return ""
}
