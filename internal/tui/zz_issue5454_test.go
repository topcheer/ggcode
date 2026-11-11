package tui

import (
	"testing"

	"github.com/topcheer/ggcode/internal/permission"
)

// TestModeLiveSurvivesValueCopy documents the #5454 hazard and the fix's
// contract: bubbletea evolves the Model through value copies, so m.mode
// written on a copy never reaches the REPL-held original - but the shared
// modeLive pointer does.
func TestModeLiveSurvivesValueCopy(t *testing.T) {
	orig := Model{mode: permission.SupervisedMode, modeLive: &modeLiveState{}}
	copyM := orig // the value copy tea.NewProgram semantics imply

	copyM.mode = permission.BypassMode
	copyM.modeLive.set(permission.BypassMode)

	if orig.mode != permission.SupervisedMode {
		t.Fatalf("premise broken: original mode mutated through copy (%s)", orig.mode)
	}
	got, ok := orig.modeLive.get()
	if !ok || got != permission.BypassMode {
		t.Fatalf("modeLive did not propagate the copy's write: got=%s ok=%v", got, ok)
	}
}

// TestModeLiveNilGuards: zero-value Models (tests, partial construction)
// must tolerate absent stores on both set and get.
func TestModeLiveNilGuards(t *testing.T) {
	var live *modeLiveState
	live.set(permission.BypassMode) // must not panic
	if _, ok := live.get(); ok {
		t.Fatal("nil store must report ok=false")
	}
}

// TestModeLiveRoundtrip: set/get roundtrip under the mutex store.
func TestModeLiveRoundtrip(t *testing.T) {
	live := &modeLiveState{}
	for _, m := range []permission.PermissionMode{permission.PlanMode, permission.BypassMode, permission.SupervisedMode} {
		live.set(m)
		got, ok := live.get()
		if !ok || got != m {
			t.Fatalf("roundtrip: set %s, got %s (ok=%v)", m, got, ok)
		}
	}
}
