package chat

// sa-86 tests: second-level disclosure - expanded lifts the render caps,
// cache invalidation, list toggle, truncation hint affordance.

import (
	"strings"
	"testing"
)

func longBodyLines(n int) string {
	var sb strings.Builder
	for i := 0; i < n; i++ {
		sb.WriteString(strings.Repeat("x", 20))
		sb.WriteString("\n")
	}
	return strings.TrimSuffix(sb.String(), "\n")
}

func newExpandedTestItem(result string, isErr bool) *GenericToolItem {
	it := NewGenericToolItem("t1", "run", StatusSuccess, "go test ./...", DefaultStyles())
	it.SetResult(result, isErr)
	return it
}

// Collapsed body caps at ToolBodyMaxLines; expanded lifts the cap and
// keeps every line.
func TestDisclosure_ExpandLiftsCap(t *testing.T) {
	body := longBodyLines(ToolBodyMaxLines * 3)
	it := newExpandedTestItem(body, false)

	collapsed := it.RenderBody(80)
	if !strings.Contains(collapsed, "more lines") {
		t.Fatalf("collapsed render must show truncation hint:\n%s", collapsed)
	}
	if got := strings.Count(collapsed, "\n"); got > ToolBodyMaxLines+1 {
		t.Fatalf("collapsed render must cap at %d lines (+hint), got %d", ToolBodyMaxLines, got)
	}

	it.ToggleExpanded()
	expanded := it.RenderBody(80)
	for i := 0; i < ToolBodyMaxLines*3; i++ {
		if !strings.Contains(expanded, strings.Repeat("x", 20)) && i == 0 {
			t.Fatalf("expanded render lost content")
		}
	}
	if strings.Contains(expanded, "more lines (alt+e)") {
		t.Fatalf("expanded render must not show the truncation hint:\n%s", expanded[:200])
	}
	if want := ToolBodyMaxLines * 3; strings.Count(expanded, "\n") < want-2 {
		t.Fatalf("expanded render must keep all ~%d lines, got %d", want, strings.Count(expanded, "\n"))
	}
}

// Error path: collapsed caps with hint, expanded keeps everything.
func TestDisclosure_ErrorPathExpand(t *testing.T) {
	body := longBodyLines(ToolBodyMaxLines * 2)
	it := newExpandedTestItem(body, true)

	collapsed := it.RenderBody(80)
	if !strings.Contains(collapsed, "more lines (alt+e)") {
		t.Fatalf("collapsed error render must carry the alt+e hint:\n%s", collapsed)
	}
	it.ToggleExpanded()
	expanded := it.RenderBody(80)
	if strings.Contains(expanded, "more lines (alt+e)") {
		t.Fatalf("expanded error render must not carry the hint")
	}
	if strings.Count(expanded, "\n") < ToolBodyMaxLines*2-2 {
		t.Fatalf("expanded error render lost lines: %d", strings.Count(expanded, "\n"))
	}
}

// ToggleExpanded invalidates the render cache so Height recomputes.
func TestDisclosure_CacheInvalidation(t *testing.T) {
	it := newExpandedTestItem(longBodyLines(ToolBodyMaxLines*2), false)
	before := it.Height(80)
	it.Render(80) // fill cache
	it.ToggleExpanded()
	after := it.Height(80)
	if after <= before {
		t.Fatalf("expanded height must exceed collapsed: before=%d after=%d", before, after)
	}
	it.ToggleExpanded()
	if round := it.Height(80); round != before {
		t.Fatalf("collapse must restore height: %d != %d", round, before)
	}
}

// List toggle flips the most recent expandable item; empty list is a no-op.
func TestDisclosure_ListToggle(t *testing.T) {
	var l List
	if l.ToggleLastExpandable() {
		t.Fatal("empty list must return false")
	}
	l.Append(newExpandedTestItem(longBodyLines(50), false))
	l.Append(NewAssistantItem("not a tool", DefaultStyles()))
	if !l.ToggleLastExpandable() {
		t.Fatal("list with a tool item must toggle")
	}
	it, ok := l.FindByID("t1").(*GenericToolItem)
	if !ok {
		t.Fatal("tool item must still be present")
	}
	if ex := it.Expanded(); !ex {
		t.Fatal("most recent expandable item must now be expanded")
	}
	// Toggling again collapses: the assistant item in between is skipped.
	if !l.ToggleLastExpandable() {
		t.Fatal("second toggle must find the same tool item")
	}
	if ex := it.Expanded(); ex {
		t.Fatal("second toggle must collapse")
	}
}

// FormatBody hint carries the alt+e affordance.
func TestDisclosure_HintCarriesKey(t *testing.T) {
	out, truncated := FormatBody(longBodyLines(30), 80, ToolBodyMaxLines)
	if !truncated {
		t.Fatal("expected truncation")
	}
	if !strings.Contains(out, "(alt+e)") {
		t.Fatalf("hint must advertise the key:\n%s", out)
	}
}
