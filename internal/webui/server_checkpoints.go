package webui

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// handleCheckpoints GET /api/checkpoints?limit=30 —— 当前 workspace 的最近编辑检查点只读列表。
// 存储复刻 internal/checkpoint 的 journal 布局:
// os.UserConfigDir()/ggcode/checkpoints/<hex(sha256(workspace))[:2]>/<hex16>.jsonl(每行一个 Checkpoint 事件)。
// 只读浏览(P6-9 边界):回滚仍走 TUI 面板。
// userConfigDir 可在测试中替换(os.UserConfigDir 在 darwin 忽略 XDG,注入难)
var userConfigDir = os.UserConfigDir

func (s *Server) handleCheckpoints(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	s.mu.RLock()
	ws := s.workspace
	s.mu.RUnlock()

	base, err := userConfigDir()
	if err != nil || ws == "" {
		writeJSON(w, []interface{}{})
		return
	}
	sum := sha256.Sum256([]byte(ws))
	name := hex.EncodeToString(sum[:8]) + ".jsonl"
	journalPath := filepath.Join(base, "ggcode", "checkpoints", name[:2], name)

	f, err := os.Open(journalPath)
	if err != nil {
		writeJSON(w, []interface{}{})
		return
	}
	defer f.Close()

	type cp struct {
		ID        string `json:"id"`
		FilePath  string `json:"file_path"`
		ToolCall  string `json:"tool_call"`
		Timestamp string `json:"timestamp"`
		RunID     string `json:"run_id,omitempty"`
	}
	var out []cp
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var c cp
		if json.Unmarshal([]byte(line), &c) == nil && c.ID != "" {
			out = append(out, c)
		}
	}
	// 最近优先
	sort.Slice(out, func(i, j int) bool { return out[i].Timestamp > out[j].Timestamp })
	const maxList = 30
	if len(out) > maxList {
		out = out[:maxList]
	}
	writeJSON(w, out)
}
