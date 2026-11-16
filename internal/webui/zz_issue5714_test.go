package webui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/config"
)

// #5714 增量收口(禁用总闸期间的地基修复):用户裁定两项缺陷——
// (1) /model 面板罗列全部 vendor×endpoint 模型(与 TUI 选型面语义不符);
// (2) 多数面板 DataURL 无真实数据源(形状无 adapter,渲染占位空态)。
// 本文件钉:/model 活动端点语义、adapter 消费的端点形状、注册表全量覆盖。

func issue5714TestConfig(t *testing.T) *config.Config {
	t.Helper()
	cfg := &config.Config{
		Vendors: map[string]config.VendorConfig{
			"alpha": {DisplayName: "Alpha", Endpoints: map[string]config.EndpointConfig{
				"e1": {DisplayName: "E1", Protocol: "openai", BaseURL: "https://a.example.com", Models: []string{"m1", "m2"}, DefaultModel: "m1"},
			}},
			"beta": {DisplayName: "Beta", Endpoints: map[string]config.EndpointConfig{
				"e2": {DisplayName: "E2", Protocol: "openai", BaseURL: "https://b.example.com", Models: []string{"m3"}, DefaultModel: "m3"},
			}},
		},
	}
	if err := cfg.SetActiveSelection("alpha", "e1", "m2"); err != nil {
		t.Fatalf("SetActiveSelection: %v", err)
	}
	return cfg
}

// TestIssue5714PanelModelActiveEndpointOnly /model 面板必须与 TUI
// openModelPanel 同语义:仅列 active vendor/endpoint 的模型,当前值置顶;
// 其它 vendor/endpoint 的模型(m3)不得出现(用户裁定缺陷的反向钉)。
func TestIssue5714PanelModelActiveEndpointOnly(t *testing.T) {
	s := &Server{cfg: issue5714TestConfig(t)}
	rec := httptest.NewRecorder()
	s.handlePanelData(rec, httptest.NewRequest(http.MethodGet, "/api/panel/model", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	var m map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatalf("decode: %v (body: %s)", err, rec.Body.String())
	}
	if grouped, _ := m["grouped"].(bool); grouped {
		t.Errorf("grouped = true, want false (活动端点平铺语义)")
	}
	opts, _ := m["options"].([]any)
	if len(opts) != 2 {
		t.Fatalf("options = %d, want 2 (仅 active endpoint 的 m2+m1), body: %s", len(opts), rec.Body.String())
	}
	first, _ := opts[0].(map[string]any)
	if first["label"] != "m2" || first["current"] != true {
		t.Errorf("options[0] = %v, want 当前模型 m2 置顶且 current=true", first)
	}
	for _, o := range opts {
		om, _ := o.(map[string]any)
		if om["label"] == "m3" {
			t.Errorf("非活动端点模型 m3 出现在 /model 面板(用户裁定缺陷): %v", om)
		}
		if _, has := om["badge"]; has {
			t.Errorf("选项不应再携带 vendor/endpoint badge(平铺语义): %v", om)
		}
	}
	if m["current"] != "m2" {
		t.Errorf("current = %v, want m2", m["current"])
	}
	ctx, _ := m["context"].(map[string]any)
	if ctx["vendor"] != "Alpha" || ctx["endpoint"] != "E1" {
		t.Errorf("context = %v, want vendor=Alpha endpoint=E1 (TUI 面板上下文,显示名)", ctx)
	}
	sub, _ := m["submit"].(map[string]any)
	if sub["command"] != "/model" || sub["argFrom"] != "label" {
		t.Errorf("submit = %v, want /model + argFrom=label", sub)
	}
}

// TestIssue5714PanelModelNoActiveEndpointExplicitEmpty 无可用 active 端点时
// 返回显式 error 空态,不再回退到全 vendor 列表(伪造数据)。
func TestIssue5714PanelModelNoActiveEndpointExplicitEmpty(t *testing.T) {
	s := &Server{cfg: &config.Config{}}
	rec := httptest.NewRecorder()
	s.handlePanelData(rec, httptest.NewRequest(http.MethodGet, "/api/panel/model", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var m map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatalf("decode: %v", err)
	}
	opts, _ := m["options"].([]any)
	if len(opts) != 0 {
		t.Errorf("options = %d, want 0 (无 active 端点不得伪造列表)", len(opts))
	}
	if errStr, _ := m["error"].(string); errStr == "" {
		t.Errorf("缺显式 error 字段: %v", m)
	}
}

// TestIssue5714SelectEndpointShapes 钉 adaptSelect 消费的三个异形端点形状
// (前端契约源:web/src/panels/adapters.ts adaptSelect)。
func TestIssue5714SelectEndpointShapes(t *testing.T) {
	s := &Server{cfg: issue5714TestConfig(t)}
	s.chatControlsFn = func() map[string]string {
		return map[string]string{"mode": "supervised", "reasoning_effort": "high", "model": "m2"}
	}

	t.Run("vendors_top_level_array", func(t *testing.T) {
		rec := httptest.NewRecorder()
		s.handleVendors(rec, httptest.NewRequest(http.MethodGet, "/api/vendors", nil))
		var arr []map[string]interface{}
		if err := json.Unmarshal(rec.Body.Bytes(), &arr); err != nil {
			t.Fatalf("/api/vendors 必须是顶层数组: %v (body=%s)", err, rec.Body.String())
		}
		if len(arr) == 0 {
			t.Fatalf("vendors 空(测试配置含 alpha/beta)")
		}
		for _, v := range arr {
			for _, key := range []string{"id", "display_name", "has_api_key", "endpoint_ids"} {
				if _, ok := v[key]; !ok {
					t.Errorf("vendors 条目缺 %q 键: %v", key, v)
				}
			}
		}
	})

	t.Run("impersonate_current_and_presets", func(t *testing.T) {
		rec := httptest.NewRecorder()
		s.handleImpersonate(rec, httptest.NewRequest(http.MethodGet, "/api/impersonate", nil))
		var m map[string]interface{}
		if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
			t.Fatalf("/api/impersonate 必须是对象: %v (body=%s)", err, rec.Body.String())
		}
		cur, ok := m["current"].(map[string]any)
		if !ok {
			t.Fatalf("缺 current 对象: %v", m)
		}
		if _, ok := cur["preset"]; !ok {
			t.Errorf("current 缺 preset 键: %v", cur)
		}
		presets, _ := m["presets"].([]any)
		if len(presets) == 0 {
			t.Errorf("presets 空(DefaultImpersonationPresets 应非空)")
		}
		for _, p := range presets {
			pm, _ := p.(map[string]any)
			if _, ok := pm["id"]; !ok {
				t.Errorf("preset 缺 id 键: %v", pm)
			}
		}
	})

	t.Run("chat_controls_flat_map", func(t *testing.T) {
		rec := httptest.NewRecorder()
		s.handleChatControls(rec, httptest.NewRequest(http.MethodGet, "/api/chat/controls", nil))
		var m map[string]interface{}
		if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
			t.Fatalf("/api/chat/controls 必须是平面 map: %v (body=%s)", err, rec.Body.String())
		}
		if m["mode"] != "supervised" {
			t.Errorf("mode = %v, want supervised", m["mode"])
		}
		if m["reasoning_effort"] != "high" {
			t.Errorf("reasoning_effort = %v, want high", m["reasoning_effort"])
		}
	})
}

// TestIssue5714PanelRegistryAdapterCoverage 注册表全量数据源覆盖钉:
// 每个注册表 DataURL 要么是后端原生 /api/panel/*(server_panels_data/
// 注入面),要么被前端归一层(adapters.ts + FormPanel.tsx)按字面引用。
// 新增注册表条目而漏接 adapter/端点会在此失败,不再静默渲染占位空态。
func TestIssue5714PanelRegistryAdapterCoverage(t *testing.T) {
	srcB, err := os.ReadFile("../../web/src/panels/adapters.ts")
	if err != nil {
		t.Fatalf("read adapters.ts: %v", err)
	}
	formB, err := os.ReadFile("../../web/src/panels/FormPanel.tsx")
	if err != nil {
		t.Fatalf("read FormPanel.tsx: %v", err)
	}
	src := string(srcB) + string(formB)
	for cmd, spec := range panelRegistry {
		u := spec.DataURL
		if u == "" {
			t.Errorf("注册表 %q 缺 DataURL", cmd)
			continue
		}
		if strings.HasPrefix(u, "/api/panel/") {
			continue // 后端原生数据面(handlePanelData/注入闭包)
		}
		if strings.Contains(src, `"`+u+`"`) {
			continue
		}
		// IM 适配器系列经 FormPanel startsWith("/api/im/adapters/") 前缀消费
		if strings.HasPrefix(u, "/api/im/adapters/") && strings.Contains(src, `"/api/im/adapters/"`) {
			continue
		}
		t.Errorf("注册表 %q 的 DataURL %s 未被前端归一层引用(占位面板回归)", cmd, u)
	}
	// /im 总览必须是 list-detail(map 形状端点,form 面板无法渲染)
	if im, ok := panelRegistry["im"]; ok && im.Kind != "list-detail" {
		t.Errorf("im 注册条目 kind = %q, want list-detail(总览端点是 map 形状)", im.Kind)
	}
}
