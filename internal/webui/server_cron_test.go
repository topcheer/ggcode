package webui

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/topcheer/ggcode/internal/config"
)

// 钉测契约(#cron-display-fix):真实存储是 ~/.ggcode/cron-jobs/<timestamp-id>.json
// 每任务一文件、{"jobs":[...]} 包裹(internal/cron Scheduler 布局)。
// handler 扫描目录聚合全部任务;HOME 隔离而非 workspace 键过滤。
func TestCronJobsReadOnly(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	dir := filepath.Join(home, ".ggcode", "cron-jobs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// 布景:复刻 scheduler 真实布局——两个任务文件,wrapped 形态
	jobs := []map[string]interface{}{
		{"id": "j1", "cron_expr": "*/20 * * * *", "prompt": "巡检", "recurring": true},
		{"id": "j2", "cron_expr": "0 9 * * *", "prompt": "日报", "recurring": false, "paused": true},
	}
	blob, _ := json.Marshal(map[string]interface{}{"jobs": jobs})
	if err := os.WriteFile(filepath.Join(dir, "20260623-212644-f183fed82fddf847.json"), blob, 0o644); err != nil {
		t.Fatal(err)
	}
	solo, _ := json.Marshal(map[string]interface{}{"jobs": []map[string]interface{}{
		{"id": "j0", "cron_expr": "@hourly", "prompt": "单任务文件", "recurring": true},
	}})
	if err := os.WriteFile(filepath.Join(dir, "20260701-090000-0123456789abcdef.json"), solo, 0o644); err != nil {
		t.Fatal(err)
	}
	// 噪音:.flock 与非 json 不应被聚合
	if err := os.WriteFile(filepath.Join(dir, "20260623-212644-f183fed82fddf847.json.flock"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := config.DefaultConfig()
	s := NewServer(cfg)
	s.SetSessionStore(nil, t.TempDir())

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
	if len(out) != 3 {
		t.Fatalf("expected 3 jobs across 2 files, got %d: %v", len(out), out)
	}
	// 排序按 ID: j0 < j1 < j2
	if out[0]["id"] != "j0" || out[1]["id"] != "j1" || out[2]["id"] != "j2" {
		t.Errorf("id sort wrong: %v", out)
	}
	if out[2]["cron_expr"] != "0 9 * * *" {
		t.Errorf("cron_expr = %v", out[2]["cron_expr"])
	}
	if out[2]["paused"] != true {
		t.Errorf("paused flag lost: %v", out[2])
	}

	// 无 cron-jobs 目录:空数组而非错误
	t.Setenv("HOME", t.TempDir())
	s2 := NewServer(config.DefaultConfig())
	s2.SetSessionStore(nil, t.TempDir())
	req2 := httptest.NewRequest("GET", "/api/cron/jobs", nil)
	rec2 := httptest.NewRecorder()
	s2.handleCronJobs(rec2, req2)
	if rec2.Code != 200 {
		t.Fatalf("missing-dir status = %d", rec2.Code)
	}
	var empty []map[string]interface{}
	if err := json.Unmarshal(rec2.Body.Bytes(), &empty); err != nil {
		t.Fatalf("missing-dir body not a JSON list: %s", rec2.Body.String())
	}
	if len(empty) != 0 {
		t.Errorf("expected empty list, got %v", empty)
	}
}

// 钉测(fn 注入优先):SetCronListFn 后真相源是活 scheduler 快照,不再扫磁盘
func TestCronJobsListFnPriority(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	// 布景:磁盘上放干扰任务(回落面会扫到,fn 注入后必须被忽略)
	dir := filepath.Join(home, ".ggcode", "cron-jobs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	diskBlob, _ := json.Marshal(map[string]interface{}{"jobs": []map[string]interface{}{
		{"id": "disk-1", "cron_expr": "* * * * *", "prompt": "磁盘面干扰项", "recurring": true},
	}})
	if err := os.WriteFile(filepath.Join(dir, "20990101-000000-ffffffffffff.json"), diskBlob, 0o644); err != nil {
		t.Fatal(err)
	}

	s := NewServer(config.DefaultConfig())
	s.SetSessionStore(nil, t.TempDir())
	s.SetCronListFn(func() []CronJobView {
		return []CronJobView{{ID: "live-1", CronExpr: "*/5 * * * *", Prompt: "当前会话任务", Recurring: true}}
	})
	rec := httptest.NewRecorder()
	s.handleCronJobs(rec, httptest.NewRequest("GET", "/api/cron/jobs", nil))
	var out []map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 || out[0]["id"] != "live-1" {
		t.Fatalf("expected fn source only [live-1], got %v", out)
	}
}
