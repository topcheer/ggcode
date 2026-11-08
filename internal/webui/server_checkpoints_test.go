package webui

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/topcheer/ggcode/internal/config"
)

func TestCheckpointsReadOnly(t *testing.T) {
	ws := t.TempDir()
	home := t.TempDir()
	// darwin 的 os.UserConfigDir 忽略 XDG_CONFIG_HOME——直接注入可替换变量
	orig := userConfigDir
	userConfigDir = func() (string, error) { return home, nil }
	t.Cleanup(func() { userConfigDir = orig })

	// 布景:复刻 internal/checkpoint journal 布局
	// os.UserConfigDir()/ggcode/checkpoints/<hex[:2]>/<hex16>.jsonl
	sum := sha256.Sum256([]byte(ws))
	name := hex.EncodeToString(sum[:8]) + ".jsonl"
	dir := filepath.Join(home, "ggcode", "checkpoints", name[:2])
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	lines := []string{
		`{"id":"c1","file_path":"a.go","tool_call":"edit_file","timestamp":"2026-11-08T01:00:00Z"}`,
		`{"id":"c2","file_path":"b.go","tool_call":"write_file","timestamp":"2026-11-08T02:00:00Z"}`,
		"corrupted-line-not-json",
		``,
	}
	blob := ""
	for _, l := range lines {
		blob += l + "\n"
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(blob), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := config.DefaultConfig()
	s := NewServer(cfg)
	s.SetSessionStore(nil, ws)

	req := httptest.NewRequest("GET", "/api/checkpoints", nil)
	rec := httptest.NewRecorder()
	s.handleCheckpoints(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}
	var out []map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("body not JSON list: %s", rec.Body.String())
	}
	if len(out) != 2 {
		t.Fatalf("expected 2 checkpoints (corrupt lines skipped), got %d", len(out))
	}
	// 最近优先
	if out[0]["id"] != "c2" {
		t.Errorf("expected newest first (c2), got %v", out[0]["id"])
	}

	// 无 journal:空列表
	s2 := NewServer(config.DefaultConfig())
	s2.SetSessionStore(nil, t.TempDir())
	rec2 := httptest.NewRecorder()
	s2.handleCheckpoints(rec2, httptest.NewRequest("GET", "/api/checkpoints", nil))
	var empty []map[string]interface{}
	if err := json.Unmarshal(rec2.Body.Bytes(), &empty); err != nil {
		t.Fatalf("missing-journal body not JSON list: %s", rec2.Body.String())
	}
	if len(empty) != 0 {
		t.Errorf("expected empty, got %v", empty)
	}
}
