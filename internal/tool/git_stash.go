package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"
	"sync"
)

// GitStash implements the git_stash tool for push/pop/apply/drop operations.
type GitStash struct{ WorkingDir string }

// Stash seat isolation (#4777 / S-05): the workspace-wide git stash stack is
// shared by every seat and sub-agent in a shared checkout, and a sub-agent's
// cleanup once deleted another seat's live entry (e7746bd, 2026-09-20). The
// tool now (a) stamps every push it creates with an owner tag and (b) refuses
// pop/apply/drop on entries it did not create - foreign-tagged (another seat)
// or untagged (CLI-created / pre-existing). Listing stays open via
// git_stash_list; manual entries are managed from the CLI as before.
const stashOwnerTagPrefix = "[ggcode-seat:"

var (
	stashOwnerOnce sync.Once
	stashOwnerID   string
	// stashOwnerTagRe extracts the tag payload from a `git stash list` line.
	// #4841: the push path appends the tool's own tag LAST, so extraction is
	// anchored to the END of the subject. The previous first-match regex let a
	// user message like "work [ggcode-seat:other-seat-999]" (or any injected
	// content flowing into the message param) forge an earlier tag that the
	// extractor trusted - enabling self-DoS (owner mismatch on your own entry)
	// and victim worktree injection (attacker-tagged content passing the
	// victim's pop ownership check). Tail-anchoring makes forged earlier tags
	// inert; the only trusted tag is the one the tool itself appended.
	stashOwnerTagRe = regexp.MustCompile(regexp.QuoteMeta(stashOwnerTagPrefix) + `([^\]]+)\]$`)
)

// stashOwner returns a per-process owner identity (hostname-pid). Stable for
// this session, distinct across seats and sub-agents.
func stashOwner() string {
	stashOwnerOnce.Do(func() {
		host, err := os.Hostname()
		if err != nil || host == "" {
			host = "unknown-host"
		}
		stashOwnerID = fmt.Sprintf("%s-%d", host, os.Getpid())
	})
	return stashOwnerID
}

// stashEntryOwner inspects `git stash list` and returns the owner tag of the
// entry at the given index ("" when the entry or its tag is absent).
func stashEntryOwner(ctx context.Context, dir string, index int) (string, bool) {
	cmd := gitCommand(ctx, "stash", "list")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "", false
	}
	trimmed := strings.TrimSpace(string(out))
	if trimmed == "" {
		return "", false // empty stack: no entry at any index
	}
	lines := strings.Split(trimmed, "\n")
	if index < 0 || index >= len(lines) {
		return "", false
	}
	m := stashOwnerTagRe.FindStringSubmatch(lines[index])
	if m == nil {
		return "", true // entry exists, no tag
	}
	return m[1], true
}

func (t GitStash) Name() string { return "git_stash" }

func (t GitStash) Description() string {
	return "Manage git stash entries. Supports push, pop, apply, and drop actions. Inspect git status/stash list first when possible. push stashes tracked changes only; pop and drop are destructive because they remove stash entries."
}

func (t GitStash) Parameters() json.RawMessage {
	return json.RawMessage(`{
	"type": "object",
	"properties": {
		"path": {
			"type": "string",
			"description": "Repository path (default: current directory)"
		},
		"action": {
			"type": "string",
			"description": "Stash action: push, pop, apply, drop (default: push). pop applies and removes an entry; drop removes an entry without applying.",
			"enum": [
				"push",
				"pop",
				"apply",
				"drop"
			]
		},
		"message": {
			"type": "string",
			"description": "Stash message (only used with push action). push currently stashes tracked changes only; untracked files are not included."
		},
		"index": {
			"type": "integer",
			"description": "Stash index for pop/apply/drop (default: 0, meaning latest). Confirm the desired entry with git_stash_list before pop/drop."
		},
		"description": {
			"type": "string",
			"description": "REQUIRED. Brief activity label shown in the UI, in the user's language."
		}
	},
	"required": [
		"description"
	]
}`)
}

func (t GitStash) Execute(ctx context.Context, input json.RawMessage) (Result, error) {
	var args struct {
		Path    string `json:"path"`
		Action  string `json:"action"`
		Message string `json:"message"`
		Index   int    `json:"index"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("invalid input: %v", err)}, nil
	}

	action := args.Action
	if action == "" {
		action = "push"
	}

	var gitArgs []string
	switch action {
	case "push":
		gitArgs = []string{"stash", "push"}
		// #4777: stamp ownership so later pop/drop from this tooling can tell
		// its own entries from other seats'. The user's message stays first.
		msg := strings.TrimSpace(args.Message)
		msg = strings.TrimSpace(strings.Join([]string{msg, stashOwnerTagPrefix + stashOwner() + "]"}, " "))
		gitArgs = append(gitArgs, "-m", msg)
	case "pop":
		gitArgs = []string{"stash", "pop"}
		if args.Index > 0 {
			gitArgs = append(gitArgs, fmt.Sprintf("stash@{%d}", args.Index))
		} else if args.Index < 0 {
			// #833: a negative index silently skipped the stash@{N} arg and
			// git defaulted to stash@{0} — destructive drop hit the wrong stash.
			return Result{IsError: true, Content: fmt.Sprintf("invalid stash index %d: must be >= 0", args.Index)}, nil
		}
	case "apply":
		gitArgs = []string{"stash", "apply"}
		if args.Index > 0 {
			gitArgs = append(gitArgs, fmt.Sprintf("stash@{%d}", args.Index))
		} else if args.Index < 0 {
			// #833: a negative index silently skipped the stash@{N} arg and
			// git defaulted to stash@{0} — destructive drop hit the wrong stash.
			return Result{IsError: true, Content: fmt.Sprintf("invalid stash index %d: must be >= 0", args.Index)}, nil
		}
	case "drop":
		gitArgs = []string{"stash", "drop"}
		if args.Index > 0 {
			gitArgs = append(gitArgs, fmt.Sprintf("stash@{%d}", args.Index))
		} else if args.Index < 0 {
			// #833: a negative index silently skipped the stash@{N} arg and
			// git defaulted to stash@{0} — destructive drop hit the wrong stash.
			return Result{IsError: true, Content: fmt.Sprintf("invalid stash index %d: must be >= 0", args.Index)}, nil
		}
	default:
		return Result{IsError: true, Content: fmt.Sprintf("unsupported action %q: must be push, pop, apply, or drop", action)}, nil
	}

	cmd := gitCommand(ctx, gitArgs...)
	cmd.Dir = resolveDir(args.Path, t.WorkingDir)

	// #4777 (S-05): pop/apply/drop mutate the SHARED stash stack - refuse
	// entries this process did not create (foreign seat's tag, or no tag at
	// all: CLI-created / pre-existing). The incident class is a sub-agent
	// cleanup deleting another seat's live entry; the stash stack has no
	// per-seat namespace, so the message tag is the isolation boundary.
	if action == "pop" || action == "apply" || action == "drop" {
		owner, exists := stashEntryOwner(ctx, cmd.Dir, args.Index)
		if !exists {
			return Result{IsError: true, Content: fmt.Sprintf(
				"git stash %s refused: no stash entry at index %d. Inspect git_stash_list first.", action, args.Index)}, nil
		}
		if owner != stashOwner() {
			kind := "untagged (created outside this session's tooling - CLI or another process)"
			if owner != "" {
				kind = fmt.Sprintf("owned by another seat/process (%s)", owner)
			}
			return Result{IsError: true, Content: fmt.Sprintf(
				"git stash %s refused on entry stash@{%d}: it is %s, not this session. "+
					"The stash stack is shared across all seats in this workspace (#4777) - "+
					"manage foreign entries from the CLI after coordinating with their owner, "+
					"or use git_stash_list to pick an entry this session created.",
				action, args.Index, kind)}, nil
		}
	}

	out, err := cmd.CombinedOutput()
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("git stash %s failed: %v\n%s", action, err, out)}, nil
	}

	trimmed := strings.TrimSpace(string(out))

	// After push: warn about untracked files that were NOT stashed.
	// git stash push only saves tracked changes by default. Untracked
	// files (e.g., newly created by the agent) remain in the working tree.
	if action == "push" {
		untracked := countUntrackedFiles(ctx, cmd.Dir)
		if untracked > 0 {
			warning := fmt.Sprintf(
				"\n\nWarning: %d untracked file(s) were NOT stashed. "+
					"git stash push only saves tracked changes. To include untracked files, "+
					"use 'git stash push --include-untracked'.",
				untracked,
			)
			if trimmed == "" {
				return Result{Content: fmt.Sprintf("git stash %s completed.%s", action, warning)}, nil
			}
			trimmed += warning
		}
	}

	if trimmed == "" {
		return Result{Content: fmt.Sprintf("git stash %s completed.", action)}, nil
	}

	return Result{Content: trimmed}, nil
}

// countUntrackedFiles runs 'git status --porcelain' and counts untracked entries (?? prefix).
func countUntrackedFiles(ctx context.Context, dir string) int {
	cmd := gitCommand(ctx, "status", "--porcelain")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return 0
	}
	count := 0
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "?? ") {
			count++
		}
	}
	return count
}

// Clone returns an independent copy of this tool for use by a different agent.
func (t GitStash) Clone() Tool {
	return &GitStash{WorkingDir: t.WorkingDir}
}
