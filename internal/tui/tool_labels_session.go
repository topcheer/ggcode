package tui

import runewidth "github.com/mattn/go-runewidth"

// describeSessionFamilyTool renders worktree/memory/config/messaging and
// plan-mode lifecycle tools.
func describeSessionFamilyTool(lang Language, toolName string, args map[string]any, fileTarget string) toolPresentation {
	switch toolName {
	case "enter_worktree":
		name := argString(args, "name")
		return toolPresentation{
			DisplayName: localizedToolLabel(lang, "enter_worktree"),
			Detail:      name,
			Activity:    localizedToolActivity(lang, "enter_worktree", name),
		}
	case "exit_worktree":
		action := argString(args, "action")
		return toolPresentation{
			DisplayName: localizedToolLabel(lang, "exit_worktree"),
			Detail:      action,
			Activity:    localizedToolActivity(lang, "exit_worktree", action),
		}
	case "save_memory":
		key := argString(args, "key")
		return toolPresentation{
			DisplayName: localizedToolLabel(lang, "save_memory"),
			Detail:      key,
			Activity:    localizedToolActivity(lang, "save_memory", key),
		}
	case "delete_memory":
		key := argString(args, "key")
		return toolPresentation{
			DisplayName: localizedToolLabel(lang, "delete_memory"),
			Detail:      key,
			Activity:    localizedToolActivity(lang, "delete_memory", key),
		}
	case "config":
		setting := argString(args, "setting")
		value := argString(args, "value")
		detail := setting
		if value != "" {
			detail = setting + " = " + value
		}
		return toolPresentation{
			DisplayName: localizedToolLabel(lang, "config"),
			Detail:      detail,
			Activity:    localizedToolActivity(lang, "config", detail),
		}
	case "send_message":
		to := argString(args, "to")
		msg := argString(args, "message")
		detail := to
		if msg != "" {
			if runewidth.StringWidth(msg) > 40 {
				runes := []rune(msg)
				w := 0
				cut := len(runes)
				for i, r := range runes {
					rw := runewidth.RuneWidth(r)
					if w+rw > 37 {
						cut = i
						break
					}
					w += rw
				}
				msg = string(runes[:cut]) + "…"
			}
			detail = to + ": " + msg
		}
		return toolPresentation{
			DisplayName: localizedToolLabel(lang, "send_message"),
			Detail:      detail,
			Activity:    localizedToolActivity(lang, "send_message", to),
		}
	case "enter_plan_mode":
		return toolPresentation{
			DisplayName: localizedToolLabel(lang, "enter_plan"),
			Detail:      "",
			Activity:    localizedToolActivity(lang, "enter_plan", ""),
		}
	case "exit_plan_mode":
		return toolPresentation{
			DisplayName: localizedToolLabel(lang, "exit_plan"),
			Detail:      "",
			Activity:    localizedToolActivity(lang, "exit_plan", ""),
		}
	default:
		return describeUnknownFamilyTool(lang, toolName, args, fileTarget)
	}
}
