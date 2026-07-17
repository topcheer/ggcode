package agentruntime

import (
	"path/filepath"
	"slices"
	"strings"

	"github.com/topcheer/ggcode/internal/agent"
	"github.com/topcheer/ggcode/internal/memory"
	"github.com/topcheer/ggcode/internal/provider"
)

// applyProjectMemoryIndex builds a lightweight index of project memory files
// (file names only) and injects it as a hint. The LLM should use read_file to
// load the full content of any relevant memory file — this keeps the system
// prompt small while still surfacing the existence of project conventions.
//
// This mirrors the auto-memory index approach: titles/names are reference
// context, not instructions, and full content is retrieved on demand.
func applyProjectMemoryIndex(agentInst *agent.Agent, workingDir string) ([]string, error) {
	if agentInst == nil || strings.TrimSpace(workingDir) == "" {
		return nil, nil
	}
	files, err := memory.ProjectMemoryFilesForPath(workingDir)
	if err != nil || len(files) == 0 {
		return nil, err
	}
	agentInst.SetProjectMemoryFiles(files)

	var names []string
	seen := make(map[string]struct{})
	for _, f := range files {
		name := filepath.Base(f)
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		names = append(names, name)
	}
	if len(names) == 0 {
		return files, nil
	}
	slices.Sort(names)

	var b strings.Builder
	b.WriteString("⚠️ IMPORTANT: Project Memory files contain critical project conventions, rules, and constraints that you MUST follow. They are NOT loaded into context automatically.\n\n")
	b.WriteString("The following project memory files exist and should be loaded with read_file when relevant to the current task (e.g. before editing code, configuring the project, or making architectural decisions):\n\n")
	for _, name := range names {
		b.WriteString("  - " + name + "\n")
	}
	b.WriteString("\nDo NOT assume you know the project's conventions from training data — always read the relevant memory file first when the task touches that area.")

	agentInst.AddMessage(provider.Message{
		Role:    "system",
		Content: []provider.ContentBlock{{Type: "text", Text: "## Project Memory\n" + b.String()}},
	})
	return files, nil
}

// ApplyProjectMemoryToAgent seeds the agent with project memory file paths and
// injects an index-based hint rather than the full file contents. Full content
// is loaded on demand via read_file or the path-triggered dynamic loader.
func ApplyProjectMemoryToAgent(agentInst *agent.Agent, workingDir string) ([]string, error) {
	return applyProjectMemoryIndex(agentInst, workingDir)
}
