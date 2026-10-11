package commands

// Agent Plugins skills component (agent-plugins.org, research sa-114):
// each valid plugin package under <project>/.agent-plugins/<dir>/ with a
// plugin.json manifest may ship skills/<name>/SKILL.md in the agentskills.io
// format. Those skill directories join the loader's targets at the FRONT of
// the list: Load() semantics are later-targets-override-earlier, so every
// native directory (project .agents/skills, .ggcode/skills, global user
// skills) keeps precedence over plugin-shipped skills of the same name.
//
// Manifest validation here intentionally mirrors (simplified) the mcp
// package's DiscoverAgentPlugins: required plugin.json with a non-empty
// name. The two packages cannot import each other's discovery without an
// import cycle risk (commands -> mcp -> config is fine, but keeping skills
// discovery local to the loader keeps this package self-contained).

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// agentPluginSkillTargets returns one loadTarget per valid plugin that
// ships a skills/ directory. A plugin without skills/ contributes nothing
// (mcp.json-only plugins are legal packages).
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
			continue // no manifest: not a plugin package
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
