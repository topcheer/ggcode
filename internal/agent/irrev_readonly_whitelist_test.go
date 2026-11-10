package agent

import (
	"testing"
)

// The irreversibility gate must not fire on commands that purely inspect
// state: the first effective token (env assignments and sudo/nohup wrapper
// prefixes stripped) decides the classification, and any write redirect,
// pipe into a mutating command, or chaining with a mutating command keeps
// the whole string gated (conservative: the entire command is rated by its
// most dangerous segment).
func TestIrrevReadOnlyCommandDowngrade(t *testing.T) {
	commands := []string{
		// Plain read-only binaries.
		"grep -rn TODO internal/",
		"cat main.go",
		"ls -la",
		"head -20 log.txt",
		"tail -f app.log",
		"find . -name '*.go'",
		// Read-only git subcommands.
		"git log --oneline -5",
		"git status",
		"git diff HEAD~1",
		"git show abc1234",
		"git branch -a",
		// Read-only gh queries (gh api is GET-by-default).
		"gh api repos/example/repo",
		"gh pr view 42",
		"gh search issues 'flaky test'",
	}
	for _, cmd := range commands {
		if got := irrevClassifyTool("run_command", `{"command":"`+cmd+`"}`); got != irrevTierNone {
			t.Errorf("read-only command %q should be tier none, got %d", cmd, got)
		}
	}
}

func TestIrrevReadOnlyWrapperPrefixStripping(t *testing.T) {
	commands := []string{
		// sudo/nohup wrappers: the first EFFECTIVE token classifies.
		"sudo cat /etc/hosts",
		"sudo git log --oneline -1",
		"nohup grep -rn pattern .",
		"sudo nohup ls -la /var/log",
		// env-assignment prefix combined with a wrapper.
		"LC_ALL=C sudo grep -i foo bar.txt",
	}
	for _, cmd := range commands {
		if got := irrevClassifyTool("run_command", `{"command":"`+cmd+`"}`); got != irrevTierNone {
			t.Errorf("wrapped read-only command %q should be tier none, got %d", cmd, got)
		}
	}

	// A wrapper in front of a mutating command must not downgrade it.
	if got := irrevClassifyTool("run_command", `{"command":"sudo rm -rf /tmp/x"}`); got != irrevTierHigh {
		t.Errorf("sudo rm -rf must stay tier high, got %d", got)
	}
	if got := irrevClassifyTool("run_command", `{"command":"nohup npm publish"}`); got == irrevTierNone {
		// npm is not whitelisted, so this must NOT be read-only.
		t.Errorf("nohup <non-whitelisted> must not be read-only, got tier none")
	}
}

func TestIrrevReadOnlyRedirectKeepsGate(t *testing.T) {
	commands := []string{
		// Write redirects turn an inspection command into a mutation.
		"grep foo bar.txt > out.txt",
		"echo hello >> appends.log",
		"cat a.go >| clobber.txt",
	}
	for _, cmd := range commands {
		if got := irrevClassifyTool("run_command", `{"command":"`+cmd+`"}`); got < irrevTierMedium {
			t.Errorf("command with write redirect %q must stay gated (>= medium), got %d", cmd, got)
		}
	}
}

func TestIrrevReadOnlyPipelineAndChainKeepGate(t *testing.T) {
	commands := []string{
		// Pipe into a mutating command.
		"cat names.txt | tee copy.txt",
		"grep -l stale . | xargs rm",
		// Chaining with a mutating command.
		"git status && rm file.txt",
		"ls -la; npm install",
	}
	for _, cmd := range commands {
		if got := irrevClassifyTool("run_command", `{"command":"`+cmd+`"}`); got < irrevTierMedium {
			t.Errorf("command chaining a mutating segment %q must stay gated (>= medium), got %d", cmd, got)
		}
	}
}

func TestReadOnlyWhitelistsExported(t *testing.T) {
	// The exported whitelists must stay populated so the classification is
	// observable (and overridable) by package users and tests.
	for _, bin := range []string{"grep", "cat", "head", "tail", "ls", "find"} {
		if !ReadOnlyCommandBinaries[bin] {
			t.Errorf("ReadOnlyCommandBinaries missing %q", bin)
		}
	}
	for _, sub := range []string{"log", "status", "diff", "show", "branch"} {
		if !ReadOnlyGitSubcommands[sub] {
			t.Errorf("ReadOnlyGitSubcommands missing %q", sub)
		}
	}
	if !ReadOnlyGhSubcommands["search"]["issues"] {
		t.Error("ReadOnlyGhSubcommands missing search.issues")
	}
}
