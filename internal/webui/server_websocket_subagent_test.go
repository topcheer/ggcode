package webui

import (
	"encoding/json"
	"testing"

	"github.com/topcheer/ggcode/internal/provider"
)

// 钉测(§6.1 二层):subagent_delta/done 帧转换——一等枚举到 WS 契约的映射面
func TestStreamEventToJSONSubagentFrames(t *testing.T) {
	delta := streamEventToJSON(provider.StreamEvent{
		Type:     provider.StreamEventSubagentDelta,
		Subagent: &provider.SubagentEvent{ID: "sa-1", Kind: "reasoning", Text: "thinking..."},
	})
	if delta["type"] != "subagent_delta" || delta["id"] != "sa-1" || delta["kind"] != "reasoning" {
		t.Fatalf("delta frame = %v", delta)
	}

	done := streamEventToJSON(provider.StreamEvent{
		Type:     provider.StreamEventSubagentDone,
		Subagent: &provider.SubagentEvent{ID: "sa-1", Result: "report", IsError: true},
	})
	if done["type"] != "subagent_done" || done["result"] != "report" || done["is_error"] != true {
		t.Fatalf("done frame = %v", done)
	}

	// JSON 序列化往返(WS 线上形态合法)
	if _, err := json.Marshal(done); err != nil {
		t.Fatalf("frame not serializable: %v", err)
	}

	// nil Subagent 防御:返回 nil(上层跳过)而非 panic
	if streamEventToJSON(provider.StreamEvent{Type: provider.StreamEventSubagentDelta}) != nil {
		t.Fatal("nil subagent should yield nil frame")
	}
}
