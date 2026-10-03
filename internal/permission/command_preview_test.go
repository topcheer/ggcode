package permission

import (
	"reflect"
	"testing"
)

func targets(rep PreviewReport) []string { return rep.WriteTargets }

// Compound chains split on && / ; / | - each segment rehearsed.
func TestPreviewCommand_CompoundChain(t *testing.T) {
	rep := PreviewCommand("mkdir -p a/b && rm -rf build && cat x | sort > out.txt")
	if len(rep.Subcommands) != 4 {
		t.Fatalf("segments = %d, want 4: %+v", len(rep.Subcommands), rep.Subcommands)
	}
	got := targets(rep)
	want := []string{"a/b", "build", "out.txt"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("write targets = %v, want %v", got, want)
	}
	if !rep.Destructive {
		t.Error("rm -rf segment must mark report destructive")
	}
}

// Pipe characters inside quotes must NOT split segments.
func TestPreviewCommand_QuotedPipeNoSplit(t *testing.T) {
	rep := PreviewCommand(`grep "a|b" file.txt`)
	if len(rep.Subcommands) != 1 {
		t.Fatalf("quoted pipe split the command: %d segments", len(rep.Subcommands))
	}
	if len(targets(rep)) != 0 {
		t.Errorf("grep is read-only, got targets %v", targets(rep))
	}
}

// rm enumerates every non-flag positional; -rf is a flag.
func TestPreviewCommand_RmEnumeratesTargets(t *testing.T) {
	rep := PreviewCommand("rm -rf /tmp/a /tmp/b c.txt")
	want := []string{"/tmp/a", "/tmp/b", "c.txt"}
	if got := targets(rep); !reflect.DeepEqual(got, want) {
		t.Errorf("rm targets = %v, want %v", got, want)
	}
	if !rep.Destructive {
		t.Error("rm must be destructive")
	}
}

// git clean without paths targets the entire worktree.
func TestPreviewCommand_GitCleanWholeTree(t *testing.T) {
	rep := PreviewCommand("git clean -fdx")
	found := false
	for _, s := range rep.Subcommands {
		for _, tg := range s.WriteTargets {
			if tg == "(entire worktree)" {
				found = true
			}
		}
	}
	if !found {
		t.Errorf("git clean -fdx must flag entire worktree: %+v", rep.Subcommands)
	}
	if !rep.Destructive {
		t.Error("git clean must be destructive")
	}
}

// git reset --hard is destructive but has no enumerable file targets.
func TestPreviewCommand_GitResetHard(t *testing.T) {
	rep := PreviewCommand("git reset --hard HEAD~1")
	if !rep.Destructive {
		t.Error("git reset --hard must be destructive")
	}
	if len(targets(rep)) != 0 {
		t.Errorf("git reset has no enumerable targets, got %v", targets(rep))
	}
}

// sed -i targets the file operands, not the script.
func TestPreviewCommand_SedInPlace(t *testing.T) {
	rep := PreviewCommand(`sed -i 's/a/b/' main.go`)
	got := targets(rep)
	want := []string{"main.go"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("sed -i targets = %v, want %v", got, want)
	}
}

// Redirection attaches write targets even to read-only verbs.
func TestPreviewCommand_RedirectOnReadonlyVerb(t *testing.T) {
	rep := PreviewCommand("sort input.txt > sorted.txt")
	got := targets(rep)
	if len(got) != 1 || got[0] != "sorted.txt" {
		t.Errorf("redirect target = %v, want [sorted.txt]", got)
	}
}

// sudo/env wrappers are rehearsed through to the wrapped verb.
func TestPreviewCommand_SudoWrapped(t *testing.T) {
	rep := PreviewCommand("sudo rm -rf /etc/thing")
	if !rep.Destructive {
		t.Error("sudo rm must surface destructive through the wrapper")
	}
	got := targets(rep)
	if len(got) != 1 || got[0] != "/etc/thing" {
		t.Errorf("sudo rm targets = %v, want [/etc/thing]", got)
	}
}

// Pure read commands produce no targets and no destructive flag.
func TestPreviewCommand_ReadOnly(t *testing.T) {
	for _, cmd := range []string{"ls -la", "cat main.go", "go version", ""} {
		rep := PreviewCommand(cmd)
		if len(targets(rep)) != 0 || rep.Destructive {
			t.Errorf("read-only %q produced targets=%v destructive=%v", cmd, targets(rep), rep.Destructive)
		}
	}
}

// Verbs with a leading path (/bin/rm) are normalized.
func TestPreviewCommand_VerbPathPrefix(t *testing.T) {
	rep := PreviewCommand("/bin/rm -rf build")
	if !rep.Destructive {
		t.Error("/bin/rm must normalize to rm and be destructive")
	}
	if len(rep.Subcommands) == 0 || rep.Subcommands[0].Verb != "rm" {
		t.Errorf("verb not normalized: %+v", rep.Subcommands)
	}
}
