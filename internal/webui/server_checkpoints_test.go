package webui

import (
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/topcheer/ggcode/internal/checkpoint"
	"github.com/topcheer/ggcode/internal/config"
)

// 用真实写入方(NewPersistentManager + Save)构造 journal,钉住真实落盘契约:
// journalEvent 信封行 {"type":"save","cp":{...},"time":...}。
// 旧版 fixture 手写扁平行 {"id":...} 是写入方从不产出的格式,曾掩盖解析失效。
func TestCheckpointsReadOnly(t *testing.T) {
	home := t.TempDir()
	ws := t.TempDir()
	// checkpoint.defaultJournalDir 走 os.UserConfigDir:darwin 看 HOME,linux 看 XDG_CONFIG_HOME
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", home)

	mgr := checkpoint.NewPersistentManager(50, ws)
	mgr.StartRun("run-1")
	mgr.Save(filepath.Join(ws, "a.go"), "old-a", "new-a", "edit_file")
	mgr.Save(filepath.Join(ws, "b.go"), "old-b", "new-b", "write_file")

	cfg := config.DefaultConfig()
	s := NewServer(cfg)
	s.SetSessionStore(nil, ws)

	body := func(t *testing.T, srv *Server) []map[string]interface{} {
		t.Helper()
		rec := httptest.NewRecorder()
		srv.handleCheckpoints(rec, httptest.NewRequest("GET", "/api/checkpoints", nil))
		if rec.Code != 200 {
			t.Fatalf("status = %d", rec.Code)
		}
		var out []map[string]interface{}
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("body not JSON list: %s", rec.Body.String())
		}
		return out
	}

	out := body(t, s)
	if len(out) != 2 {
		t.Fatalf("expected 2 checkpoints from real journal, got %d", len(out))
	}
	// 最近优先:b.go 后保存
	if got, _ := out[0]["file_path"].(string); !filepath.IsAbs(got) || filepath.Base(got) != "b.go" {
		t.Errorf("expected newest first (b.go), got %v", out[0]["file_path"])
	}
	if out[0]["tool_call"] != "write_file" || out[0]["id"] == "" || out[0]["timestamp"] == "" {
		t.Errorf("expected populated id/tool_call/timestamp, got %v", out[0])
	}
	if out[0]["run_id"] != "run-1" {
		t.Errorf("expected run_id=run-1, got %v", out[0]["run_id"])
	}

	// undo 后:被撤销的检查点不得再出现(事件回放语义,而非收集所有 save 行)
	if _, err := mgr.Undo("user"); err != nil {
		t.Fatal(err)
	}
	out = body(t, s)
	if len(out) != 1 {
		t.Fatalf("expected 1 checkpoint after undo, got %d", len(out))
	}
	if got, _ := out[0]["file_path"].(string); filepath.Base(got) != "a.go" {
		t.Errorf("expected surviving a.go after undo of b.go, got %v", out[0]["file_path"])
	}

	// 无 journal 的 workspace:空列表
	s2 := NewServer(config.DefaultConfig())
	s2.SetSessionStore(nil, t.TempDir())
	if out := body(t, s2); len(out) != 0 {
		t.Errorf("expected empty for missing journal, got %v", out)
	}
}
