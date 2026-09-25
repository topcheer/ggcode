package agent

// Mid-Run Stale Todo Detection
//
// Research: Claude Code, Cursor, and OpenHands all emphasize keeping todo lists
// current "at every milestone." The system prompt instructs the agent to update
// todos as work progresses. In practice, agents frequently:
//
//   1. Create a todo list with 3-7 items
//   2. Mark the first item as in_progress
//   3. Work for many iterations (editing files, running builds, fixing errors)
//      WITHOUT calling todo_write again
//   4. The todo list becomes stale — it no longer reflects actual progress
//
// The existing checkIncompleteTodos() only fires at end-of-run (when the agent
// stops making tool calls). There's no MID-RUN detection of plan abandonment.
//
// This module fills that gap: it tracks the iteration of the last successful
// todo_write call and injects a gentle reminder when todos haven't been updated
// for a configurable number of iterations while there are still incomplete items.
//
// r78 consolidation: the structured task board (task_create/task_update) is the
// second plan representation agents abandon mid-run. Instead of adding a 192nd
// detector, this state now ALSO records successful task_create/task_update calls
// as plan-sync signals (boardSync) and includes incomplete board tasks in the
// lazy incompleteness check. One state machine, one injection site, both plans.
//
// Design:
//   - Deterministic, zero-LLM-cost (pure counters + one lazy disk read)
//   - Fires at most once per stagnation period; resets when the agent updates
//   - Only fires when there are incomplete (pending/in_progress) todos
//   - Threshold tuned to avoid noise: 8 iterations is enough to distinguish
//     active-but-stale from normal update cadence

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/task"
	"github.com/topcheer/ggcode/internal/tool"
)

const (
	// staleTodoThreshold: minimum iterations since last todo_write update
	// before a staleness reminder fires. Tuned so agents that update todos
	// every 2-4 iterations never trigger, while truly abandoned plans do.
	staleTodoThreshold = 8

	// maxStaleTodoReminders: cap the total number of staleness reminders per
	// run to prevent flooding the context if the agent repeatedly ignores them.
	maxStaleTodoReminders = 2
)

// todoStalenessState tracks todo_write update recency to detect plan abandonment.
type todoStalenessState struct {
	// lastUpdateIter is the iteration number of the last successful todo_write
	// call. -1 means no todo_write has occurred yet.
	lastUpdateIter int

	// hasTodos records whether the last todo_write created a non-empty list.
	hasTodos bool

	// remindedCount is the total number of staleness reminders injected this run.
	remindedCount int

	// reminderActive means a reminder was injected for the current stagnation
	// period and hasn't been cleared by a subsequent todo_write. Prevents
	// repeated firing while waiting for the agent to act on it.
	reminderActive bool

	// boardSync records that at least one task_create/task_update succeeded
	// (r78): the structured task board counts as plan state alongside the
	// todo list, so board activity resets staleness too.
	boardSync bool
}

func newTodoStalenessState() *todoStalenessState {
	return &todoStalenessState{lastUpdateIter: -1}
}

func (s *todoStalenessState) reset() {
	s.lastUpdateIter = -1
	s.hasTodos = false
	s.remindedCount = 0
	s.reminderActive = false
	s.boardSync = false
}

// recordUpdate is called whenever todo_write succeeds. It records the iteration
// and clears the active-reminder flag so a new stagnation period can be detected.
func (s *todoStalenessState) recordUpdate(iteration int, todoCount int) {
	s.lastUpdateIter = iteration
	s.hasTodos = todoCount > 0
	s.reminderActive = false
}

// recordBoardUpdate is called whenever task_create/task_update succeeds (r78).
// Board activity is a plan-sync signal: it refreshes the recency clock and
// clears the active reminder, exactly like a todo_write would.
func (s *todoStalenessState) recordBoardUpdate(iteration int) {
	s.lastUpdateIter = iteration
	s.boardSync = true
	s.reminderActive = false
}

// shouldRemind returns true if todos are stale and a reminder should be injected.
// The hasIncomplete parameter is computed lazily by the caller (via ListTodos)
// only when the threshold is met, avoiding unnecessary disk reads.
func (s *todoStalenessState) shouldRemind(iteration int, hasIncomplete bool) bool {
	if !s.hasTodos || !hasIncomplete {
		return false
	}
	if s.remindedCount >= maxStaleTodoReminders {
		return false
	}
	if s.reminderActive {
		return false
	}
	if s.lastUpdateIter < 0 {
		return false
	}
	return iteration-s.lastUpdateIter >= staleTodoThreshold
}

func (s *todoStalenessState) markReminded() {
	s.reminderActive = true
	s.remindedCount++
}

// --- Agent integration methods ---

// recordTodoStalenessUpdate records a successful todo_write call for staleness tracking.
func (a *Agent) recordTodoStalenessUpdate(iteration int, todoCount int) {
	a.todoStaleness.recordUpdate(iteration, todoCount)
}

// resetTodoStaleness clears state for a new run.
func (a *Agent) resetTodoStaleness() {
	a.todoStaleness.reset()
}

// maybeRemindStaleTodo checks whether the todo list has gone stale and, if so,
// returns a reminder message to inject into the conversation. Returns empty
// string if no reminder is needed.
func (a *Agent) maybeRemindStaleTodo(iteration int) string {
	// Quick check: if no plan state (todos or task board) was ever written,
	// nothing to do.
	if a.todoStaleness.lastUpdateIter < 0 || (!a.todoStaleness.hasTodos && !a.todoStaleness.boardSync) {
		return ""
	}
	// Quick check: not enough iterations have passed yet.
	if iteration-a.todoStaleness.lastUpdateIter < staleTodoThreshold {
		return ""
	}
	// Budget check: don't flood the context.
	if a.todoStaleness.remindedCount >= maxStaleTodoReminders || a.todoStaleness.reminderActive {
		return ""
	}

	// Lazy checks: only inspect todo file / task board when threshold is met.
	hasIncomplete, incompleteCount, totalCount := a.checkHasIncompleteTodos()
	if !hasIncomplete {
		incompleteCount, totalCount = a.checkHasIncompleteTasks()
		if incompleteCount == 0 {
			return ""
		}
		hasIncomplete = true
	}

	a.todoStaleness.markReminded()
	itersSince := iteration - a.todoStaleness.lastUpdateIter
	debug.Log("todo_staleness", "stale plan state detected: %d incomplete of %d, %d iterations since last update (threshold %d), injecting reminder (%d/%d)",
		incompleteCount, totalCount, itersSince, staleTodoThreshold, a.todoStaleness.remindedCount, maxStaleTodoReminders)

	if a.todoStaleness.hasTodos {
		return staleTodoReminderText(incompleteCount, totalCount, itersSince)
	}
	return staleTaskBoardReminderText(incompleteCount, totalCount, itersSince)
}

// checkHasIncompleteTodos reads the current session's todos and returns whether
// any are pending or in_progress, plus counts. Returns false if no todos or
// all done or the tool is unavailable.
func (a *Agent) checkHasIncompleteTodos() (hasIncomplete bool, incompleteCount int, totalCount int) {
	t, ok := a.tools.Get("todo_write")
	if !ok {
		return false, 0, 0
	}
	tw, ok := t.(*tool.TodoWrite)
	if !ok {
		return false, 0, 0
	}
	todos, err := tw.ListTodos()
	if err != nil || len(todos) == 0 {
		return false, 0, 0
	}
	totalCount = len(todos)
	for _, td := range todos {
		if td.Status == "pending" || td.Status == "in_progress" {
			incompleteCount++
		}
	}
	return incompleteCount > 0, incompleteCount, totalCount
}

// staleTodoReminderText builds the reminder message for a stale todo list.
func staleTodoReminderText(incomplete, total, itersSince int) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf(
		"Todo list sync: It's been %d iterations since you last updated your todo list, "+
			"and %d of %d items are still incomplete. ",
		itersSince, incomplete, total,
	))
	sb.WriteString("Update your todo list with `todo_write` to reflect progress so far - ")
	sb.WriteString("mark completed items as done, adjust remaining work, or remove items that are no longer relevant. ")
	sb.WriteString("An up-to-date plan helps you stay on track and avoid missed steps.")
	return sb.String()
}

// staleTaskBoardReminderText builds the reminder message when the structured
// task board (task_create/task_update) is the abandoned plan (r78).
func staleTaskBoardReminderText(incomplete, total, itersSince int) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf(
		"Task board sync: It's been %d iterations since you last updated the task board, "+
			"and %d of %d tasks are still incomplete. ",
		itersSince, incomplete, total,
	))
	sb.WriteString("Use `task_list` to review the board, then `task_update` to reflect progress - ")
	sb.WriteString("mark completed tasks, start unblocked ones (check the ready/blocked annotations), ")
	sb.WriteString("or restructure dependencies if the plan no longer matches reality.")
	return sb.String()
}

// checkHasIncompleteTasks reads the session's structured task board and returns
// the count of pending/in_progress tasks plus the total (r78). Returns zeros if
// the task_list tool or its manager is unavailable, or the board is empty/all done.
func (a *Agent) checkHasIncompleteTasks() (incompleteCount int, totalCount int) {
	t, ok := a.tools.Get("task_list")
	if !ok {
		return 0, 0
	}
	var mgr *task.Manager
	switch tl := t.(type) {
	case *tool.TaskListTool:
		mgr = tl.Manager
	case tool.TaskListTool:
		mgr = tl.Manager
	default:
		return 0, 0
	}
	if mgr == nil {
		return 0, 0
	}
	for _, tk := range mgr.List() {
		totalCount++
		if tk.Status == task.StatusPending || tk.Status == task.StatusInProgress {
			incompleteCount++
		}
	}
	return incompleteCount, totalCount
}

// parseTodoCount extracts the number of todo items from a todo_write tool-call
// input JSON. Returns 0 if parsing fails. This avoids re-reading the file -
// we can determine the count directly from the tool-call arguments.
func parseTodoCount(input json.RawMessage) int {
	var args struct {
		Todos []json.RawMessage `json:"todos"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return 0
	}
	return len(args.Todos)
}

// recordTaskBoardSync records a successful task_create/task_update call for
// staleness tracking (r78 consolidation - same detector as todo_write).
func (a *Agent) recordTaskBoardSync(iteration int) {
	a.todoStaleness.recordBoardUpdate(iteration)
}
