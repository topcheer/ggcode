package webui

import (
	"net/http"
	"sort"
	"time"

	"github.com/topcheer/ggcode/internal/checkpoint"
)

// handleCheckpoints GET /api/checkpoints?limit=30 —— 当前 workspace 的最近编辑检查点只读列表。
// 通过 checkpoint.ListPersisted 读取 internal/checkpoint 的 journal 布局
// (os.UserConfigDir()/ggcode/checkpoints/<hex(sha256(workspace))[:2]>/<hex16>.jsonl)
// 并按事件回放得到当前仍存活的检查点,与写入格式(journalEvent 信封行)及 TUI 面板状态保持一致。
// 只读浏览(P6-9 边界):回滚仍走 TUI 面板。

// persistedListCapacity 与生产写入方 NewPersistentManager(50, workingDir) 的容量一致
// (root.go / daemon.go / pipe.go / acp agent_loop.go),保证回放时的淘汰语义对齐。
const persistedListCapacity = 50

func (s *Server) handleCheckpoints(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	s.mu.RLock()
	ws := s.workspace
	s.mu.RUnlock()
	if ws == "" {
		writeJSON(w, []interface{}{})
		return
	}

	type cp struct {
		ID        string    `json:"id"`
		FilePath  string    `json:"file_path"`
		ToolCall  string    `json:"tool_call"`
		Timestamp time.Time `json:"timestamp"`
		RunID     string    `json:"run_id,omitempty"`
	}
	live := checkpoint.ListPersisted(persistedListCapacity, ws)
	out := make([]cp, 0, len(live))
	for _, c := range live {
		out = append(out, cp{
			ID:        c.ID,
			FilePath:  c.FilePath,
			ToolCall:  c.ToolCall,
			Timestamp: c.Timestamp,
			RunID:     c.RunID,
		})
	}
	// 最近优先
	sort.SliceStable(out, func(i, j int) bool { return out[i].Timestamp.After(out[j].Timestamp) })
	const maxList = 30
	if len(out) > maxList {
		out = out[:maxList]
	}
	writeJSON(w, out)
}
