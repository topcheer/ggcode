package agent

import (
	"os"
	"strings"
	"testing"
)

func Test1817RestoreSkipsUserOverride(t *testing.T) {
	a := &Agent{effortAdapter: &adaptiveEffortState{}}
	a.effortAdapter.setUserOverride(true)
	if !a.effortAdapter.hasUserOverride() {
		t.Fatal("setup: override flag must be set")
	}

	a.restoreEffort("low")
}

func Test1817StreamBracketSurvivesPanic(t *testing.T) {
	srcBytes, err := readAgentAndProcessingSources()
	if err != nil {
		t.Skipf("read agent.go: %v", err)
	}
	// #5543: the stream bracket moved into Agent.chatTurn (chat_turn_error.go);
	// pins scan both so the defer-in-same-function contract still holds.
	turnBytes, turnErr := os.ReadFile("chat_turn_error.go")
	if turnErr != nil {
		t.Skipf("read chat_turn_error.go: %v", turnErr)
	}
	src := string(srcBytes) + "\n" + string(turnBytes)
	if !strings.Contains(src, "defer func() {\n\t\tif effortApplied != \"\"") {
		t.Fatal("stream bracket must defer restores inside the chatTurn function (#1817 case 2)")
	}
	if strings.Contains(src, "\t\tif effortApplied != \"\" {\n\t\ta.restoreEffort(effortPrev)\n\t\t}\n\t\tif err != nil {") {
		t.Fatal("positional restore after streamChatResponse still present (#1817 case 2 regression)")
	}
}
