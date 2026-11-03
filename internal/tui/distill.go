package tui

import (
	"regexp"
	"strings"

	"github.com/topcheer/ggcode/internal/agent"
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/tool"
)

const (
	maxAutoSnippetsPerRun = 3

	snippetMinCmdLen  = 8
	snippetMaxCmdLen  = 300
	snippetNameMaxLen = 40
)

var autoSnippetDeny = []string{
	"rm -rf", "rm -fr", "sudo rm", "git push --force", "git push -f",
	"push --force", "git reset --hard", "git checkout -- .", "git clean -f",
	"drop table", "drop database", "truncate table", "mkfs", "dd if=",
	"shutdown", "reboot", "kill -9", "pkill", "> /dev/sd", "curl | sh",
	"curl | bash", "wget | sh",
}

var autoSnippetSecrets = []string{
	"sk-", "token=", "password=", "passwd=", "api_key=", "apikey=",
	"secret=", "authorization:", "bearer ", "ghp_", "gho_", "aws_",
	"-----begin",
}

func snippetCandidate(cmd string) bool {
	if len(cmd) < snippetMinCmdLen || len(cmd) > snippetMaxCmdLen {
		return false
	}

	if len(strings.Fields(cmd)) < 2 {
		return false
	}
	lower := strings.ToLower(cmd)
	for _, d := range autoSnippetDeny {
		if strings.Contains(lower, d) {
			return false
		}
	}
	for _, s := range autoSnippetSecrets {
		if strings.Contains(lower, s) {
			return false
		}
	}
	return true
}

var snippetNameSanitizer = regexp.MustCompile(`[^a-z0-9]+`)

func snippetName(cmd string) string {
	fields := strings.Fields(cmd)
	name := fields[0]
	if len(fields) > 1 {
		name += " " + fields[1]
	}
	name = snippetNameSanitizer.ReplaceAllString(strings.ToLower(name), "-")
	name = strings.Trim(name, "-")
	if name == "" {
		name = "cmd"
	}
	if len(name) > snippetNameMaxLen {
		name = name[:snippetNameMaxLen]
	}
	return "auto-" + name
}

func distillCommands(st *tool.CmdSnippetTool, cmds []string) int {
	saved := 0
	for _, cmd := range cmds {
		if saved >= maxAutoSnippetsPerRun {
			break
		}
		if !snippetCandidate(cmd) {
			continue
		}
		if _, err := st.SaveAutoSnippet(snippetName(cmd), cmd,
			"distilled from a successful run", []string{"auto", "distilled"}); err != nil {
			debug.Log("tui", "distill: save snippet failed: %v", err)
			continue
		}
		saved++
	}
	return saved
}

func distillSnippetsFromRun(a *agent.Agent, stats agent.RunStats) {
	if len(stats.SuccessfulCommands) == 0 {
		return
	}
	workingDir := a.WorkingDir()
	if workingDir == "" {
		return
	}
	st := &tool.CmdSnippetTool{WorkingDir: workingDir}
	if saved := distillCommands(st, stats.SuccessfulCommands); saved > 0 {
		debug.Log("tui", "distill: persisted %d snippet(s) from run", saved)
	}
}

func successfulCommandsLabel(stats agent.RunStats) string {
	if len(stats.SuccessfulCommands) > 0 {
		return strings.Join(stats.SuccessfulCommands, "; ")
	}
	return strings.Join(stats.CommandsRun, "; ")
}
