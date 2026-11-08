package webui

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
)

// handleCronJobs GET /api/cron/jobs —— 当前 workspace 的定时任务只读列表。
// 存储布局复刻 internal/cron 的全局分桶:~/.ggcode/cron-jobs/<sha256(workspace)>.json。
// 只读展示(id/cron/prompt 截断/状态),写操作仍走 TUI/CLI 面(P6-10 边界)。
func (s *Server) handleCronJobs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	s.mu.RLock()
	ws := s.workspace
	s.mu.RUnlock()

	home, err := os.UserHomeDir()
	if err != nil || ws == "" {
		writeJSON(w, []interface{}{})
		return
	}
	h := sha256.Sum256([]byte(ws))
	storePath := filepath.Join(home, ".ggcode", "cron-jobs", fmt.Sprintf("%x", h)+".json")

	raw, err := os.ReadFile(storePath)
	if err != nil {
		writeJSON(w, []interface{}{})
		return
	}
	var jobs []struct {
		ID        string `json:"id"`
		CronExpr  string `json:"cron_expr"`
		Prompt    string `json:"prompt"`
		Recurring bool   `json:"recurring"`
		Paused    bool   `json:"paused,omitempty"`
	}
	if err := json.Unmarshal(raw, &jobs); err != nil {
		writeJSON(w, []interface{}{})
		return
	}
	type out struct {
		ID        string `json:"id"`
		CronExpr  string `json:"cron_expr"`
		Prompt    string `json:"prompt"`
		Recurring bool   `json:"recurring"`
		Paused    bool   `json:"paused"`
	}
	result := make([]out, 0, len(jobs))
	for _, j := range jobs {
		p := j.Prompt
		if len(p) > 120 {
			p = p[:120] + "…"
		}
		result = append(result, out{ID: j.ID, CronExpr: j.CronExpr, Prompt: p, Recurring: j.Recurring, Paused: j.Paused})
	}
	writeJSON(w, result)
}
