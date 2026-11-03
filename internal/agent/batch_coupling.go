package agent

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/topcheer/ggcode/internal/debug"
)

type couplingRule struct {
	prereqTool    string
	dependentTool string
	matchFn       func(prereqArgs, depArgs json.RawMessage) bool
	description   string
}

type batchCouplingState struct {
	mu sync.Mutex

	maxWarns    int
	warnsIssued int
}

func newBatchCouplingState() *batchCouplingState {
	return &batchCouplingState{
		maxWarns: 2,
	}
}

func (s *batchCouplingState) reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.warnsIssued = 0
}

var couplingRules = []couplingRule{
	{
		prereqTool:    "file_ops",
		dependentTool: "write_file",
		description:   "file_ops(mkdir) + write_file to that directory (write needs the directory to exist first)",
		matchFn:       matchMkdirToFilePath,
	},
	{
		prereqTool:    "file_ops",
		dependentTool: "edit_file",
		description:   "file_ops(mkdir) + edit_file in that directory (edit needs the directory to exist first)",
		matchFn:       matchMkdirToFilePath,
	},
	{
		prereqTool:    "file_ops",
		dependentTool: "multi_edit_file",
		description:   "file_ops(mkdir) + multi_edit_file in that directory (edit needs the directory to exist first)",
		matchFn:       matchMkdirToFilePath,
	},
	{
		prereqTool:    "edit_file",
		dependentTool: "read_file",
		description:   "edit_file + read_file on the same file (read depends on edited content -- sequence them)",
		matchFn:       matchSameFilePath,
	},
	{
		prereqTool:    "multi_edit_file",
		dependentTool: "read_file",
		description:   "multi_edit_file + read_file on the same file (read depends on edited content)",
		matchFn:       matchSameFilePath,
	},
	{
		prereqTool:    "write_file",
		dependentTool: "read_file",
		description:   "write_file + read_file on the same file (read would get the content you just wrote -- consider skipping)",
		matchFn:       matchSameFilePath,
	},
	{
		prereqTool:    "git_checkout",
		dependentTool: "read_file",
		description:   "git_checkout + read_file (file content may differ on the target branch -- read AFTER checkout completes)",
		matchFn:       nil,
	},
	{
		prereqTool:    "git_add",
		dependentTool: "git_commit",
		description:   "git_add + git_commit in the same batch (commit needs the staged files -- run add first)",
		matchFn:       nil,
	},
}

type couplingToolCall struct {
	name string
	args json.RawMessage
}

func (s *batchCouplingState) checkBatchCoupling(toolCalls []couplingToolCall) string {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.warnsIssued >= s.maxWarns {
		return ""
	}
	if len(toolCalls) < 2 {
		return ""
	}

	var findings []string
	seen := make(map[string]bool)

	for i, prereq := range toolCalls {
		for j, dep := range toolCalls {
			if i == j {
				continue
			}

			for _, rule := range couplingRules {
				if rule.prereqTool != prereq.name || rule.dependentTool != dep.name {
					continue
				}
				if rule.matchFn != nil && !rule.matchFn(prereq.args, dep.args) {
					continue
				}

				key := rule.description
				if seen[key] {
					continue
				}
				seen[key] = true
				findings = append(findings, rule.description)
			}
		}
	}

	if len(findings) == 0 {
		return ""
	}

	s.warnsIssued++
	debug.Log("batch-coupling", "detected %d coupling pattern(s) in batch of %d calls (warning %d/%d)",
		len(findings), len(toolCalls), s.warnsIssued, s.maxWarns)

	var sb strings.Builder
	sb.WriteString("## Parallel Tool Call Coupling Warning\n\n")
	sb.WriteString("Your current batch of tool calls contains hidden sequential dependencies.\n")
	sb.WriteString("These calls may fail or produce stale results if executed in parallel\n")
	sb.WriteString("(regardless of the order you listed them in — sequence them explicitly):\n\n")
	for _, f := range findings {
		sb.WriteString(fmt.Sprintf("  - %s\n", f))
	}
	sb.WriteString("\n")
	sb.WriteString("**Guidance**: Sequence the dependent call AFTER the prerequisite completes.\n")
	sb.WriteString("Issue the prerequisite tool call first, wait for its result, then issue the dependent call.\n")
	if s.warnsIssued >= s.maxWarns {
		sb.WriteString("\n*(This is the last coupling warning for this run.)*\n")
	}

	return sb.String()
}

func matchMkdirToFilePath(prereqArgs, depArgs json.RawMessage) bool {
	dirPath := extractMkdirPath(prereqArgs)
	if dirPath == "" {
		return false
	}
	filePath := couplingExtractPath(depArgs)
	if filePath == "" {
		return false
	}
	dirPath = strings.TrimSuffix(dirPath, "/")
	return strings.HasPrefix(filePath, dirPath+"/")
}

func matchSameFilePath(prereqArgs, depArgs json.RawMessage) bool {
	prior := couplingExtractPath(prereqArgs)
	later := couplingExtractPath(depArgs)
	if prior == "" || later == "" {
		return false
	}
	return prior == later
}

func extractMkdirPath(args json.RawMessage) string {
	var parsed struct {
		Operations []struct {
			Action string `json:"action"`
			Source string `json:"source"`
		} `json:"operations"`
	}
	if err := json.Unmarshal(args, &parsed); err != nil {
		return ""
	}
	for _, op := range parsed.Operations {
		if op.Action == "mkdir" {
			return op.Source
		}
	}
	return ""
}

func couplingExtractPath(args json.RawMessage) string {
	var parsed struct {
		FilePath string `json:"file_path"`
		Path     string `json:"path"`
	}
	if err := json.Unmarshal(args, &parsed); err != nil {
		return ""
	}
	if parsed.FilePath != "" {
		return parsed.FilePath
	}
	return parsed.Path
}
