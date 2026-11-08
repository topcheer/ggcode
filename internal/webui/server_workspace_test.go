package webui

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/topcheer/ggcode/internal/config"
)

func TestWorkspaceFilesSearch(t *testing.T) {
	dir := t.TempDir()
	// 布景:src/a.go, src/deep/b.ts, node_modules/skip.js, README.md
	for _, p := range []string{"src/a.go", "src/deep/b.ts", "node_modules/skip.js", "README.md"} {
		full := filepath.Join(dir, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	cfg := config.DefaultConfig()
	s := NewServer(cfg)
	s.SetSessionStore(nil, dir)

	srv := httptest.NewServer(s.mux)
	defer srv.Close()

	get := func(q string) []string {
		req := httptest.NewRequest("GET", "/api/workspace/files?q="+q, nil)
		rec := httptest.NewRecorder()
		s.handleWorkspaceFiles(rec, req)
		var out []string
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return out
	}

	// 空查询:列全部(node_modules 被跳过)
	all := get("")
	if len(all) != 3 {
		t.Fatalf("expected 3 files (node_modules skipped), got %v", all)
	}
	// 子串匹配(大小写不敏感)
	got := get("readme")
	if len(got) != 1 || got[0] != "README.md" {
		t.Fatalf("expected [README.md], got %v", got)
	}
	// src 前缀命中目录下文件
	if got := get("src/"); len(got) != 2 {
		t.Fatalf("expected 2 under src/, got %v", got)
	}
	// deep 精确
	if got := get("deep"); len(got) != 1 || got[0] != "src/deep/b.ts" {
		t.Fatalf("expected src/deep/b.ts, got %v", got)
	}
}
