package agent

import (
	"encoding/json"
	"os/exec"
	"strings"
	"sync"

	"github.com/topcheer/ggcode/internal/debug"
)

var (
	sharedTreeWarnedBranch string
	sharedTreeBranchMu     sync.Mutex
)

func sharedTreeCurrentBranch(dir string) string {
	out, err := exec.Command("git", "-C", dir, "branch", "--show-current").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func resetSharedTreeBranchGuard() {
	sharedTreeBranchMu.Lock()
	sharedTreeWarnedBranch = ""
	sharedTreeBranchMu.Unlock()
}

func isSharedMainTree(dir string) bool {
	dir = strings.TrimSuffix(dir, "/")
	return !strings.Contains(dir, "/.ggcode/worktrees/")
}

func (a *Agent) checkSharedTreeBranchGuard(toolName string, args json.RawMessage, resultContent string) string {
	if toolName != "run_command" || strings.Contains(resultContent, "IsError") {
		return ""
	}
	var call struct {
		Command string `json:"command"`
	}
	if json.Unmarshal(args, &call) != nil || call.Command == "" {
		return ""
	}
	if isBuild, _, _ := detectBuildTestCommand(call.Command); !isBuild {
		return ""
	}
	cwd := a.workingDir
	if cwd == "" {
		cwd = "."
	}
	if !isSharedMainTree(cwd) {
		return ""
	}
	branch := sharedTreeCurrentBranch(cwd)
	if branch == "" || branch == "main" || branch == "master" {
		return ""
	}

	sharedTreeBranchMu.Lock()
	already := sharedTreeWarnedBranch == branch
	if !already {
		sharedTreeWarnedBranch = branch
	}
	sharedTreeBranchMu.Unlock()
	if already {
		return ""
	}
	debug.Log("agent", "shared-tree branch guard: validation ran on %s (not main) in the shared tree (#4803)", branch)
	return "[#4803 warning] validation ran on branch \"" + branch + "\" in the SHARED main tree - this is NOT a main-baseline result. If main-baseline verification is needed, run it in a dedicated worktree (enter_worktree) or switch the shared tree back to main first."
}
