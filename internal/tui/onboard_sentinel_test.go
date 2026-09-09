package tui

import "testing"

// #1892: the sentinel guard lifecycle, pinned via the real Update flow.
// Case a: a successful discovery must CLEAR modelsAreSentinel (it used to
// stay set forever, refusing a focused-enter even after real models
// arrived). Case b pins the guard predicate shape both enter branches use.
func TestOnboardDiscoverySuccessClearsSentinel(t *testing.T) {
	m := newOnboardModelForTest()
	// Placeholder state: discovery failed or has not run yet.
	m.modelsAreSentinel = true
	m.step = onboardStepModel
	m.discoverGen = 7

	m2, _ := m.Update(discoverResultMsg{models: []string{"glm-5", "glm-5.3-flash"}, gen: 7})
	pointee := m2.(*onboardModel)
	got := *pointee
	if got.modelsAreSentinel {
		t.Fatal("successful discovery must clear modelsAreSentinel (focused-enter stays refused forever otherwise)")
	}
	if len(got.allModels) != 2 {
		t.Fatalf("models not applied: %v", got.allModels)
	}

	// A stale-generation result (old endpoint) must not flip the flag off
	// on its own path semantics: sentinel stays whatever the current
	// generation set it to; here the fresh list already cleared it.
	m3 := newOnboardModelForTest()
	m3.modelsAreSentinel = true
	m3.step = onboardStepModel
	m3.discoverGen = 2
	m4, _ := m3.Update(discoverResultMsg{models: nil, gen: 1})
	if !m4.(*onboardModel).modelsAreSentinel {
		t.Fatal("failed/stale discovery must keep the sentinel flag (placeholder is still not a real model)")
	}
}

func newOnboardModelForTest() onboardModel {
	m := onboardModel{}
	m.step = onboardStepModel
	return m
}
