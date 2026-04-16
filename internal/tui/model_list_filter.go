package tui

import (
	"strconv"
	"strings"

	"charm.land/bubbles/v2/textinput"
)

const maxVisibleModelRows = 10

type modelListWindow struct {
	items         []string
	indices       []int
	selected      int
	filteredCount int
	totalCount    int
	hiddenBefore  int
	hiddenAfter   int
	filterEnabled bool
	filterActive  bool
	filterValue   string
}

func newModelFilterInput(lang Language) textinput.Model {
	ti := textinput.New()
	ti.Prompt = tr(lang, "panel.model_filter.prompt")
	ti.Placeholder = tr(lang, "panel.model_filter.placeholder")
	return ti
}

func shouldEnableModelFilter(models []string) bool {
	return len(models) > maxVisibleModelRows
}

func buildModelListWindow(models []string, selected int, filter textinput.Model) modelListWindow {
	query := strings.TrimSpace(strings.ToLower(filter.Value()))
	filteredItems := make([]string, 0, len(models))
	filteredIndices := make([]int, 0, len(models))
	for i, model := range models {
		if query != "" && !strings.Contains(strings.ToLower(model), query) {
			continue
		}
		filteredItems = append(filteredItems, model)
		filteredIndices = append(filteredIndices, i)
	}

	window := modelListWindow{
		filteredCount: len(filteredItems),
		totalCount:    len(models),
		filterEnabled: shouldEnableModelFilter(models),
		filterActive:  filter.Focused(),
		filterValue:   filter.Value(),
	}
	if len(filteredItems) == 0 {
		window.selected = -1
		return window
	}

	selectedPos := indexOfInt(filteredIndices, selected)
	if selectedPos < 0 {
		selectedPos = 0
	}
	start := 0
	if len(filteredItems) > maxVisibleModelRows {
		start = selectedPos - maxVisibleModelRows/2
		if start < 0 {
			start = 0
		}
		maxStart := len(filteredItems) - maxVisibleModelRows
		if start > maxStart {
			start = maxStart
		}
	}
	end := len(filteredItems)
	if end > start+maxVisibleModelRows {
		end = start + maxVisibleModelRows
	}

	window.items = filteredItems[start:end]
	window.indices = filteredIndices[start:end]
	window.selected = selectedPos - start
	window.hiddenBefore = start
	window.hiddenAfter = len(filteredItems) - end
	return window
}

func syncModelSelection(selected *int, models []string, filter textinput.Model) {
	window := buildModelListWindow(models, *selected, filter)
	if len(window.indices) == 0 {
		*selected = -1
		return
	}
	if *selected >= 0 && indexOfInt(window.indices, *selected) >= 0 {
		return
	}
	fullItems, fullIndices := filteredModelItems(models, filter.Value())
	if len(fullItems) == 0 || len(fullIndices) == 0 {
		*selected = -1
		return
	}
	*selected = fullIndices[0]
}

func filteredModelItems(models []string, filterValue string) ([]string, []int) {
	query := strings.TrimSpace(strings.ToLower(filterValue))
	items := make([]string, 0, len(models))
	indices := make([]int, 0, len(models))
	for i, model := range models {
		if query != "" && !strings.Contains(strings.ToLower(model), query) {
			continue
		}
		items = append(items, model)
		indices = append(indices, i)
	}
	return items, indices
}

func moveFilteredModelSelection(selected *int, models []string, filter textinput.Model, delta int) {
	_, indices := filteredModelItems(models, filter.Value())
	if len(indices) == 0 {
		*selected = -1
		return
	}
	pos := indexOfInt(indices, *selected)
	if pos < 0 {
		pos = 0
	}
	pos = (pos + delta + len(indices)) % len(indices)
	*selected = indices[pos]
}

func renderModelListWindow(renderList func([]string, int, bool) string, window modelListWindow, focused bool, lang Language) string {
	if window.totalCount == 0 {
		return "  " + tr(lang, "panel.model_list.none")
	}
	if window.filteredCount == 0 {
		return "  " + tr(lang, "panel.model_list.no_matches")
	}
	rows := make([]string, 0, len(window.items)+3)
	if window.filterEnabled {
		rows = append(rows, "  "+tr(lang, "panel.model_list.showing", window.filteredCount, window.totalCount))
	}
	if window.hiddenBefore > 0 {
		rows = append(rows, "  ... "+tr(lang, "panel.model_list.hidden_above", window.hiddenBefore))
	}
	rows = append(rows, strings.Split(renderList(window.items, window.selected, focused), "\n")...)
	if window.hiddenAfter > 0 {
		rows = append(rows, "  ... "+tr(lang, "panel.model_list.hidden_more", window.hiddenAfter))
	}
	return strings.Join(rows, "\n")
}

func countModelBodyRows(window modelListWindow) int {
	rows := 0
	if window.filterEnabled {
		rows++
	}
	switch {
	case window.totalCount == 0, window.filteredCount == 0:
		return rows + 1
	}
	if window.filterEnabled {
		rows++
	}
	if window.hiddenBefore > 0 {
		rows++
	}
	rows += len(window.items)
	if window.hiddenAfter > 0 {
		rows++
	}
	return rows
}

func modelFilterConsumesKey(msg string) bool {
	switch msg {
	case "up", "down", "tab", "shift+tab", "enter", "esc":
		return false
	default:
		return true
	}
}

func itoa(v int) string { return strconv.Itoa(v) }

func indexOfInt(values []int, target int) int {
	for i, value := range values {
		if value == target {
			return i
		}
	}
	return -1
}
