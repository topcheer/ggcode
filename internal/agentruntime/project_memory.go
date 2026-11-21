package agentruntime

import (
	"strings"

	"github.com/topcheer/ggcode/internal/agent"
	"github.com/topcheer/ggcode/internal/memory"
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
		// #5892 round-2: stable content rides the assembled head; a bare
		// system-role AddMessage lands at the head position on an empty
		// message list and forces a full-rewrite on first assembly.
		agentInst.AppendStableSystemSection(hint)
	}
	return files, nil
}
