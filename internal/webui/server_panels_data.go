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
	// 注入式数据面优先(root 装配闭包):stats/usage/memory/plugins/tags/lanchat。
	if fn := s.panelDataFns[id]; fn != nil {
		writeJSON(w, fn())
		return
	}
	switch id {
	case "model":
		s.panelDataModel(w)
	case "lang":
		s.panelDataLang(w)
	case "plugins", "memory", "tags", "stats", "usage", "lanchat":
		writeError(w, http.StatusNotImplemented, "panel data pending: "+id)
	default:
		writeError(w, http.StatusNotFound, "unknown panel: "+id)
	}
}

// panelDataModel: 分组选择数据(vendor→endpoint→models)+当前值。
// 与头部模型下拉同源(cfg.Vendors+ResolveActiveEndpoint),零新真值源。
func (s *Server) panelDataModel(w http.ResponseWriter) {
	type opt struct {
		ID      string `json:"id"`      // 回填值: "vendor/endpoint/model"
		Label   string `json:"label"`   // 显示名: model
		Badge   string `json:"badge"`   // vendor/endpoint
		Current bool   `json:"current"` // 当前选中
	}
	groups := []map[string]interface{}{}
	current := ""
	if s.cfg != nil {
		if resolved, err := s.cfg.ResolveActiveEndpoint(); err == nil {
			current = resolved.Model
		}
		for vname, v := range s.cfg.Vendors {
			for ename, ep := range v.Endpoints {
				opts := make([]opt, 0, len(ep.Models))
				for _, m := range ep.Models {
					id := vname + "/" + ename + "/" + m
					opts = append(opts, opt{ID: id, Label: m, Badge: vname + "/" + ename, Current: m == current})
				}
				if len(opts) > 0 {
					groups = append(groups, map[string]interface{}{
						"label":   vname + " / " + ename,
						"options": opts,
					})
				}
			}
		}
	}
	writeJSON(w, map[string]interface{}{
		"kind":    "select",
		"grouped": true,
		"current": current,
		"groups":  groups,
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
