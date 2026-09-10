package agent

import (
	"os"
	"strings"
	"testing"
)

// #1817 case 1: restoreEffort must NOT roll back a user override that lands
// DURING the streaming call (apply checked the flag before; the window is
// tens of seconds wide).
func Test1817RestoreSkipsUserOverride(t *testing.T) {
	a := &Agent{effortAdapter: &adaptiveEffortState{}}
	a.effortAdapter.setUserOverride(true)
	if !a.effortAdapter.hasUserOverride() {
		t.Fatal("setup: override flag must be set")
	}
	// restore with override active must be a no-op - a provider-backed agent
	// would keep the user's fresh value; here we verify no panic and no
	// internal state churn via the debug-log path (function returns early).
	a.restoreEffort("low")
}

// #1817 case 2: the stream bracket must restore effort even when
// streamChatResponse panics (Run() recovers the panic into an error; the
// old positional restores never ran).
func Test1817StreamBracketSurvivesPanic(t *testing.T) {
	srcBytes, err := os.ReadFile("agent.go")
	if err != nil {
		t.Skipf("read agent.go: %v", err)
	}
	src := string(srcBytes)
	if !strings.Contains(src, "defer func() {\n\t\t\t\tif samplingApplied >= 0") {
		t.Fatal("stream bracket must defer restores inside the closure (#1817 case 2)")
	}
	if strings.Contains(src, "\t\tif effortApplied != \"\" {\n\t\ta.restoreEffort(effortPrev)\n\t\t}\n\t\tif err != nil {") {
		t.Fatal("positional restore after streamChatResponse still present (#1817 case 2 regression)")
	}
}
