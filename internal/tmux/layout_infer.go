package tmux

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// InferDefaultLayout returns a reasonable first-run layout for a workspace.
// It is intentionally conservative and only uses local project files.
func InferDefaultLayout(workspace string) []LayoutPane {
	workspace = normalizeWorkspace(workspace)
	if fileExists(filepath.Join(workspace, "go.mod")) {
		layout := []LayoutPane{
			{Purpose: "shell", Command: "", Horizontal: true, Size: "35%"},
			{Purpose: "test", Command: "go test -tags goolm ./...", Horizontal: false, Size: "30%"},
		}
		if makefileHasTarget(workspace, "verify-ci") {
			layout = append(layout, LayoutPane{Purpose: "verify", Command: "make verify-ci", Horizontal: false, Size: "30%"})
		}
		return layout
	}
	if fileExists(filepath.Join(workspace, "package.json")) {
		layout := []LayoutPane{{Purpose: "shell", Command: "", Horizontal: true, Size: "35%"}}
		scripts := packageJSONScripts(filepath.Join(workspace, "package.json"))
		if _, ok := scripts["dev"]; ok {
			layout = append(layout, LayoutPane{Purpose: "dev", Command: "npm run dev", Horizontal: false, Size: "30%"})
		}
		if _, ok := scripts["test"]; ok {
			layout = append(layout, LayoutPane{Purpose: "test", Command: "npm test", Horizontal: false, Size: "30%"})
		}
		return layout
	}
	if fileExists(filepath.Join(workspace, "pubspec.yaml")) {
		return []LayoutPane{
			{Purpose: "shell", Command: "", Horizontal: true, Size: "35%"},
			{Purpose: "analyze", Command: "flutter analyze", Horizontal: false, Size: "30%"},
			{Purpose: "test", Command: "flutter test", Horizontal: false, Size: "30%"},
		}
	}
	return []LayoutPane{{Purpose: "shell", Command: "", Horizontal: true, Size: "35%"}}
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func makefileHasTarget(workspace, target string) bool {
	for _, name := range []string{"Makefile", "makefile", "GNUmakefile"} {
		data, err := os.ReadFile(filepath.Join(workspace, name))
		if err != nil {
			continue
		}
		prefix := target + ":"
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, prefix) {
				return true
			}
		}
	}
	return false
}

func packageJSONScripts(path string) map[string]string {
	var pkg struct {
		Scripts map[string]string `json:"scripts"`
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	if err := json.Unmarshal(data, &pkg); err != nil {
		return nil
	}
	return pkg.Scripts
}
