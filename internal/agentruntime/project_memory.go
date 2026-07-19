package agentruntime

import (
	"strings"

	"github.com/topcheer/ggcode/internal/agent"
	"github.com/topcheer/ggcode/internal/memory"
	"github.com/topcheer/ggcode/internal/provider"
)

// ApplyProjectMemoryToAgent seeds the agent with project memory file paths and
// injects an index-based hint rather than the full file contents. Full content
// is loaded on demand via read_file or the path-triggered dynamic loader.
func ApplyProjectMemoryToAgent(agentInst *agent.Agent, workingDir string) ([]string, error) {
	if agentInst == nil || strings.TrimSpace(workingDir) == "" {
		return nil, nil
	}
	files, err := memory.ProjectMemoryFilesForPath(workingDir)
	if err != nil || len(files) == 0 {
		return nil, err
	}
	agentInst.SetProjectMemoryFiles(files)

	hint := memory.BuildProjectMemoryHint(files)
	if hint != "" {
		agentInst.AddMessage(provider.Message{
			Role:    "system",
			Content: []provider.ContentBlock{{Type: "text", Text: hint}},
		})
	}
	return files, nil
}
