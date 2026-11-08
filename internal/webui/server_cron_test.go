package webui

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/topcheer/ggcode/internal/config"
)

func TestCronJobsReadOnly(t *testing.T) {
	ws := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)

	// 布景:按 workspace hash 分桶写一个 jobs 文件(复刻 cron.MigrateWorkspaceJobs 布局)
	h := sha256.Sum256([]byte(ws))
	dir := filepath.Join(home, ".ggcode", "cron-jobs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	jobs := []map[string]interface{}{
		{"id": "j1", "cron_expr": "*/20 * * * *", "prompt": "巡检", "recurring": true},
		{"id": "j2", "cron_expr": "0 9 * * *", "prompt": "日报", "recurring": false, "paused": true},
	}
	blob, _ := json.Marshal(jobs)
	if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("%x", h)+".json"), blob, 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := config.DefaultConfig()
	s := NewServer(cfg)
	s.SetSessionStore(nil, ws)

	req := httptest.NewRequest("GET", "/api/cron/jobs", nil)
	rec := httptest.NewRecorder()
	s.handleCronJobs(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}
	var out []map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out) != 2 {
		t.Fatalf("expected 2 jobs, got %d: %v", len(out), out)
	}
	if out[0]["cron_expr"] != "*/20 * * * *" {
		t.Errorf("cron_expr = %v", out[0]["cron_expr"])
	}

	// 无分桶文件:空数组而非错误
	s2 := NewServer(config.DefaultConfig())
	s2.SetSessionStore(nil, t.TempDir())
	req2 := httptest.NewRequest("GET", "/api/cron/jobs", nil)
	rec2 := httptest.NewRecorder()
	s2.handleCronJobs(rec2, req2)
	if rec2.Code != 200 {
		t.Fatalf("missing-bucket status = %d", rec2.Code)
	}
	var empty []map[string]interface{}
	if err := json.Unmarshal(rec2.Body.Bytes(), &empty); err != nil {
		t.Fatalf("missing-bucket body not a JSON list: %s", rec2.Body.String())
	}
	if len(empty) != 0 {
		t.Errorf("expected empty list, got %v", empty)
	}
}
