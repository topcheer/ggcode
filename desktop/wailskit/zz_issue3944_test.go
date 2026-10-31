//go:build goolm

package wailskit

// #3944 probes: SetPermissionMode must switch the mode IN PLACE on the
// live policy (keeping runtime-built always_allow/override layers) instead
// of rebuilding and discarding them; the rebuild stays only as the
// no-existing-policy bootstrap fallback.

import (
	"testing"

	"github.com/topcheer/ggcode/internal/agent"
	"github.com/topcheer/ggcode/internal/permission"
	"github.com/topcheer/ggcode/internal/session"
)

func TestIssue3944_ModeSwitchKeepsLivePolicy(t *testing.T) {
	b := &ChatBridge{currentSes: &session.Session{}}
	ag := agent.NewAgent(nil, nil, "sys", 5)

	// The runtime-built layer: session-approved always_allow pattern that
	// lived only in memory on the live policy.
	live := permission.NewConfigPolicyWithMode(nil, []string{"/wd"}, permission.AutoMode)
	live.AllowCommandPattern("make *")
	ag.SetPermissionPolicy(live)
	b.mu.Lock()
	b.agent = ag
	b.workingDir = "/wd"
	b.mu.Unlock()

	b.SetPermissionMode("supervised")

	cur := ag.PermissionPolicy()
	if cur == nil {
		t.Fatal("policy lost")
	}
	if cur != live {
		t.Fatal("mode switch must keep the LIVE policy instance (#3944) - runtime always_allow/override layers must survive")
	}
	if got := live.CurrentMode(); got != permission.SupervisedMode {
		t.Fatalf("mode must actually switch in place, got %v", got)
	}
	if rs := live.CommandRuleSet(); rs == nil {
		t.Fatal("command rule set dropped")
	}
}
