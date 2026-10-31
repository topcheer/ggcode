package agent

import (
	"fmt"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/provider"
)

// Subagent completion push notification (P1-1): without this, a spawned
// sub-agent's result is invisible to the main loop until the model thinks
// to poll wait_agent, so models learn spawn -> wait -> wait. The completion
// feed turns "run finished" into an injected notice at the next LLM turn
// boundary - same injection point as user mid-run steering.
//
// Design constraints (independent-review revisions):
//   - ONE merged notice per boundary even when several agents finished
//     (avoid intervention fatigue from consecutive injections).
//   - First observation establishes a baseline: already-terminal agents are
//     marked seen WITHOUT injecting - they predate this loop and flooding
//     them in would be noise, not signal.
//   - Advisory wording: the model harvests via wait_agent when relevant;
//     it must not re-spawn or drop what it is doing.

// SubagentCompletion carries one terminal sub-agent state across the feed.
// Local type: internal/agent must not import internal/subagent (which
// imports agent for its runner factory) - the wiring layer converts.
type SubagentCompletion struct {
	ID      string
	Name    string
	Failed  bool
	Summary string // truncated result / error text
}

const subagentNotifySummaryRunes = 200

// SetSubagentCompletionFeed installs the completion source. Pass nil to
// disable. Safe to call before or during a run.
func (a *Agent) SetSubagentCompletionFeed(fn func() []SubagentCompletion) {
	a.mu.Lock()
	a.subagentCompletionFeed = fn
	a.mu.Unlock()
}

// injectSubagentCompletions polls the feed and, when at least one NEW
// terminal transition is observed, injects one merged user notice. Returns
// whether a notice was injected.
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
		// First observation: already-terminal agents predate this loop -
		// mark seen WITHOUT injecting (baseline swallow).
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
