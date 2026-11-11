package agentruntime

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/topcheer/ggcode/internal/provider"
	"github.com/topcheer/ggcode/internal/session"
)

const (
	// DefaultShiftHandoffWindow bounds how recent the previous session must
	// be for its handoff brief to be injected. 72h covers overnight and
	// weekend gaps for cron-driven workspaces without resurfacing stale work.
	DefaultShiftHandoffWindow = 72 * time.Hour

	// shiftHandoffMaxRunes caps the total injected brief so the system
	// prompt pays a fixed, small token cost for continuity context.
	shiftHandoffMaxRunes = 800
)

// BuildShiftHandoffBrief renders a short, deterministic handoff brief from
// the most recent prior session in the same workspace (#shift-handoff).
//
// A brand-new session otherwise starts amnesiac: the prompt assembly injects
// project snapshots, skills and automemory, but nothing about what the
// previous session in this workspace was doing (the "shift handoff" gap -
// cron and one-shot sessions re-read context from zero every run).
//
// skipSessionID is excluded (used when the current session is being resumed;
// resumed sessions already carry their full history and need no brief).
// Entries older than maxAge stop the scan: ListForWorkspace is sorted
// newest-first, so the first stale entry means every later entry is stale
// too. Pure deterministic assembly - no LLM cost, one full session Load.
func BuildShiftHandoffBrief(store session.Store, workingDir, skipSessionID string, maxAge time.Duration) string {
	if store == nil {
		return ""
	}
	sessions, err := store.ListForWorkspace(workingDir)
	if err != nil || len(sessions) == 0 {
		return ""
	}
	for _, entry := range sessions {
		if entry.ID == skipSessionID {
			continue
		}
		if time.Since(entry.UpdatedAt) > maxAge {
			return ""
		}
		full, err := store.Load(entry.ID)
		if err != nil || full == nil || !full.HasUserInteraction() {
			continue
		}
		goal := firstHumanMessage(full.Messages)
		state := lastAssistantMessage(full.Messages)
		if goal == "" && state == "" {
			continue
		}
		return composeHandoffBrief(entry.Title, entry.UpdatedAt, goal, state)
	}
	return ""
}

// firstHumanMessage returns the collapsed text of the first genuine human
// user message - the session's goal, as typed by the user.
func firstHumanMessage(msgs []provider.Message) string {
	for _, m := range msgs {
		if m.Role != "user" || m.Origin != "human" {
			continue
		}
		var sb strings.Builder
		for _, b := range m.Content {
			if b.Type == "text" {
				sb.WriteString(b.Text)
			}
		}
		if s := collapseSpace(sb.String()); s != "" {
			return s
		}
	}
	return ""
}

// lastAssistantMessage returns the collapsed text of the final assistant
// message - the session's end state (what it last reported doing).
func lastAssistantMessage(msgs []provider.Message) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		m := msgs[i]
		if m.Role != "assistant" {
			continue
		}
		var sb strings.Builder
		for _, b := range m.Content {
			if b.Type == "text" {
				sb.WriteString(b.Text)
			}
		}
		if s := collapseSpace(sb.String()); s != "" {
			return s
		}
	}
	return ""
}

func composeHandoffBrief(title string, ended time.Time, goal, state string) string {
	var b strings.Builder
	b.WriteString("## Previous Session Handoff\n")
	b.WriteString("The most recent session in this workspace ended " + formatAgo(time.Since(ended)))
	if title != "" {
		b.WriteString(" (" + handoffTruncate(title, 60) + ")")
	}
	b.WriteString(".\n")
	if goal != "" {
		b.WriteString("Goal: " + handoffTruncate(goal, 280) + "\n")
	}
	if state != "" {
		b.WriteString("Last state: " + handoffTruncate(state, 280) + "\n")
	}
	b.WriteString("Treat this as background context only; verify current repository state before acting on it.")
	return handoffTruncate(b.String(), shiftHandoffMaxRunes)
}

func formatAgo(d time.Duration) string {
	switch {
	case d < time.Hour:
		return fmt.Sprintf("%d minutes ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%d hours ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%d days ago", int(d.Hours()/24))
	}
}

func collapseSpace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func handoffTruncate(s string, max int) string {
	if max <= 0 || utf8.RuneCountInString(s) <= max {
		return s
	}
	runes := []rune(s)
	return string(runes[:max-1]) + "\u2026"
}
