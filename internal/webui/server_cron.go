package webui

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// CronJobView /api/cron/jobs 输出行(前端 CronPage 消费契约)。
type CronJobView struct {
	ID        string `json:"id"`
	CronExpr  string `json:"cron_expr"`
	Prompt    string `json:"prompt"`
	Recurring bool   `json:"recurring"`
	Paused    bool   `json:"paused"`
	CreatedAt string `json:"created_at,omitempty"`
}

// SetCronListFn 注入当前 session 的任务快照源(活 cron.Scheduler——随会话切换
// 自动重绑 sessionPath,root 用 repl.CronJobsSnapshot 转换)。注入后本 handler
// 以活实例为真相源;独立 webui 模式(fn=nil)回落磁盘扫描(无会话过滤语义)。
func (s *Server) SetCronListFn(fn func() []CronJobView) {
	s.mu.Lock()
	s.cronListFn = fn
	s.mu.Unlock()
}

// handleCronJobs GET /api/cron/jobs —— 当前 session 的定时任务只读列表。
// 真相源是活 Scheduler(随会话切换重绑 sessionPath),经 SetCronListFn 注入;
// 独立 webui 模式(fn=nil)回落扫描 ~/.ggcode/cron-jobs/*.json 兼容形态。
// 只读展示,写操作仍走 TUI/CLI 面(P6-10 边界)。
func (s *Server) handleCronJobs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	s.mu.RLock()
	fn := s.cronListFn
	s.mu.RUnlock()
	if fn != nil {
		writeJSON(w, fn())
		return
	}
	writeJSON(w, s.scanLegacyCronStore())
}

// scanLegacyCronStore 独立模式回落:扫描 ~/.ggcode/cron-jobs/*.json 聚合
// 全部任务(兼容 scheduler 的 {"jobs":[...]} 包裹与裸数组两形态;.flock 跳过)。
func (s *Server) scanLegacyCronStore() []CronJobView {
	home, err := os.UserHomeDir()
	if err != nil {
		return []CronJobView{}
	}
	dir := filepath.Join(home, ".ggcode", "cron-jobs")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return []CronJobView{}
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

	result := make([]CronJobView, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") || strings.HasSuffix(e.Name(), ".flock") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
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
			result = append(result, CronJobView{ID: j.ID, CronExpr: j.CronExpr, Prompt: p, Recurring: j.Recurring, Paused: j.Paused, CreatedAt: j.CreatedAt})
		}
	}
	sort.Slice(result, func(i, k int) bool { return result[i].ID < result[k].ID })
	return result
}
