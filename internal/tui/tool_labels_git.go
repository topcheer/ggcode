package tui

import "strings"

// describeGitFamilyTool renders the read-only git inspection tools plus
// add/commit/stash actions.
func describeGitFamilyTool(lang Language, toolName string, args map[string]any, fileTarget string) toolPresentation {
	switch toolName {
	case "git_status":
		statusTarget := displayToolFileTarget(argString(args, "path"))
		return toolPresentationFor(lang, "inspect", statusTarget)
	case "git_diff":
		detail := ""
		if argString(args, "cached") == "true" {
			detail = "--cached"
		}
		if f := argString(args, "file"); f != "" {
			if detail != "" {
				detail += " "
			}
			detail += displayToolFileTarget(f)
		}
		return toolPresentationFor(lang, "diff", detail)
	case "git_log":
		return toolPresentationFor(lang, "log", "")
	case "git_show":
		showTarget := displayToolTarget(argString(args, "revision"))
		return toolPresentationFor(lang, "show", showTarget)
	case "git_blame":
		blameTarget := displayToolFileTarget(argString(args, "file"))
		return toolPresentationFor(lang, "blame", blameTarget)
	case "git_branch_list":
		detail := ""
		if argString(args, "remote") == "true" {
			detail = "--remote"
		}
		return toolPresentationFor(lang, "branches", detail)
	case "git_remote":
		return toolPresentationFor(lang, "remote", "")
	case "git_stash_list":
		return toolPresentationFor(lang, "stash", "list")
	case "git_add":
		files := parseStringSlice(args, "files")
		stageTarget := displayToolFileTarget(strings.Join(files, ", "))
		return toolPresentationFor(lang, "stage", stageTarget)
	case "git_commit":
		commitDetail := compactSingleLine(argString(args, "message"))
		return toolPresentationFor(lang, "commit", commitDetail)
	case "git_stash":
		action := argString(args, "action")
		if action == "" {
			action = "push"
		}
		return toolPresentationFor(lang, "stash", action)
	default:
		return describeUnknownFamilyTool(lang, toolName, args, fileTarget)
	}
}
