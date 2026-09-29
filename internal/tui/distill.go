package tui

import (
	"regexp"
	"strings"

	"github.com/topcheer/ggcode/internal/agent"
	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/tool"
)

// Trajectory→asset distillation (SE-Agent arXiv:2508.02085, ACE
// arXiv:2510.04618): a run's verified-successful shell commands are
// automatically persisted as cmd_snippet entries so future sessions can
// recall them without rediscovery. Runs post-turn alongside experience
// capture; failures are debug-logged only and never disturb the session.

const (
	// maxAutoSnippetsPerRun bounds how many commands one run may distill.
	maxAutoSnippetsPerRun = 3
	// snippet command length bounds: lower avoids fragments ("ls", "go
	// build"), upper avoids unreadable pipelines.
	snippetMinCmdLen  = 8
	snippetMaxCmdLen  = 300
	snippetNameMaxLen = 40
)

// autoSnippetDeny matches destructive or high-blast-radius commands that
// must never be auto-persisted for one-key recall.
var autoSnippetDeny = []string{
	"rm -rf", "rm -fr", "sudo rm", "git push --force", "git push -f",
	"push --force", "git reset --hard", "git checkout -- .", "git clean -f",
	"drop table", "drop database", "truncate table", "mkfs", "dd if=",
	"shutdown", "reboot", "kill -9", "pkill", "> /dev/sd", "curl | sh",
	"curl | bash", "wget | sh",
}

// autoSnippetSecrets matches credential-shaped substrings; persisting a
// command containing them would leak secrets into the snippet store.
var autoSnippetSecrets = []string{
	"sk-", "token=", "password=", "passwd=", "api_key=", "apikey=",
	"secret=", "authorization:", "bearer ", "ghp_", "gho_", "aws_",
	"-----begin",
}

// snippetCandidate reports whether a successful command is worth
// persisting as a reusable snippet.
func snippetCandidate(cmd string) bool {
	if len(cmd) < snippetMinCmdLen || len(cmd) > snippetMaxCmdLen {
		return false
	}
	// Require at least a verb plus one argument: bare verbs ("ls") carry
	// no reusable information.
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

// snippetNameSanitizer collapses anything that is not [a-z0-9] into '-'.
var snippetNameSanitizer = regexp.MustCompile(`[^a-z0-9]+`)

// snippetName derives a stable, readable snippet name from the command's
// first two words ("go test -run X" → "auto-go-test").
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

// distillCommands persists up to maxAutoSnippetsPerRun candidate commands
// via the snippet tool. Returns the number saved. SaveAutoSnippet is
// idempotent on command text, so repeated runs refresh rather than
// duplicate.
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

// distillSnippetsFromRun persists up to maxAutoSnippetsPerRun verified
// commands from the run as cmd_snippet entries.
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

// successfulCommandsLabel renders the command list for the experience
// case, preferring verified-successful commands and falling back to all
// commands run.
func successfulCommandsLabel(stats agent.RunStats) string {
	if len(stats.SuccessfulCommands) > 0 {
		return strings.Join(stats.SuccessfulCommands, "; ")
	}
	return strings.Join(stats.CommandsRun, "; ")
}
