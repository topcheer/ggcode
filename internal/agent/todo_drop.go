package agent

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/topcheer/ggcode/internal/debug"
)

// Todo contract drop detection.
//
// When an agent rewrites its todo list via todo_write and silently omits
// an item that was previously "pending" or "in_progress" -- without ever
// marking it "done" -- it has dropped a commitment. This is a subtle but
// costly failure mode: the agent appears to make progress (the list
// shrinks) but actually abandoned work it told the user it would do.
//
// Users rely on the todo list as a contract. Silent removals breach trust
// and cause missed requirements that surface much later.
//
// Research basis:
//   - "Agent-as-Tool: Hierarchical Decision Making" (arXiv:2507.01489):
//     hierarchical task decomposition requires tracking subtask completion,
//     not just replacement.
//   - "HiRA: Hierarchical Reasoning for Decoupled Planning and Execution"
//     (arXiv:2507.02652): plan elements must be explicitly resolved, not
//     silently dropped.
//   - Belief-behavior consistency research (arXiv:2507.02197): agents must
//     practice what they preach -- committed plans should be honored.
//
// This is distinct from:
//   - todo_staleness: detects the agent stopped UPDATING the list entirely.
//     This detector catches the list being actively rewritten with items
//     removed.
//   - plan_drift: detects the agent's NARRATIVE diverging from the plan.
//     This detector catches structural removal of committed todo items.
//   - criteria_drift: detects success criteria changing. This detector is
//     about explicit todo commitments being dropped.
//   - scope_creep: detects adding scope. This detector detects REMOVING
//     committed scope.

const (
	// todoDropMaxFires caps guidance injections per run.
	todoDropMaxFires = 1

	// todoDropSimilarity is the threshold for fuzzy-matching a removed
	// item to a new item (to avoid flagging rewording as a drop). If a
	// removed item's content is >60% similar to any new item, we consider
	// it carried forward (reworded), not dropped.
	todoDropSimilarity = 0.6
)

// todoItem represents a single todo entry parsed from todo_write arguments.
type todoItem struct {
	ID      string `json:"id"`
	Content string `json:"content"`
	Status  string `json:"status"`
}

// todoDropState tracks the previous todo list to detect silent item removal.
type todoDropState struct {
	mu        sync.Mutex
	prevItems []todoItem
	fireCount int
	hasFired  bool
}

// newTodoDropState creates a fresh detector state.
func newTodoDropState() *todoDropState {
	return &todoDropState{}
}

// reset clears state for a new run.
func (t *todoDropState) reset() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.prevItems = nil
	t.fireCount = 0
	t.hasFired = false
}

// checkDrop compares the incoming todo list with the previous one and
// returns a non-empty guidance message if items were silently dropped.
// The returned newItems are stored as prevItems for the next comparison.
func (t *todoDropState) checkDrop(input json.RawMessage) string {
	newItems := parseTodoItems(input)

	t.mu.Lock()
	defer t.mu.Unlock()

	msg := ""
	if len(t.prevItems) > 0 && !t.hasFired && t.fireCount < todoDropMaxFires {
		dropped := t.findDroppedItems(t.prevItems, newItems)
		if len(dropped) > 0 {
			t.fireCount++
			t.hasFired = true
			msg = t.formatGuidance(dropped)
		}
	}

	t.prevItems = newItems
	return msg
}

// findDroppedItems identifies items that were pending/in_progress in the
// previous list, are absent from the new list, and don't fuzzy-match any
// new item (to exclude reworded carry-forwards).
func (t *todoDropState) findDroppedItems(prev, curr []todoItem) []todoItem {
	var dropped []todoItem
	for _, p := range prev {
		// Only flag items that were not completed.
		status := strings.ToLower(strings.TrimSpace(p.Status))
		if status == "done" || status == "completed" {
			continue
		}
		// Check if this item survives (by ID or fuzzy content match).
		if t.itemSurvives(p, curr) {
			continue
		}
		dropped = append(dropped, p)
	}
	return dropped
}

// itemSurvives checks whether a previous item is still present in the new
// list, either by exact ID match or by content similarity above threshold.
func (t *todoDropState) itemSurvives(item todoItem, curr []todoItem) bool {
	for _, c := range curr {
		// Same ID = same item (possibly status-changed or reworded).
		if item.ID != "" && c.ID == item.ID {
			return true
		}
		// Fuzzy content match: item was reworded but carried forward.
		if item.Content != "" && c.Content != "" {
			sim := todoDropJaccard(item.Content, c.Content)
			if sim >= todoDropSimilarity {
				return true
			}
		}
	}
	return false
}

// formatGuidance builds the user-facing guidance message.
func (t *todoDropState) formatGuidance(dropped []todoItem) string {
	if len(dropped) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("[Todo Contract Check] ")
	if len(dropped) == 1 {
		sb.WriteString(fmt.Sprintf(
			"Your updated todo list dropped a previously-committed item that was not marked done: \"%s\". ",
			truncateStr(dropped[0].Content, 80),
		))
	} else {
		sb.WriteString(fmt.Sprintf(
			"Your updated todo list dropped %d previously-committed items that were not marked done:\n", len(dropped),
		))
		for _, d := range dropped {
			sb.WriteString(fmt.Sprintf("  - %s\n", truncateStr(d.Content, 80)))
		}
	}
	sb.WriteString("If these items were completed, mark them \"done\" rather than removing them. ")
	sb.WriteString("If they are genuinely no longer needed, that's fine -- but explicitly note the removal. ")
	sb.WriteString("Silent removal of committed todo items risks missing requirements the user expects done.")
	return sb.String()
}

// parseTodoItems extracts todo items from a todo_write tool-call input JSON.
func parseTodoItems(input json.RawMessage) []todoItem {
	var args struct {
		Todos []todoItem `json:"todos"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return nil
	}
	return args.Todos
}

// todoDropJaccard computes the Jaccard similarity coefficient between two
// strings using whitespace-tokenized word sets. Range: [0.0, 1.0].
func todoDropJaccard(a, b string) float64 {
	setA := wordSet(a)
	setB := wordSet(b)
	if len(setA) == 0 || len(setB) == 0 {
		return 0
	}
	intersection := 0
	for w := range setA {
		if setB[w] {
			intersection++
		}
	}
	union := len(setA) + len(setB) - intersection
	if union == 0 {
		return 0
	}
	return float64(intersection) / float64(union)
}

// wordSet tokenizes a string into a lowercase word set.
func wordSet(s string) map[string]bool {
	words := strings.Fields(strings.ToLower(s))
	set := make(map[string]bool, len(words))
	for _, w := range words {
		// Strip common punctuation for better matching.
		w = strings.Trim(w, ".,;:!?\"'()[]{}")
		if w != "" {
			set[w] = true
		}
	}
	return set
}

// truncateStr shortens a string to maxLen, appending "..." if truncated.
func truncateStr(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

// checkTodoDrop is the entry point called from the agent loop after a
// successful todo_write tool call. If guidance is returned, it is appended
// to the tool result content so the agent sees it on the next turn.
func (a *Agent) checkTodoDrop(args json.RawMessage) string {
	if a.todoDrop == nil {
		return ""
	}
	msg := a.todoDrop.checkDrop(args)
	if msg != "" {
		debug.Log("agent", "todo contract drop detected: %s", msg)
	}
	return msg
}

// resetTodoDrop resets the detector for a new agent run.
func (a *Agent) resetTodoDrop() {
	if a.todoDrop != nil {
		a.todoDrop.reset()
	}
}
