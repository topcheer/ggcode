package agentruntime

import (
	"strings"

	"github.com/topcheer/ggcode/internal/agent"
	"github.com/topcheer/ggcode/internal/memory"
	"github.com/topcheer/ggcode/internal/provider"
)

func ApplyProjectMemoryToAgent(agentInst *agent.Agent, workingDir string) ([]string, error) {
	if agentInst == nil || strings.TrimSpace(workingDir) == "" {
		return nil, nil
	}
	files, err := memory.ProjectMemoryFilesForPath(workingDir)
	if err != nil || len(files) == 0 {
		return nil, err
	}
	agentInst.SetProjectMemoryFiles(files)

	hint := memory.BuildProjectMemoryHint(files, workingDir)
	if hint != "" {
		agentInst.AddMessage(provider.Message{
			Role:    "system",
			Content: []provider.ContentBlock{{Type: "text", Text: hint}},
		})
	}
	return files, nil
}
