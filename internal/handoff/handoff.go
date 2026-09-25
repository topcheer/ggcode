// Package handoff implements context reset with a structured handoff
// artifact — the "context reset" alternative to in-place compaction
// described in Anthropic's "Harness design for long-running application
// development" (Mar 2026, https://www.anthropic.com/engineering/harness-design-long-running-apps):
//
//	Compaction summarizes history in place so the same agent continues on a
//	shortened history; continuity is preserved but the agent never gets a
//	clean slate ("context anxiety" can persist). A context reset instead
//	clears the context entirely and starts a fresh agent, paired with a
//	structured handoff artifact that carries the previous agent's state and
//	next steps.
//
// ggcode already had both ends of that spectrum (`/clear` resets with no
// handoff, `/compact` summarizes in place) but not the combination: reset +
// artifact. This package builds the artifact from session-local facts
// (user goals, live task board, git snapshot) with zero LLM calls, so the
// reset is instant, deterministic and works offline.
package handoff

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/provider"
)

// Marker is the first line of every handoff artifact and of the system
// message seeded into the fresh session's context. It lets tools and
// prompt inspectors identify handoff briefings unambiguously (same role as
// the context manager's "[Previous conversation summary]" marker).
const Marker = "[Session Handoff]"

// Caps keep the artifact small enough to seed a fresh context cheaply.
const (
	// maxGoals keeps the N most recent user goals (oldest dropped first).
	maxGoals = 10
	// maxGoalRunes bounds a single goal line; runes (not bytes) so CJK
	// text truncates fairly.
	maxGoalRunes = 400
	// maxStatusLines bounds `git status --porcelain` output.
	maxStatusLines = 30
	// maxCommitLines bounds `git log --oneline` output.
	maxCommitLines = 5
	// gitTimeout bounds each git subprocess call.
	gitTimeout = 5 * time.Second
)

// GitInfo is a best-effort snapshot of the workspace git state at handoff
// time. Available is false when git is missing, the directory is not a
// repository, or every probe failed — the artifact still renders.
type GitInfo struct {
	Available bool
	Branch    string
	Status    string
	Log       string
}

// Snapshot is everything a fresh session needs to resume the previous
// session's work after a full context reset.
type Snapshot struct {
	// OldSessionID is the session being handed off ("" if unknown).
	OldSessionID string
	// Model is the vendor/endpoint/model string for provenance.
	Model string
	// GeneratedAt timestamps the artifact.
	GeneratedAt time.Time
	// Goals are the user's stated goals, most recent last.
	Goals []string
	// TasksStats is a one-line board summary, e.g.
	// "3 completed · 1 in progress · 5 pending"; "" when no board exists.
	TasksStats string
	// TasksDigest is the rendered task board (caller-provided, already
	// size-capped); "" when no board exists.
	TasksDigest string
	// Git is the workspace git snapshot at handoff time.
	Git GitInfo
}

// ExtractGoals pulls the user's text messages out of a session's message
// log, keeping the most recent maxGoals entries (oldest dropped first),
// truncating each to maxGoalRunes, skipping blank text and collapsing
// consecutive duplicates (retry/resend artifacts).
func ExtractGoals(msgs []provider.Message) []string {
	var all []string
	for _, msg := range msgs {
		if msg.Role != "user" {
			continue
		}
		for _, block := range msg.Content {
			if block.Type != "text" {
				continue
			}
			text := strings.TrimSpace(block.Text)
			if text == "" {
				continue
			}
			if n := len(all); n > 0 && all[n-1] == text {
				continue // consecutive duplicate (resend/retry)
			}
			all = append(all, truncateRunes(text, maxGoalRunes))
		}
	}
	if len(all) > maxGoals {
		all = all[len(all)-maxGoals:]
	}
	return all
}

// CollectGit captures a best-effort git snapshot of dir. Every failure is
// tolerated: a handoff must never fail because git does.
func CollectGit(dir string) GitInfo {
	if dir == "" {
		return GitInfo{}
	}
	ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
	defer cancel()

	git := func(args ...string) (string, bool) {
		cmd := exec.CommandContext(ctx, "git", args...)
		cmd.Dir = dir
		var out bytes.Buffer
		cmd.Stdout = &out
		if err := cmd.Run(); err != nil {
			return "", false
		}
		return strings.TrimSpace(out.String()), true
	}

	// -C would be cleaner, but cmd.Dir already scopes the call; rev-parse
	// doubles as the repository probe.
	branch, ok := git("rev-parse", "--abbrev-ref", "HEAD")
	if !ok {
		return GitInfo{}
	}
	status, _ := git("status", "--porcelain")
	log, _ := git("log", "--oneline", "-n", fmt.Sprint(maxCommitLines))
	return GitInfo{
		Available: true,
		Branch:    branch,
		Status:    capLines(status, maxStatusLines),
		Log:       log,
	}
}

// Render turns a Snapshot into the markdown handoff artifact. It always
// starts with Marker and always renders every section (with "(none)"
// placeholders), so downstream consumers can rely on a stable shape.
func Render(s Snapshot) string {
	var b strings.Builder
	b.WriteString(Marker)
	b.WriteString("\n\n")
	if !s.GeneratedAt.IsZero() {
		fmt.Fprintf(&b, "_Generated %s", s.GeneratedAt.Format(time.RFC3339))
		if s.OldSessionID != "" {
			fmt.Fprintf(&b, " · from session %s", shortID(s.OldSessionID))
		}
		if s.Model != "" {
			fmt.Fprintf(&b, " · %s", s.Model)
		}
		b.WriteString("_\n")
	}
	b.WriteString(`
You are taking over from a previous session whose conversation context was
fully reset. This artifact is the previous session's handoff. Re-verify
file and git state before acting on anything below, and keep the task
board updated with the task tools (task IDs remain valid).
`)

	b.WriteString("\n## Mission (user goals, oldest → newest)\n")
	if len(s.Goals) == 0 {
		b.WriteString("(none recorded)\n")
	}
	for i, g := range s.Goals {
		fmt.Fprintf(&b, "%d. %s\n", i+1, g)
	}

	b.WriteString("\n## Task board (carried over — live state)\n")
	if s.TasksStats == "" && s.TasksDigest == "" {
		b.WriteString("(no tasks on the board)\n")
	} else {
		if s.TasksStats != "" {
			b.WriteString(s.TasksStats)
			b.WriteString("\n")
		}
		if s.TasksDigest != "" {
			b.WriteString(indent(s.TasksDigest))
			b.WriteString("\n")
		}
	}

	b.WriteString("\n## Git snapshot\n")
	if !s.Git.Available {
		b.WriteString("(git unavailable — verify the workspace state manually)\n")
	} else {
		fmt.Fprintf(&b, "branch: %s\n", s.Git.Branch)
		if s.Git.Status != "" {
			b.WriteString("dirty files:\n")
			b.WriteString(indent(s.Git.Status))
			b.WriteString("\n")
		} else {
			b.WriteString("working tree clean\n")
		}
		if s.Git.Log != "" {
			b.WriteString("recent commits:\n")
			b.WriteString(indent(s.Git.Log))
			b.WriteString("\n")
		}
	}

	b.WriteString(`
## Suggested next steps
- Re-orient: run git status and inspect the files named in the mission before editing.
- Pick up the oldest in-progress task from the board above; keep its status current.
- When this session grows stale too, run /handoff again to reset with a fresh artifact.
`)
	return b.String()
}

// SystemMessage wraps a rendered artifact as the system message seeded
// into the fresh session's context (protocol-safe: a lone system message
// in an otherwise-empty context).
func SystemMessage(artifact string) provider.Message {
	return provider.Message{
		Role: "system",
		Content: []provider.ContentBlock{
			{Type: "text", Text: strings.TrimSpace(artifact)},
		},
	}
}

// SanitizeToken makes a session ID safe for use in an artifact filename.
func SanitizeToken(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		}
		if b.Len() >= 24 {
			break
		}
	}
	if b.Len() == 0 {
		return "session"
	}
	return b.String()
}

func shortID(s string) string {
	if len(s) <= 8 {
		return s
	}
	return s[:8]
}

func indent(s string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i, line := range lines {
		lines[i] = "  " + line
	}
	return strings.Join(lines, "\n")
}

func capLines(s string, max int) string {
	if s == "" {
		return ""
	}
	lines := strings.Split(s, "\n")
	if len(lines) > max {
		lines = append(lines[:max], fmt.Sprintf("(… %d more)", len(lines)-max))
	}
	return strings.Join(lines, "\n")
}

func truncateRunes(s string, max int) string {
	runes := []rune(strings.TrimSpace(s))
	if len(runes) <= max {
		return string(runes)
	}
	return string(runes[:max]) + "…"
}
