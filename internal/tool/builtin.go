package tool

import "github.com/topcheer/ggcode/internal/permission"

// RegisterBuiltinTools registers all built-in tools.
// If policy is nil, no sandbox path checking is enforced (permissive mode).
func RegisterBuiltinTools(registry *Registry, policy permission.PermissionPolicy, workingDir string) error {
	sandboxFor := func(toolName string) AllowedPathChecker {
		if policy == nil {
			return nil
		}
		return func(path string) bool {
			return policy.AllowedPathForTool(toolName, path)
		}
	}
	jobManager := NewCommandJobManager(workingDir)
	tools := []Tool{
		// File operations
		ReadFile{SandboxCheck: sandboxFor("read_file")},
		WriteFile{SandboxCheck: sandboxFor("write_file")},
		ListDir{SandboxCheck: sandboxFor("list_directory")},
		EditFile{SandboxCheck: sandboxFor("edit_file")},

		// Search
		SearchFiles{SandboxCheck: sandboxFor("search_files")},
		Grep{SandboxCheck: sandboxFor("grep")},
		Glob{SandboxCheck: sandboxFor("glob")},
	}
	tools = append(tools, NewLSPTools(workingDir, sandboxFor("read_file"), sandboxFor("edit_file"))...)
	tools = append(tools,

		// Multi-edit and notebook
		MultiEditFile{SandboxCheck: sandboxFor("multi_edit_file")},
		NotebookEdit{SandboxCheck: sandboxFor("notebook_edit")},

		// Sleep
		SleepTool{},

		// Worktree
		EnterWorktree{WorkingDir: workingDir},
		ExitWorktree{WorkingDir: workingDir},

		// Execution
		RunCommand{WorkingDir: workingDir},
		StartCommandTool{Manager: jobManager},
		ReadCommandOutputTool{Manager: jobManager},
		WaitCommandTool{Manager: jobManager},
		StopCommandTool{Manager: jobManager},
		WriteCommandInputTool{Manager: jobManager},
		ListCommandsTool{Manager: jobManager},

		// Git
		GitStatus{},
		GitDiff{},
		GitLog{},

		// Web
		WebFetch{},
		WebSearch{},

		// Productivity
		NewAskUserTool(),
		NewWorkspaceTodoWrite(workingDir),
	)
	for _, t := range tools {
		if err := registry.Register(t); err != nil {
			return err
		}
	}
	return nil
}
