package tool

import (
	"context"
	"os"
	"time"

	"github.com/topcheer/ggcode/internal/permission"
	"github.com/topcheer/ggcode/internal/tmux"
)

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
		MultiFileRead{SandboxCheck: sandboxFor("multi_file_read")},
		WriteFile{SandboxCheck: sandboxFor("write_file")},
		MultiFileWrite{SandboxCheck: sandboxFor("multi_file_write")},
		ListDir{SandboxCheck: sandboxFor("list_directory")},
		EditFile{SandboxCheck: sandboxFor("edit_file")},
		MultiFileEdit{SandboxCheck: sandboxFor("multi_file_edit")},

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

		// Debug log access
		DebugLogTool{},

		// Worktree
		&EnterWorktree{WorkingDir: workingDir},
		&ExitWorktree{WorkingDir: workingDir},

		// Execution
		&RunCommand{WorkingDir: workingDir, Policy: policy},
		StartCommandTool{Manager: jobManager, Policy: policy},
		ReadCommandOutputTool{Manager: jobManager},
		WaitCommandTool{Manager: jobManager},
		StopCommandTool{Manager: jobManager},
		WriteCommandInputTool{Manager: jobManager},
		ListCommandsTool{Manager: jobManager},

		// Git (read-only)
		&GitStatus{},
		&GitDiff{},
		&GitLog{},
		&GitShow{},
		&GitBlame{},
		&GitBranchList{},
		&GitRemote{},
		&GitStashList{},

		// Git (write — require approval)
		&GitAdd{},
		&GitCommit{},
		&GitStash{},

		// Web
		WebFetch{},
		WebSearch{},
		NewBrowser(),

		// Productivity
		NewAskUserTool(),
		NewTodoWrite(""), // session ID set later via agent.SetSessionID()

		// Permission
		NewSwitchModeTool(policy),

		// IM (manager injected post-registration via SetManager)
		IMTool{},

		// Screenshot
		ScreenshotTool{},
	)
	for _, tool := range tools {
		if err := registry.Register(tool); err != nil {
			return err
		}
	}
	if shouldRegisterTmuxTool(tmux.NewClient()) {
		if err := registry.Register(NewTmuxTool(workingDir)); err != nil {
			return err
		}
	}
	if shouldRegisterGhosttyTool() {
		if err := registry.Register(NewGhosttyTool(workingDir)); err != nil {
			return err
		}
	}
	if shouldRegisterWarpTool() {
		if err := registry.Register(NewWarpTool(workingDir)); err != nil {
			return err
		}
	}
	if shouldRegisterKittyTool() {
		if err := registry.Register(NewKittyTool(workingDir)); err != nil {
			return err
		}
	}
	if shouldRegisterITerm2Tool() {
		if err := registry.Register(NewIterm2Tool(workingDir)); err != nil {
			return err
		}
	}
	if shouldRegisterMobileDevice() {
		if err := registry.Register(NewMobileDeviceTool()); err != nil {
			return err
		}
	}

	return nil
}

func shouldRegisterGhosttyTool() bool {
	return os.Getenv("TERM_PROGRAM") == "ghostty"
}

func shouldRegisterWarpTool() bool {
	return os.Getenv("TERM_PROGRAM") == "WarpTerminal"
}

func shouldRegisterKittyTool() bool {
	// Kitty sets TERM_PROGRAM=kitty, but this can be lost when running
	// through tmux, screen, or other wrappers. KITTY_WINDOW_ID is always
	// set by kitty and is a reliable fallback.
	return os.Getenv("TERM_PROGRAM") == "kitty" || os.Getenv("KITTY_WINDOW_ID") != ""
}

func shouldRegisterITerm2Tool() bool {
	return os.Getenv("TERM_PROGRAM") == "iTerm.app"
}

func shouldRegisterTmuxTool(client *tmux.Client) bool {
	if client == nil {
		client = tmux.NewClient()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	env, err := client.Detect(ctx)
	return err == nil && env != nil && env.Available && env.InTmux
}
