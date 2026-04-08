package tool

import "github.com/topcheer/ggcode/internal/permission"

// RegisterBuiltinTools registers all built-in tools.
// If policy is nil, no sandbox path checking is enforced (permissive mode).
func RegisterBuiltinTools(registry *Registry, policy permission.PermissionPolicy, workingDir string) error {
	var sandbox AllowedPathChecker
	if policy != nil {
		sandbox = func(path string) bool {
			return policy.AllowedPath(path)
		}
	}
	jobManager := NewCommandJobManager(workingDir)
	tools := []Tool{
		// File operations
		ReadFile{SandboxCheck: sandbox},
		WriteFile{SandboxCheck: sandbox},
		ListDir{SandboxCheck: sandbox},
		EditFile{SandboxCheck: sandbox},

		// Search
		SearchFiles{SandboxCheck: sandbox},
		Glob{SandboxCheck: sandbox},

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
		NewTodoWrite(""),
	}
	for _, t := range tools {
		if err := registry.Register(t); err != nil {
			return err
		}
	}
	return nil
}
