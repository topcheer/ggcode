package webui

import (
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// handleWorkspaceFiles GET /api/workspace/files?q=<prefix>
// @mention 实体补全(§4.3):在 Server.workspace 下做受限深度遍历,
// 返回匹配 q(大小写不敏感前缀/子串)的相对路径,最多 50 条。
// 跳过依赖/产物/隐藏重目录,避免大仓库卡顿。
func (s *Server) handleWorkspaceFiles(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	s.mu.RLock()
	root := s.workspace
	s.mu.RUnlock()
	if root == "" {
		writeJSON(w, []string{})
		return
	}
	q := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))

	const (
		maxResults = 50
		maxDepth   = 8
	)

	skipDir := func(name string) bool {
		switch name {
		case "node_modules", ".git", ".ggcode", "dist", "dist-site", "bin",
			"target", "build", ".next", ".venv", "__pycache__", "vendor":
			return true
		}
		return strings.HasPrefix(name, ".") && name != "."
	}

	var matches []string
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // 跳过不可读子树,不中断
		}
		rel, rerr := filepath.Rel(root, path)
		if rerr != nil {
			return nil
		}
		if rel == "." {
			return nil
		}
		if d.IsDir() {
			depth := strings.Count(rel, string(filepath.Separator))
			if depth >= maxDepth || skipDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		rel = filepath.ToSlash(rel)
		if q == "" || strings.Contains(strings.ToLower(rel), q) {
			matches = append(matches, rel)
		}
		if len(matches) >= maxResults {
			return filepath.SkipAll
		}
		return nil
	})
	sort.Strings(matches)
	if len(matches) > maxResults {
		matches = matches[:maxResults]
	}
	writeJSON(w, matches)
}
