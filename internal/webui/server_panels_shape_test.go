package webui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/topcheer/ggcode/internal/config"
)

// TestPanelEndpointShapes 钉前端 adapters.ts 依赖的端点形状契约:
// 异形端点(cron 顶层数组/skills 双键/IM 平面对象)的 JSON 形状漂移会静默
// 破坏前端归一层,此处钉死后端输出防漂移。前端契约源:web/src/panels/adapters.ts。
func TestPanelEndpointShapes(t *testing.T) {
	s := &Server{cfg: &config.Config{}}
	s.cfg.IM.Adapters = map[string]config.IMAdapterConfig{
		"testqq": {Enabled: true, Platform: "qq", Command: "demo"},
	}

	t.Run("cron_top_level_array", func(t *testing.T) {
		rec := httptest.NewRecorder()
		s.handleCronJobs(rec, httptest.NewRequest(http.MethodGet, "/api/cron/jobs", nil))
		var arr []map[string]interface{}
		if err := json.Unmarshal(rec.Body.Bytes(), &arr); err != nil {
			t.Fatalf("cron/jobs 必须是顶层数组(空环境回落 []): %v, body=%s", err, rec.Body.String())
		}
	})

	t.Run("skills_dual_keys", func(t *testing.T) {
		rec := httptest.NewRecorder()
		s.handleKnightSkills(rec, httptest.NewRequest(http.MethodGet, "/api/knight/skills", nil))
		var m map[string]interface{}
		if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
			t.Fatalf("knight/skills 必须是对象: %v, body=%s", err, rec.Body.String())
		}
		if _, ok := m["active"]; !ok {
			t.Errorf("knight/skills 缺 active 键: %v", m)
		}
		if _, ok := m["staging"]; !ok {
			t.Errorf("knight/skills 缺 staging 键: %v", m)
		}
	})

	t.Run("im_detail_flat_config", func(t *testing.T) {
		rec := httptest.NewRecorder()
		s.handleIMAdapterDetail(rec, httptest.NewRequest(http.MethodGet, "/api/im/adapters/testqq", nil))
		var m map[string]interface{}
		if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
			t.Fatalf("im detail 必须是平面对象: %v, body=%s", err, rec.Body.String())
		}
		// 前端 IM_FIELD_ORDER 依赖的键面
		for _, key := range []string{"enabled", "platform"} {
			if _, ok := m[key]; !ok {
				t.Errorf("im detail 缺 %q 键: %v", key, m)
			}
		}
		if m["enabled"] != true {
			t.Errorf("im detail enabled 应为 true: %v", m["enabled"])
		}
	})
}
