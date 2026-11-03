package agent

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/topcheer/ggcode/internal/debug"
)

const (
	todoDropMaxFires = 1

	todoDropSimilarity = 0.6
)

type todoItem struct {
	ID      string `json:"id"`
	Content string `json:"content"`
	Status  string `json:"status"`
}

type todoDropState struct {
	mu        sync.Mutex
	prevItems []todoItem
	fireCount int
	hasFired  bool
}

func newTodoDropState() *todoDropState {
	return &todoDropState{}
}

func (t *todoDropState) reset() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.prevItems = nil
	t.fireCount = 0
	t.hasFired = false
}

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

func (t *todoDropState) findDroppedItems(prev, curr []todoItem) []todoItem {
	var dropped []todoItem
	for _, p := range prev {

		status := strings.ToLower(strings.TrimSpace(p.Status))
		if status == "done" || status == "completed" {
			continue
		}

		if t.itemSurvives(p, curr) {
			continue
		}
		dropped = append(dropped, p)
	}
	return dropped
}

func (t *todoDropState) itemSurvives(item todoItem, curr []todoItem) bool {
	for _, c := range curr {

		if item.ID != "" && c.ID == item.ID {
			return true
		}

		if item.Content != "" && c.Content != "" {
			sim := todoDropSimilarityOf(item.Content, c.Content)
			if sim >= todoDropSimilarity {
				return true
			}
		}
	}
	return false
}

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

func parseTodoItems(input json.RawMessage) []todoItem {
	var args struct {
		Todos []todoItem `json:"todos"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return nil
	}
	return args.Todos
}

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

func hasCJK(s string) bool {
	for _, r := range s {
		if r >= 0x4E00 && r <= 0x9FFF {
			return true
		}
	}
	return false
}

func cjkBigramSet(s string) map[string]bool {
	runes := []rune(strings.ToLower(s))
	set := make(map[string]bool, len(runes))
	for i := 0; i+1 < len(runes); i++ {
		set[string(runes[i:i+2])] = true
	}
	if len(runes) > 0 {
		set[string(runes[len(runes)-1:])] = true
	}
	return set
}

func wordSet(s string) map[string]bool {
	words := strings.Fields(strings.ToLower(s))
	set := make(map[string]bool, len(words))
	for _, w := range words {

		w = strings.Trim(w, ".,;:!?\"'()[]{}")
		if w != "" {
			set[w] = true
		}
	}
	return set
}

func todoDropSimilarityOf(a, b string) float64 {
	if hasCJK(a) || hasCJK(b) {
		return jaccardOfSets(cjkBigramSet(a), cjkBigramSet(b))
	}
	return todoDropJaccard(a, b)
}

func jaccardOfSets(a, b map[string]bool) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	intersection := 0
	for k := range a {
		if b[k] {
			intersection++
		}
	}
	union := len(a) + len(b) - intersection
	if union == 0 {
		return 0
	}
	return float64(intersection) / float64(union)
}

func truncateStr(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	if maxLen <= 3 {
		return string(runes[:maxLen])
	}
	return string(runes[:maxLen-3]) + "..."
}

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

func (a *Agent) resetTodoDrop() {
	if a.todoDrop != nil {
		a.todoDrop.reset()
	}
}
