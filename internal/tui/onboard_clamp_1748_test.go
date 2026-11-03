package tui

import "testing"

func TestOnboardDiscoverClampsCursorToFilteredList1748(t *testing.T) {
	m := newOnboardModelForTest()
	m.step = onboardStepModel
	m.discoverGen = 3

	m.models = []string{"m0", "m1", "m2", "m3", "m4", "m5"}
	m.modelCursor = 5

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

	_ = got.models[got.modelFiltered[got.modelCursor]]
}
