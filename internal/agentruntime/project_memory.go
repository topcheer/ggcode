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
	content, files, err := memory.LoadProjectMemory(workingDir)
	if err != nil {
		return nil, err
	}
	if len(files) > 0 {
		agentInst.SetProjectMemoryFiles(files)
	}
	if strings.TrimSpace(content) != "" {
		agentInst.AddMessage(provider.Message{
			Role:    "system",
			Content: []provider.ContentBlock{{Type: "text", Text: "## Project Memory\n" + content}},
		})
	}
	return files, nil
}
