package webui

import (
	"net/http"
	"strings"
)

// handlePanelData GET /api/panel/{id} — 面板数据端点分发。
// P1 实现 select 原型数据(model/lang);其余原型的数据端点按阶段补齐,
// 未实现的面板返回 501(前端显示占位提示,不崩溃)。
func (s *Server) handlePanelData(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/api/panel/")
	// 注入式数据面优先(root 装配闭包):stats/usage/memory/plugins/tags。
	if fn := s.panelDataFns[id]; fn != nil {
		writeJSON(w, fn())
		return
	}
	switch id {
	case "model":
		s.panelDataModel(w)
	case "lang":
		s.panelDataLang(w)
	case "plugins", "memory", "tags", "stats", "usage":
		writeError(w, http.StatusNotImplemented, "panel data pending: "+id)
	default:
		writeError(w, http.StatusNotFound, "unknown panel: "+id)
	}
}

// panelDataModel: TUI 选型面语义(internal/tui openModelPanel 同源):
// 只列当前 active vendor/endpoint 的模型(当前值不在列表时置顶),并附
// vendor/endpoint/protocol 上下文。用户裁定缺陷修复:不再罗列全部
// vendor×endpoint 的全量模型(那与 TUI /model 选型面语义不符)。
func (s *Server) panelDataModel(w http.ResponseWriter) {
	type opt struct {
		ID      string `json:"id"`      // 回填值: model 名(与 TUI /model <name> 同参)
		Label   string `json:"label"`   // 显示名: model
		Current bool   `json:"current"` // 当前选中
	}
	opts := []opt{}
	current := ""
	ctx := map[string]string{}
	if s.cfg != nil {
		resolved, err := s.cfg.ResolveActiveEndpoint()
		if err != nil {
			// 无可用 active 端点:显式空态(前端展示 error,不再伪造全量列表)
			writeJSON(w, map[string]interface{}{
				"kind":    "select",
				"grouped": false,
				"options": []opt{},
				"current": "",
				"error":   err.Error(),
				"submit":  map[string]string{"command": "/model", "argFrom": "label"},
			})
			return
		}
		current = resolved.Model
		seen := map[string]bool{}
		models := make([]string, 0, len(resolved.Models))
		if current != "" {
			models = append(models, current)
			seen[current] = true
		}
		for _, m := range resolved.Models {
			if m == "" || seen[m] {
				continue
			}
			seen[m] = true
			models = append(models, m)
		}
		for _, m := range models {
			opts = append(opts, opt{ID: m, Label: m, Current: m == current})
		}
		ctx = map[string]string{
			"vendor":   resolved.VendorName,
			"endpoint": resolved.EndpointName,
			"protocol": resolved.Protocol,
		}
	}
	writeJSON(w, map[string]interface{}{
		"kind":    "select",
		"grouped": false,
		"options": opts,
		"current": current,
		"context": ctx,
		// 回填规约:前端取选项 label 拼 `/model <model>`(TUI /model 语义
		// 按模型名切换;vendor/endpoint 上下文由配置层解析)。
		"submit": map[string]string{"command": "/model", "argFrom": "label"},
	})
}

// panelDataLang: 语言选择(常量域)。
func (s *Server) panelDataLang(w http.ResponseWriter) {
	type opt struct {
		ID      string `json:"id"`
		Label   string `json:"label"`
		Current bool   `json:"current"`
	}
	current := "en"
	if s.cfg != nil && s.cfg.Language != "" {
		current = s.cfg.Language
	}
	opts := []opt{
		{ID: "en", Label: "English", Current: current == "en"},
		{ID: "zh-CN", Label: "简体中文", Current: current == "zh-CN"},
	}
	writeJSON(w, map[string]interface{}{
		"kind":    "select",
		"grouped": false,
		"options": opts,
		"current": current,
		"submit":  map[string]string{"command": "/lang", "argFrom": "id"},
	})
}
