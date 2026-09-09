package tui

import "testing"

// #1748: discovery arriving while a NARROWING filter is active must clamp
// the cursor against the FILTERED list - consumers index
// modelFiltered[cursor]. The old full-list clamp left the cursor past the
// filtered tail; Enter without moving the cursor panicked out of range.
func TestOnboardDiscoverClampsCursorToFilteredList1748(t *testing.T) {
	m := newOnboardModelForTest()
	m.step = onboardStepModel
	m.discoverGen = 3
	// Pre-discovery list deep enough that the stale cursor is large.
	m.models = []string{"m0", "m1", "m2", "m3", "m4", "m5"}
	m.modelCursor = 5
	// Narrowing filter typed DURING discovery: only glm entries survive.
	m.modelFilter.SetValue("glm")
	m.applyModelFilter()

	m2, _ := m.Update(discoverResultMsg{
		models: []string{"a0", "a1", "a2", "a3", "a4", "glm5", "glm5f"},
		gen:    3,
	})
	got := *m2.(*onboardModel)
	if got.modelCursor >= len(got.modelFiltered) {
		t.Fatalf("cursor %d must be clamped to filtered list (len %d)", got.modelCursor, len(got.modelFiltered))
	}
	// The exact consumer indexing path must not panic.
	_ = got.models[got.modelFiltered[got.modelCursor]]
}
