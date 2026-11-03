package agent

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/tool"
)

const (
	staleTodoThreshold = 8

	activeStaleTodoThreshold = 5

	maxStaleTodoReminders = 5

	reminderGraceIters = 4
)

type todoStalenessState struct {
	lastUpdateIter int

	hasTodos bool

	remindedCount int

	lastRemindedIter int

	reminderActive bool

	activeGoal bool
}

func newTodoStalenessState() *todoStalenessState {
	return &todoStalenessState{lastUpdateIter: -1, lastRemindedIter: -1}
}

func (s *todoStalenessState) reset() {
	s.lastUpdateIter = -1
	s.hasTodos = false
	s.remindedCount = 0
	s.lastRemindedIter = -1
	s.reminderActive = false
	s.activeGoal = false
}

func (s *todoStalenessState) recordUpdate(iteration int, todoCount int) {
	s.lastUpdateIter = iteration
	s.hasTodos = todoCount > 0
	s.reminderActive = false
	s.remindedCount = 0
}

func (s *todoStalenessState) shouldRemind(iteration int, hasIncomplete bool) bool {
	if !s.hasTodos || !hasIncomplete {
		return false
	}
	if s.remindedCount >= maxStaleTodoReminders {
		return false
	}
	if s.reminderActive && iteration-s.lastRemindedIter < reminderGraceIters {
		return false
	}
	if s.lastUpdateIter < 0 {
		return false
	}
	return iteration-s.lastUpdateIter >= staleThresholdFor(s.activeGoal)
}

func staleThresholdFor(activeGoal bool) int {
	if activeGoal {
		return activeStaleTodoThreshold
	}
	return staleTodoThreshold
}

func (s *todoStalenessState) markReminded(iteration int) {
	s.reminderActive = true
	s.lastRemindedIter = iteration
	s.remindedCount++
}

func (a *Agent) recordTodoStalenessUpdate(iteration int, todoCount int) {
	a.todoStaleness.recordUpdate(iteration, todoCount)
}

func (a *Agent) resetTodoStaleness() {
	a.todoStaleness.reset()
}

func (a *Agent) maybeRemindStaleTodo(iteration int) string {

	if a.todoStaleness.lastUpdateIter < 0 || !a.todoStaleness.hasTodos {
		return ""
	}

	if iteration-a.todoStaleness.lastUpdateIter < staleTodoThreshold {
		return ""
	}

	if a.todoStaleness.remindedCount >= maxStaleTodoReminders {
		return ""
	}

	if a.todoStaleness.reminderActive && iteration-a.todoStaleness.lastRemindedIter < reminderGraceIters {
		return ""
	}

	hasIncomplete, incompleteCount, totalCount, items := a.checkHasIncompleteTodos()
	if !hasIncomplete {
		return ""
	}

	a.todoStaleness.activeGoal = a.checkActiveGoalTodos()
	if itersSince := iteration - a.todoStaleness.lastUpdateIter; itersSince < staleThresholdFor(a.todoStaleness.activeGoal) {
		return ""
	}

	a.todoStaleness.markReminded(iteration)
	itersSince := iteration - a.todoStaleness.lastUpdateIter
	debug.Log("todo_staleness", "stale todos detected: %d incomplete of %d, %d iterations since last update (threshold %d), injecting reminder (%d/%d)",
		incompleteCount, totalCount, itersSince, staleTodoThreshold, a.todoStaleness.remindedCount, maxStaleTodoReminders)

	return staleTodoReminderText(incompleteCount, totalCount, itersSince, items)
}

func (a *Agent) checkActiveGoalTodos() bool {
	t, ok := a.tools.Get("todo_write")
	if !ok {
		return false
	}
	tw, ok := t.(*tool.TodoWrite)
	if !ok {
		return false
	}
	todos, err := tw.ListTodos()
	if err != nil {
		return false
	}
	for _, td := range todos {
		if td.Status == "in_progress" {
			return true
		}
	}
	return false
}

func (a *Agent) checkHasIncompleteTodos() (hasIncomplete bool, incompleteCount int, totalCount int, items []string) {
	t, ok := a.tools.Get("todo_write")
	if !ok {
		return false, 0, 0, nil
	}
	tw, ok := t.(*tool.TodoWrite)
	if !ok {
		return false, 0, 0, nil
	}
	todos, err := tw.ListTodos()
	if err != nil || len(todos) == 0 {
		return false, 0, 0, nil
	}
	totalCount = len(todos)
	for _, td := range todos {
		if td.Status == "pending" || td.Status == "in_progress" {
			incompleteCount++
			items = append(items, fmt.Sprintf("[%s] %s", td.Status, td.Content))
		}
	}
	return incompleteCount > 0, incompleteCount, totalCount, items
}

func staleTodoReminderText(incomplete, total, itersSince int, items []string) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf(
		"Todo list sync: It's been %d iterations since you last updated your todo list, "+
			"and %d of %d items are still incomplete:\n",
		itersSince, incomplete, total,
	))
	for _, it := range items {
		sb.WriteString("  - " + it + "\n")
	}
	sb.WriteString("Update your todo list with `todo_write` to reflect progress so far - ")
	sb.WriteString("mark completed items as done, adjust remaining work, or remove items that are no longer relevant. ")
	sb.WriteString("An up-to-date plan helps you stay on track and avoid missed steps.")
	return sb.String()
}

func parseTodoCount(input json.RawMessage) int {
	var args struct {
		Todos []json.RawMessage `json:"todos"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return 0
	}
	return len(args.Todos)
}
