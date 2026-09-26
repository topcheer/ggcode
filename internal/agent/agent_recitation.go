package agent

import (
	"strings"
	"sync"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/provider"
)

// Todo recitation: periodically re-anchor the active plan near the end of
// the context window.
//
// Frontier harness research (Anthropic, "Effective harnesses for long-running
// agents", Nov 2025; Manus, "Context Engineering for AI Agents", Jul 2025)
// identifies plan drift over long horizons as a primary failure mode: a todo
// list written early sits far back in the context where attention decays
// ("context rot" / lost-in-the-middle), so the agent slowly stops working the
// plan — or declares done while pending items remain.
//
// ggcode already re-injects the todo summary after compaction
// (manager.go buildPostCompactState) and nudges once per run when the list
// goes stale (maybeRemindStaleTodo), but between those two points a
// 50-iteration run never sees the plan again. Recitation differs from both:
//   - post-compact reinjection fires only at compaction boundaries;
//   - maybeRemindStaleTodo fires once per run and asks the model to UPDATE
//     the list without re-presenting its content;
//   - recitation re-presents the CURRENT plan content verbatim every
//     todoRecitationInterval model sends (and immediately after any
//     todo_write change), keeping the plan in the attention sweet spot near
//     the context tail.
//
// The recitation message is EPHEMERAL: it is appended to the request slice at
// the send site (agent.go, where contextManager.Messages() already returned a
// copy) and never stored in the context manager, session file, or TUI
// history. The durable plan state remains ~/.ggcode/todos/<session>.json, so
// compaction and session resume pick it up via the existing post-compact
// path. Ephemeral injection means zero context growth across iterations.
// Protocol safety: the message lands after the fully-closed tool results of
// the previous iteration — never between tool_calls and tool_results — which
// is a legal position for OpenAI, Anthropic and Gemini message schemas.
type todoReciter struct {
	mu          sync.Mutex
	lastSummary string // content of the last recited plan (compared by value)
	itersSince  int    // model sends since the last recitation
}

// todoRecitationInterval is the number of model sends between recitations of
// an unchanged plan. 8 bounds plan drift to at most 8 iterations while
// keeping the overhead at a few dozen tokens amortized per send.
const todoRecitationInterval = 8

func newTodoReciter() *todoReciter {
	return &todoReciter{}
}

// maybe decides whether this model send carries a recitation. summary is the
// current todo-state text ("" when no plan exists). It returns the text to
// recite, or "" when nothing is due.
func (t *todoReciter) maybe(summary string) string {
	summary = strings.TrimSpace(summary)
	t.mu.Lock()
	defer t.mu.Unlock()
	if summary == "" {
		// No active plan: reset so a list created later recites immediately.
		t.lastSummary = ""
		t.itersSince = 0
		return ""
	}
	if summary != t.lastSummary {
		// Plan created or changed (todo_write): re-anchor immediately so the
		// model always works from the freshest plan wording.
		t.lastSummary = summary
		t.itersSince = 0
		return summary
	}
	t.itersSince++
	if t.itersSince < todoRecitationInterval {
		return ""
	}
	t.itersSince = 0
	return summary
}

// todoRecitationContextManager is the capability interface for context
// managers that can produce the current todo-state summary. The content is
// identical to the todo section of the post-compact state, so the model sees
// one stable plan format on both paths.
type todoRecitationContextManager interface {
	TodoRecitationSummary() string
}

// todoRecitationMsg wraps a summary into the harness reminder envelope models
// are trained to treat as ephemeral harness state (the <system-reminder>
// convention popularized by Claude Code).
func todoRecitationMsg(summary string) provider.Message {
	return provider.Message{
		Role: "user",
		Content: []provider.ContentBlock{
			{Type: "text", Text: "<system-reminder>\n" + summary + "\n</system-reminder>"},
		},
	}
}

// appendTodoRecitation decides at the send site whether this request carries
// a plan recitation, returning the (possibly extended) message slice. msgs
// must be a fresh copy (contextManager.Messages() already returns one); the
// appended message is never written back to the manager, the session file,
// or the TUI.
func (a *Agent) appendTodoRecitation(iteration int, msgs []provider.Message) []provider.Message {
	if a.todoRecite == nil {
		return msgs
	}
	cm, ok := a.contextManager.(todoRecitationContextManager)
	if !ok {
		return msgs
	}
	summary := a.todoRecite.maybe(cm.TodoRecitationSummary())
	if summary == "" {
		return msgs
	}
	debug.Log("agent", "iteration %d: todo recitation attached (%d chars)", iteration, len(summary))
	return append(msgs, todoRecitationMsg(summary))
}
