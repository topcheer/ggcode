package agent

import (
	"encoding/json"
)

// Shared-tree checkout guard (#4805 / S-38).
//
// #4803 (S-35) warns when a VALIDATION command runs on a non-main branch of
// the shared main tree - after the fact, once per run. S-38 covers the other
// half: the moment a seat CHECKS OUT a non-main branch in the shared tree,
// every other seat's baseline silently shifts for the branch's whole
// lifetime. This guard appends an advisory to the git_checkout result so the
// acting seat sees the fleet-wide consequence at switch time and is pointed
// at enter_worktree; it also names the branch so patrol logs can attribute
// the dwell later. Advisory-only: blocking would break legitimate shared-
// tree flows (release reverts, hotfix rounds that deliberately use the main
// tree). Worktrees are exempt (they exist to be on fix branches).
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
		return "" // returning TO the trunk is exactly what we want
	}
	_ = call.Create // advisory text is identical for switch and create
	return "\n[#4805 note] the SHARED main tree is now on branch \"" + call.Branch +
		"\" - every seat's baseline validation in this tree describes that branch " +
		"until it returns to main (#4803 warnings will fire on build/test runs). " +
		"For branch-scoped work prefer enter_worktree; when done here, switch the " +
		"shared tree back to main promptly."
}
