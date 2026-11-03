package agent

import (
	"fmt"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/provider"
)

type SubagentCompletion struct {
	ID      string
	Name    string
	Failed  bool
	Summary string
}

const subagentNotifySummaryRunes = 200

func (a *Agent) SetSubagentCompletionFeed(fn func() []SubagentCompletion) {
	a.mu.Lock()
	a.subagentCompletionFeed = fn
	a.mu.Unlock()
}

func (a *Agent) injectSubagentCompletions() bool {
	a.mu.RLock()
	feed := a.subagentCompletionFeed
	a.mu.RUnlock()
	if feed == nil {
		return false
	}
	completions := feed()
	if len(completions) == 0 {
		return false
	}

	a.subagentNotifiedMu.Lock()
	defer a.subagentNotifiedMu.Unlock()
	baseline := a.subagentNotified == nil
	if baseline {
		a.subagentNotified = make(map[string]bool)
	}
	var fresh []SubagentCompletion
	for _, c := range completions {
		if a.subagentNotified[c.ID] {
			continue
		}
		a.subagentNotified[c.ID] = true
		fresh = append(fresh, c)
	}
	if baseline {

		return false
	}
	if len(fresh) == 0 {
		return false
	}

	var b strings.Builder
	b.WriteString("A sub-agent run finished while you were working:\n")
	for _, c := range fresh {
		status := "completed"
		if c.Failed {
			status = "FAILED"
		}
		line := fmt.Sprintf("- %s (%s): %s", c.ID, c.Name, status)
		if s := truncateRunes(strings.TrimSpace(c.Summary), subagentNotifySummaryRunes); s != "" {
			line += " - " + s
		}
		b.WriteString(line + "\n")
	}
	b.WriteString("When its result is relevant to your current task, harvest the full output with wait_agent or list_agents; otherwise continue what you were doing. Do not re-spawn.")

	debug.Log("agent", "injecting sub-agent completion notice (n=%d)", len(fresh))
	a.contextManager.Add(provider.Message{
		Role: "user",
		Content: []provider.ContentBlock{{
			Type: "text",
			Text: b.String(),
		}},
	})
	return true
}
