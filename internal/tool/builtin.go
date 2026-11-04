package tool

import (
	"context"
	"os"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/permission"
	"github.com/topcheer/ggcode/internal/tmux"
)

func RegisterBuiltinTools(registry *Registry, policy permission.PermissionPolicy, workingDir string, protectedPaths []string, sandbox *SandboxPolicy) error {

	secLedger := &SecurityLedger{}
	riskLedger := NewAllowedRiskLedger()
	fileGuard := NewFileGuard(protectedPaths)
	debug.Log("fileguard", "initialized with %d patterns: %v", len(fileGuard.Patterns()), fileGuard.Patterns())

	sandboxFor := func(toolName string) AllowedPathChecker {
		var base AllowedPathChecker
		if policy != nil {
			base = func(path string) bool {
				return policy.AllowedPathForTool(toolName, path)
			}
		}
		return MergeFileGuards(base, fileGuard, workingDir)
	}
	readSandboxFor := func(toolName string) AllowedPathChecker {
		if policy == nil {
			return nil
		}
		return func(path string) bool {
			return policy.AllowedPathForTool(toolName, path)
		}
	}
	jobManager := NewCommandJobManager(workingDir)
	jobManager.SetSandboxPolicy(sandbox)
	registry.jobManager = jobManager
	codeIndex := NewCodeIndexManager(workingDir)
	registry.codeIndex = codeIndex
	tools := []Tool{

		ReadFile{SandboxCheck: readSandboxFor("read_file")},
		MultiFileRead{SandboxCheck: readSandboxFor("multi_file_read")},
		WriteFile{SandboxCheck: sandboxFor("write_file"), WorkingDir: workingDir},
		MultiFileWrite{SandboxCheck: sandboxFor("multi_file_write"), WorkingDir: workingDir},

		ApplyPatch{SandboxCheck: sandboxFor("apply_patch"), WorkingDir: workingDir},
		ListDir{SandboxCheck: readSandboxFor("list_directory")},
		EditFile{SandboxCheck: sandboxFor("edit_file"), WorkingDir: workingDir},
		MultiEditFile{SandboxCheck: sandboxFor("multi_edit_file"), WorkingDir: workingDir},
		MultiFileEdit{SandboxCheck: sandboxFor("multi_file_edit"), WorkingDir: workingDir},
		BatchReplace{SandboxCheck: sandboxFor("batch_replace"), WorkingDir: workingDir},
		FileOps{SandboxCheck: sandboxFor("file_ops"), WorkingDir: workingDir},

		SearchFiles{SandboxCheck: readSandboxFor("search_files")},
		Grep{SandboxCheck: readSandboxFor("grep")},
		Glob{SandboxCheck: readSandboxFor("glob")},
		CodeSearch{SandboxCheck: readSandboxFor("code_search"), Index: codeIndex},
		SessionRecall{Registry: registry},
	}
	tools = append(tools, NewLSPTools(workingDir, readSandboxFor("read_file"), sandboxFor("edit_file"))...)
	tools = append(tools,

		NotebookEdit{SandboxCheck: sandboxFor("notebook_edit")},

		SleepTool{},

		DebugLogTool{},

		&EnterWorktree{WorkingDir: workingDir},
		&ExitWorktree{WorkingDir: workingDir},
		&ListWorktree{WorkingDir: workingDir},
		MergeWorktree{WorkingDir: workingDir},

		&RunCommand{WorkingDir: workingDir, Policy: policy, Sandbox: sandbox, SecLedger: secLedger, RiskLedger: riskLedger},
		StartCommandTool{Manager: jobManager, Policy: policy, SecLedger: secLedger, RiskLedger: riskLedger},
		ReadCommandOutputTool{Manager: jobManager},
		WaitCommandTool{Manager: jobManager},
		StopCommandTool{Manager: jobManager},
		WriteCommandInputTool{Manager: jobManager},
		ListCommandsTool{Manager: jobManager},

		&GitStatus{},
		&GitDiff{},
		&GitLog{},
		&GitShow{},
		&GitBlame{},
		&GitBranchList{},
		&GitRemote{},
		&GitStashList{},

		&GitAdd{},
		&GitCommit{},
		&GitStash{},
		&GitCheckout{},
		&GitRevert{},
		&GitReset{},
		&GitTag{},

		WebFetch{},
		WebSearch{},
		NewBrowser(),

		NewAskUserTool(),
		NewTodoWrite(""),
		NewCurrentTimeTool(),

		NewSwitchModeTool(policy),

		&RestartTool{},

		IMTool{},

		MobileFileTool{},

		StartShareTool{},
		StopShareTool{},

		RuntimeTool{},

		ScreenshotTool{},

		DesktopControlTool{WorkingDir: workingDir},

		ClipboardTool{},

		OpenEditorTool{WorkingDir: workingDir},

		CodeHealthTool{WorkingDir: workingDir},

		ScanTodosTool{WorkingDir: workingDir},

		UndoEditTool{},

		CIStatusTool{WorkingDir: workingDir},

		ReviewChanges{WorkingDir: workingDir},

		&CmdSnippetTool{WorkingDir: workingDir},

		DepGraphTool{WorkingDir: workingDir},

		&KnowledgeGraphTool{WorkingDir: workingDir},

		&GoalGraphTool{WorkingDir: workingDir},

		ScaffoldProject{SandboxCheck: sandboxFor("scaffold_project"), WorkingDir: workingDir},
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

	if err := registry.Register(CodeExecution{Registry: registry}); err != nil {
		return err
	}

	if err := registry.Register(&MacroTool{WorkingDir: workingDir, Registry: registry}); err != nil {
		return err
	}

	if err := registry.Register(&DefineTool{WorkingDir: workingDir, Registry: registry}); err != nil {
		return err
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
