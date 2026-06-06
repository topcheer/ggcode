package im

import (
	"strings"

	"github.com/topcheer/ggcode/internal/permission"
)

func ParseApprovalReply(text string) (permission.Decision, bool) {
	t := strings.ToLower(strings.TrimSpace(text))
	switch t {
	case "y", "yes", "ok", "好", "好的", "允许", "同意", "确认":
		return permission.Allow, true
	case "a", "always", "总是允许", "总是", "始终允许":
		return permission.Allow, true
	case "n", "no", "nope", "拒绝", "取消", "不要", "deny":
		return permission.Deny, true
	}
	if strings.HasPrefix(t, "y") && len(t) <= 3 {
		return permission.Allow, true
	}
	if strings.HasPrefix(t, "n") && len(t) <= 3 {
		return permission.Deny, true
	}
	return permission.Deny, false
}

func IsApprovalAlwaysReply(text string) bool {
	t := strings.ToLower(strings.TrimSpace(text))
	switch t {
	case "a", "always", "总是允许", "总是", "始终允许":
		return true
	}
	return false
}
