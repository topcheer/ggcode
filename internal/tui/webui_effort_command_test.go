package tui

import (
	"testing"
)

// 钉测(/effort 命令分发):
// 1) /effort 路由进 handleEffortCommand(chatList 为 nil 时安全 no-op,不 panic)
// 2) 非法枚举走拒绝分支(不触碰 agent),返回 cmd 为 nil
// 3) reasoningEffortCycle 枚举与 webui EFFORTS 前端常量对齐(minimal,low,medium,high)
func TestHandleEffortCommandDispatch(t *testing.T) {
	m := newTestModel()
	m.chatList = nil
	m.agent = nil

	if cmd := m.handleCommandWithDisplay("/effort bogus", false); cmd != nil {
		t.Fatal("invalid effort must not enqueue any cmd")
	}
	if cmd := m.handleCommandWithDisplay("/effort", false); cmd != nil {
		t.Fatal("cycle path with nil agent must not enqueue any cmd")
	}

	// 后端真枚举:首元素为空串(未设置态,不算可选值),可选值为 low/medium/high——
	// 与 webui ChatControls EFFORTS 常量必须一致
	want := "low|medium|high"
	got := ""
	for _, e := range reasoningEffortCycle {
		if e == "" {
			continue // 未设置态不是下拉可选值
		}
		if got != "" {
			got += "|"
		}
		got += e
	}
	if got != want {
		t.Fatalf("reasoningEffortCycle = %q, want %q (webui ChatControls EFFORTS must match)", got, want)
	}
}
