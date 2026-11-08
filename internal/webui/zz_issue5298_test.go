package webui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/config"
)

// #5298: PUT /api/config 白名单合并契约。此前 handleConfig 对非 GET 恒 405,
// 设置页「配置/常规」保存按钮自 #5277 起从未工作过。

func putConfigBody(t *testing.T, s *Server, body string) (int, map[string]interface{}) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPut, "/api/config", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+s.Token())
	w := httptest.NewRecorder()
	s.handleConfig(w, req)
	var out map[string]interface{}
	_ = json.NewDecoder(w.Body).Decode(&out)
	return w.Code, out
}

func Test5298_ConfigPUTWhitelistApplies(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.FilePath = filepath.Join(t.TempDir(), "ggcode.yaml")
	s := NewServer(cfg)

	code, out := putConfigBody(t, s, `{"language":"en","max_iterations":42,"allowed_dirs":["/a","/b"],"bogus_key":1,"vendors":{}}`)
	if code != http.StatusOK {
		t.Fatalf("want 200, got %d: %v", code, out)
	}
	if s.cfg.Language != "en" || s.cfg.MaxIterations != 42 {
		t.Fatalf("scalars not applied: language=%q max=%d", s.cfg.Language, s.cfg.MaxIterations)
	}
	if len(s.cfg.AllowedDirs) != 2 || s.cfg.AllowedDirs[0] != "/a" {
		t.Fatalf("allowed_dirs not applied: %v", s.cfg.AllowedDirs)
	}
	applied, _ := out["applied"].([]interface{})
	if len(applied) != 3 {
		t.Fatalf("want 3 applied, got %v", out["applied"])
	}
	ignored, _ := out["ignored"].([]interface{})
	if len(ignored) != 2 {
		t.Fatalf("want bogus_key+vendors ignored, got %v", out["ignored"])
	}
}

func Test5298_ConfigPUTWrongTypeRejected(t *testing.T) {
	cfg := config.DefaultConfig()
	orig := cfg.Language
	s := NewServer(cfg)

	code, out := putConfigBody(t, s, `{"language":123}`)
	if code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d: %v", code, out)
	}
	if s.cfg.Language != orig {
		t.Fatalf("config mutated on rejected PUT: %q -> %q", orig, s.cfg.Language)
	}
}

func Test5298_ConfigPUTBadJSONRejected(t *testing.T) {
	s := NewServer(config.DefaultConfig())

	code, _ := putConfigBody(t, s, `{not json`)
	if code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", code)
	}
}

func Test5298_ConfigPUTVendorCoherenceValidated(t *testing.T) {
	cfg := config.DefaultConfig()
	if len(cfg.Vendors) == 0 {
		t.Skip("default config has no vendors")
	}
	s := NewServer(cfg)

	// 不存在的 vendor 应被 SetActiveSelection 拒绝而非静默写入(#5297 同族教训)
	code, _ := putConfigBody(t, s, `{"vendor":"no-such-vendor"}`)
	if code != http.StatusBadRequest {
		t.Fatalf("want 400 for unknown vendor, got %d", code)
	}
	if s.cfg.Vendor == "no-such-vendor" {
		t.Fatal("unknown vendor silently applied")
	}
}
