package tui

import "github.com/topcheer/ggcode/internal/permission"

func defaultApprovalOptions() []approvalOption {
	return defaultApprovalOptionsFor(LangEnglish)
}

func defaultApprovalOptionsFor(lang Language) []approvalOption {
	return []approvalOption{
		{label: tr(lang, "approval.allow"), shortcut: "y", decision: permission.Allow},
		{label: tr(lang, "approval.allow_always"), shortcut: "a", decision: permission.Allow},
		{label: tr(lang, "approval.deny"), shortcut: "n", decision: permission.Deny},
	}
}

// diffConfirmOptions returns the options for diff confirmation.
func diffConfirmOptions() []approvalOption {
	return diffConfirmOptionsFor(LangEnglish)
}

func diffConfirmOptionsFor(lang Language) []approvalOption {
	return []approvalOption{
		{label: tr(lang, "approval.accept"), shortcut: "y", decision: permission.Allow},
		{label: tr(lang, "approval.reject"), shortcut: "n", decision: permission.Deny},
	}
}
