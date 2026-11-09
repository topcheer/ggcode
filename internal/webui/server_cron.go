package webui

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// handleCronJobs GET /api/cron/jobs —— 定时任务只读列表。
// 真实存储布局(internal/cron Scheduler):~/.ggcode/cron-jobs/<timestamp-id>.json,
// 每任务一文件,内容为 {"jobs":[...]} 数组包裹——历史实现读
// <sha256(workspace)>.json 单文件,形状不同构导致恒空(用户可见 bug)。
// 本 handler 扫描目录聚合全部任务文件。只读展示(id/cron/prompt 截断/状态),
// 写操作仍走 TUI/CLI 面(P6-10 边界)。
func (s *Server) handleCronJobs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	home, err := os.UserHomeDir()
	if err != nil {
		writeJSON(w, []interface{}{})
		return
	}
	dir := filepath.Join(home, ".ggcode", "cron-jobs")
	entries, err := os.ReadDir(dir)
	if err != nil {
		writeJSON(w, []interface{}{})
		return
	}

	type jobRaw struct {
		ID        string `json:"id"`
		CronExpr  string `json:"cron_expr"`
		Prompt    string `json:"prompt"`
		Recurring bool   `json:"recurring"`
		Paused    bool   `json:"paused,omitempty"`
		CreatedAt string `json:"created_at,omitempty"`
	}
	type storeFile struct {
		Jobs []jobRaw `json:"jobs"`
	}
	type out struct {
		ID        string `json:"id"`
		CronExpr  string `json:"cron_expr"`
		Prompt    string `json:"prompt"`
		Recurring bool   `json:"recurring"`
		Paused    bool   `json:"paused"`
		CreatedAt string `json:"created_at,omitempty"`
	}

	result := make([]out, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") || strings.HasSuffix(e.Name(), ".flock") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		// 兼容两种形态:scheduler 写出的 {"jobs":[...]} 包裹,以及裸数组
		var wrapped storeFile
		var bare []jobRaw
		var jobs []jobRaw
		if err := json.Unmarshal(raw, &wrapped); err == nil && wrapped.Jobs != nil {
			jobs = wrapped.Jobs
		} else if err := json.Unmarshal(raw, &bare); err == nil {
			jobs = bare
		}
		for _, j := range jobs {
			p := j.Prompt
			if len(p) > 120 {
				p = p[:120] + "…"
			}
			result = append(result, out{ID: j.ID, CronExpr: j.CronExpr, Prompt: p, Recurring: j.Recurring, Paused: j.Paused, CreatedAt: j.CreatedAt})
		}
	}
	sort.Slice(result, func(i, k int) bool { return result[i].ID < result[k].ID })
	writeJSON(w, result)
}
