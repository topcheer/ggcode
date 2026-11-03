package commands

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

func agentPluginSkillTargets(projectDir string) []loadTarget {
	if strings.TrimSpace(projectDir) == "" {
		return nil
	}
	root := filepath.Join(projectDir, ".agent-plugins")
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	var out []loadTarget
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		dir := filepath.Join(root, entry.Name())
		manifest, err := os.ReadFile(filepath.Join(dir, "plugin.json"))
		if err != nil {
			continue
		}
		var m struct {
			Name string `json:"name"`
		}
		if json.Unmarshal(manifest, &m) != nil || strings.TrimSpace(m.Name) == "" {
			continue
		}
		skills := filepath.Join(dir, "skills")
		if info, err := os.Stat(skills); err != nil || !info.IsDir() {
			continue
		}
		out = append(out, loadTarget{Dir: skills, Source: SourceProject, LoadedFrom: LoadedFromSkills})
	}
	return out
}
