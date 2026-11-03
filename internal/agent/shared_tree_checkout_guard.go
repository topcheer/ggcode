package agent

import (
	"encoding/json"
)

func (a *Agent) checkoutAdvisory(toolName string, args json.RawMessage) string {
	if toolName != "git_checkout" {
		return ""
	}
	var call struct {
		Path   string `json:"path"`
		Branch string `json:"branch"`
		Create bool   `json:"create"`
	}
	if json.Unmarshal(args, &call) != nil || call.Branch == "" {
		return ""
	}
	dir := call.Path
	if dir == "" {
		dir = a.workingDir
	}
	if dir == "" {
		dir = "."
	}
	if !isSharedMainTree(dir) {
		return ""
	}
	if call.Branch == "main" || call.Branch == "master" {
		return ""
	}
	_ = call.Create
	return "\n[#4805 note] the SHARED main tree is now on branch \"" + call.Branch +
		"\" - every seat's baseline validation in this tree describes that branch " +
		"until it returns to main (#4803 warnings will fire on build/test runs). " +
		"For branch-scoped work prefer enter_worktree; when done here, switch the " +
		"shared tree back to main promptly."
}
