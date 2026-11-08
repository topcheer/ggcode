package tui

import (
	"testing"

	"github.com/topcheer/ggcode/internal/permission"
)

// 钉测(快照真值源回归):REPL.model 是 bubbletea 值语义的启动旧拷贝,
// 运行期 /mode 经 ConfigPolicy 指针切换后 m.mode 字段并不回写 r.model——
// 历史快照因此永远回旧值(用户报障:界面 supervised / API bypass)。
// 快照必须读指针共享的真值源:mode←policy、reasoning_effort←r.agent。
func TestChatControlsSnapshotReadsLiveSources(t *testing.T) {
	policy := permission.NewConfigPolicy(nil, nil)
	policy.SetMode(permission.SupervisedMode)
	r := &REPL{
		model: Model{mode: permission.BypassMode, policy: policy}, // 模拟启动旧拷贝残留 bypass
	}
	snap := r.ChatControlsSnapshot()
	if got := snap["mode"]; got != "supervised" {
		t.Fatalf("mode should read live policy (supervised), got %q", got)
	}

	// 经指针切换(bypass 语义同 handleModeCommand 内 cp.SetMode)
	policy.SetMode(permission.BypassMode)
	if got := r.ChatControlsSnapshot()["mode"]; got != "bypass" {
		t.Fatalf("mode should follow policy switch to bypass, got %q", got)
	}

	// reasoning_effort:REPL 直达 agent 指针非空时取真值(agent 注入需重量级构造,空值分支已由 e2e 覆盖)
}
