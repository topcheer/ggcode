package harness

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

type fakeRunner struct {
	result *RunResult
	err    error
	seen   *[]RunRequest
}

func (r fakeRunner) Run(_ context.Context, req RunRequest) (*RunResult, error) {
	if r.seen != nil {
		*r.seen = append(*r.seen, req)
	}
	return r.result, r.err
}

type streamingRunner struct {
	result *RunResult
	err    error
	stdout []string
	stderr []string
}

func (r streamingRunner) Run(_ context.Context, req RunRequest) (*RunResult, error) {
	for _, chunk := range r.stdout {
		if req.OnOutput != nil {
			req.OnOutput(chunk)
		}
	}
	for _, line := range r.stderr {
		if req.OnProgress != nil {
			req.OnProgress(line)
		}
	}
	return r.result, r.err
}

type sequenceRunner struct {
	results []*RunResult
	errs    []error
	seen    *[]RunRequest
	index   int
}

func (r *sequenceRunner) Run(_ context.Context, req RunRequest) (*RunResult, error) {
	if r.seen != nil {
		*r.seen = append(*r.seen, req)
	}
	idx := r.index
	r.index++
	var result *RunResult
	if idx < len(r.results) {
		result = r.results[idx]
	}
	var err error
	if idx < len(r.errs) {
		err = r.errs[idx]
	}
	return result, err
}

type blockingRunner struct {
	started chan struct{}
	release chan struct{}
	result  *RunResult
	err     error
}

func (r blockingRunner) Run(ctx context.Context, req RunRequest) (*RunResult, error) {
	if r.started != nil {
		select {
		case r.started <- struct{}{}:
		default:
		}
	}
	if r.release != nil {
		select {
		case <-r.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return r.result, r.err
}

type writingRunner struct {
	relPath string
	body    string
	result  *RunResult
	err     error
}

func (r writingRunner) Run(_ context.Context, req RunRequest) (*RunResult, error) {
	if strings.TrimSpace(r.relPath) != "" {
		path := filepath.Join(req.WorkingDir, r.relPath)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			return nil, err
		}
		if err := os.WriteFile(path, []byte(r.body), 0644); err != nil {
			return nil, err
		}
	}
	return r.result, r.err
}

func TestInitCreatesHarnessScaffold(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "cmd"), 0755); err != nil {
		t.Fatalf("mkdir cmd: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "internal", "inventory"), 0755); err != nil {
		t.Fatalf("mkdir internal/inventory: %v", err)
	}
	result, err := Init(root, InitOptions{Goal: "Build an ERP system"})
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	for _, rel := range []string{
		ConfigRelPath,
		filepath.Join(StateRelDir, "events.jsonl"),
		filepath.Join(StateRelDir, "snapshot.db"),
		"AGENTS.md",
		filepath.Join("cmd", "AGENTS.md"),
		filepath.Join("internal", "inventory", "AGENTS.md"),
		filepath.Join("docs", "runbooks", "harness.md"),
	} {
		if _, err := os.Stat(filepath.Join(root, rel)); err != nil {
			t.Fatalf("expected %s to exist: %v", rel, err)
		}
	}
	if result.Config == nil || result.Config.Project.Goal != "Build an ERP system" {
		t.Fatalf("unexpected init config: %+v", result.Config)
	}
	if len(result.Config.Contexts) == 0 {
		t.Fatalf("expected detected contexts, got %+v", result.Config.Contexts)
	}
	if !result.GitInitialized {
		t.Fatalf("expected init to bootstrap git repository")
	}
	data, err := os.ReadFile(filepath.Join(root, ConfigRelPath))
	if err != nil {
		t.Fatalf("read harness config: %v", err)
	}
	if strings.Contains(string(data), "max_iterations:") {
		t.Fatalf("expected scaffolded harness config to omit max_iterations, got:\n%s", string(data))
	}
}

func TestDiscoverFindsHarnessRootFromNestedDir(t *testing.T) {
	root := t.TempDir()
	if _, err := Init(root, InitOptions{}); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	nested := filepath.Join(root, "cmd", "app")
	if err := os.MkdirAll(nested, 0755); err != nil {
		t.Fatalf("mkdir nested: %v", err)
	}
	project, err := Discover(nested)
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	if project.RootDir != root {
		t.Fatalf("project.RootDir = %q, want %q", project.RootDir, root)
	}
}

func TestInitBootstrapsGitRepository(t *testing.T) {
	root := t.TempDir()
	result, err := Init(root, InitOptions{})
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	if !result.GitInitialized {
		t.Fatalf("expected git repo bootstrap, got %+v", result)
	}
	if _, err := os.Stat(filepath.Join(root, ".git")); err != nil {
		t.Fatalf("expected .git to exist: %v", err)
	}
	if strings.TrimSpace(result.ScaffoldCommit) == "" {
		t.Fatalf("expected scaffold commit to be created")
	}
	if !hasHeadCommit(context.Background(), root) {
		t.Fatalf("expected init to leave repository with a HEAD commit")
	}
}

func TestInitCreatesIndependentHarnessScaffoldCommit(t *testing.T) {
	root := t.TempDir()
	result, err := Init(root, InitOptions{})
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	cmd := exec.Command("git", "show", "--stat", "--format=%s", result.ScaffoldCommit)
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git show scaffold commit: %v\n%s", err, string(out))
	}
	text := string(out)
	if !strings.Contains(text, "chore: initialize harness scaffold") {
		t.Fatalf("expected scaffold commit message, got:\n%s", text)
	}
	for _, rel := range []string{ConfigRelPath, "AGENTS.md", filepath.Join("docs", "runbooks", "harness.md")} {
		if !strings.Contains(text, rel) {
			t.Fatalf("expected scaffold commit to include %s, got:\n%s", rel, text)
		}
	}
}

func TestInitAllowsImmediateWorktreePreparationInEmptyRepo(t *testing.T) {
	root := t.TempDir()
	result, err := Init(root, InitOptions{})
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	task, err := EnqueueTask(result.Project, "Prepare worktree right after init", "cli")
	if err != nil {
		t.Fatalf("EnqueueTask() error = %v", err)
	}
	workspace, err := PrepareWorkspace(context.Background(), result.Project, result.Config, task)
	if err != nil {
		t.Fatalf("PrepareWorkspace() error = %v", err)
	}
	defer func() {
		task.WorkspacePath = workspace.Path
		_ = cleanupWorkspace(result.Project, task)
	}()
	if workspace.Mode != "git-worktree" {
		t.Fatalf("workspace.Mode = %q, want git-worktree", workspace.Mode)
	}
}

func TestCheckProjectPassesForScaffold(t *testing.T) {
	root := t.TempDir()
	git(t, root, "init")
	if err := os.MkdirAll(filepath.Join(root, "cmd"), 0755); err != nil {
		t.Fatalf("mkdir cmd: %v", err)
	}
	result, err := Init(root, InitOptions{})
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	cfg := result.Config
	cfg.Checks.Commands = nil
	report, err := CheckProject(context.Background(), result.Project, cfg, CheckOptions{})
	if err != nil {
		t.Fatalf("CheckProject() error = %v", err)
	}
	if !report.Passed {
		t.Fatalf("expected scaffolded project to pass, got %+v", report)
	}
}

func TestCheckProjectFailsWhenContextAgentMissing(t *testing.T) {
	root := t.TempDir()
	git(t, root, "init")
	if err := os.MkdirAll(filepath.Join(root, "cmd"), 0755); err != nil {
		t.Fatalf("mkdir cmd: %v", err)
	}
	result, err := Init(root, InitOptions{})
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	if err := os.Remove(filepath.Join(root, "cmd", "AGENTS.md")); err != nil {
		t.Fatalf("remove context agent: %v", err)
	}
	result.Config.Checks.Commands = nil
	report, err := CheckProject(context.Background(), result.Project, result.Config, CheckOptions{})
	if err != nil {
		t.Fatalf("CheckProject() error = %v", err)
	}
	if report.Passed {
		t.Fatalf("expected missing context AGENTS to fail check")
	}
}

func TestRunTaskPersistsCompletedTaskAndLog(t *testing.T) {
	root := t.TempDir()
	git(t, root, "init")
	git(t, root, "init")
	result, err := Init(root, InitOptions{})
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	summary, err := RunTask(context.Background(), result.Project, result.Config, "Implement ERP inventory module", fakeRunner{
		result: &RunResult{Output: "done"},
	})
	if err != nil {
		t.Fatalf("RunTask() error = %v", err)
	}
	if summary.Task == nil || summary.Task.Status != TaskCompleted {
		t.Fatalf("expected completed task, got %+v", summary.Task)
	}
	data, err := os.ReadFile(summary.Task.LogPath)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	if strings.TrimSpace(string(data)) != "done" {
		t.Fatalf("log = %q, want done", string(data))
	}
	loaded, err := LoadTask(result.Project, summary.Task.ID)
	if err != nil {
		t.Fatalf("LoadTask() error = %v", err)
	}
	if loaded.Status != TaskCompleted {
		t.Fatalf("loaded.Status = %q, want %q", loaded.Status, TaskCompleted)
	}
	if loaded.VerificationStatus != VerificationPassed {
		t.Fatalf("loaded.VerificationStatus = %q, want %q", loaded.VerificationStatus, VerificationPassed)
	}
	if loaded.ReviewStatus != ReviewPending {
		t.Fatalf("loaded.ReviewStatus = %q, want %q", loaded.ReviewStatus, ReviewPending)
	}
	if loaded.VerificationReportPath == "" {
		t.Fatalf("expected delivery report path to be recorded")
	}
	if _, err := os.Stat(loaded.VerificationReportPath); err != nil {
		t.Fatalf("expected delivery report to exist: %v", err)
	}
}

func TestBinaryRunnerStreamsStdoutChunksWithoutWaitingForNewline(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script helper is unix-only")
	}
	root := t.TempDir()
	script := filepath.Join(root, "runner.sh")
	content := "#!/bin/sh\nprintf 'hello'\nsleep 0.1\nprintf ' world\\n'\nprintf 'tool: read_file README.md\\n' >&2\n"
	if err := os.WriteFile(script, []byte(content), 0o755); err != nil {
		t.Fatalf("write helper script: %v", err)
	}
	var (
		outputs  []string
		progress []string
	)
	result, err := (BinaryRunner{Executable: script}).Run(context.Background(), RunRequest{
		WorkingDir: root,
		Prompt:     "ignored",
		OnOutput: func(chunk string) {
			outputs = append(outputs, chunk)
		},
		OnProgress: func(line string) {
			progress = append(progress, line)
		},
	})
	if err != nil {
		t.Fatalf("BinaryRunner.Run() error = %v", err)
	}
	if len(outputs) < 2 {
		t.Fatalf("expected multiple streamed stdout chunks, got %#v", outputs)
	}
	if outputs[0] != "hello" {
		t.Fatalf("expected first stdout chunk before newline, got %#v", outputs)
	}
	if strings.Join(outputs, "") != "hello world\n" {
		t.Fatalf("unexpected streamed stdout = %#v", outputs)
	}
	if len(progress) != 1 || progress[0] != "tool: read_file README.md" {
		t.Fatalf("unexpected progress lines = %#v", progress)
	}
	if !strings.Contains(result.Output, "hello") || !strings.Contains(result.Output, "world") || !strings.Contains(result.Output, "tool: read_file README.md") {
		t.Fatalf("expected combined run output, got %q", result.Output)
	}
}

func TestBinaryRunnerPassesSandboxFlags(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script helper is unix-only")
	}
	root := t.TempDir()
	script := filepath.Join(root, "runner.sh")
	content := "#!/bin/sh\nprintf '%s\n' \"$@\"\n"
	if err := os.WriteFile(script, []byte(content), 0o755); err != nil {
		t.Fatalf("write helper script: %v", err)
	}
	result, err := (BinaryRunner{Executable: script}).Run(context.Background(), RunRequest{
		GGCodeConfigPath:    filepath.Join(root, "ggcode.yaml"),
		AllowedDirs:         []string{filepath.Join(root, "worktree")},
		ReadOnlyAllowedDirs: []string{filepath.Join(root, ".ggcode")},
		WorkingDir:          root,
		Prompt:              "ignored",
	})
	if err != nil {
		t.Fatalf("BinaryRunner.Run() error = %v", err)
	}
	for _, want := range []string{
		"--config",
		filepath.Join(root, "ggcode.yaml"),
		"--allowedDir",
		filepath.Join(root, "worktree"),
		"--readOnlyAllowedDir",
		filepath.Join(root, ".ggcode"),
		"--bypass",
		"--prompt",
		"ignored",
	} {
		if !strings.Contains(result.Output, want) {
			t.Fatalf("expected BinaryRunner args to contain %q, got %q", want, result.Output)
		}
	}
}

func TestExecuteTaskPersistsWorkerStderrAndDetailedExitError(t *testing.T) {
	root := t.TempDir()
	git(t, root, "init")
	result, err := Init(root, InitOptions{})
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	result.Config.Run.WorktreeMode = "off"
	task, err := EnqueueTask(result.Project, "Investigate ERP failure", "cli")
	if err != nil {
		t.Fatalf("EnqueueTask() error = %v", err)
	}
	summary, err := ExecuteTask(context.Background(), result.Project, result.Config, task, streamingRunner{
		stdout: []string{"partial stdout\n"},
		stderr: []string{"tool: read_file README.md", "fatal: missing API key"},
		result: &RunResult{
			Output:   "partial stdout\nfatal: missing API key\n",
			ExitCode: 1,
		},
	})
	if err != nil {
		t.Fatalf("ExecuteTask() error = %v", err)
	}
	if summary.Task.Status != TaskFailed {
		t.Fatalf("Status = %q, want %q", summary.Task.Status, TaskFailed)
	}
	if !strings.Contains(summary.Task.Error, "fatal: missing API key") {
		t.Fatalf("expected detailed exit error, got %q", summary.Task.Error)
	}
	logData, err := os.ReadFile(summary.Task.LogPath)
	if err != nil {
		t.Fatalf("ReadFile(log) error = %v", err)
	}
	logText := string(logData)
	if !strings.Contains(logText, "fatal: missing API key") {
		t.Fatalf("expected stderr to be persisted in task log, got %q", logText)
	}
	if !strings.Contains(logText, "tool: read_file README.md") {
		t.Fatalf("expected progress line in task log, got %q", logText)
	}
}

func TestRunTaskWithContextPersistsContextMetadata(t *testing.T) {
	root := t.TempDir()
	git(t, root, "init")
	if err := os.MkdirAll(filepath.Join(root, "internal", "inventory"), 0755); err != nil {
		t.Fatalf("mkdir context: %v", err)
	}
	git(t, root, "init")
	result, err := Init(root, InitOptions{})
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	var inventoryCtx *ContextConfig
	for _, contextCfg := range result.Config.Contexts {
		if contextCfg.Path == filepath.Join("internal", "inventory") {
			ctxCopy := contextCfg
			ctxCopy.Owner = "inventory-team"
			inventoryCtx = &ctxCopy
			for i := range result.Config.Contexts {
				if result.Config.Contexts[i].Path == ctxCopy.Path {
					result.Config.Contexts[i].Owner = ctxCopy.Owner
				}
			}
			break
		}
	}
	if inventoryCtx == nil {
		t.Fatal("expected internal/inventory context")
	}
	var seen []RunRequest
	summary, err := RunTaskWithOptions(context.Background(), result.Project, result.Config, "Implement ERP inventory module", fakeRunner{
		result: &RunResult{Output: "done"},
		seen:   &seen,
	}, RunTaskOptions{ContextName: inventoryCtx.Name, ContextPath: inventoryCtx.Path})
	if err != nil {
		t.Fatalf("RunTaskWithOptions() error = %v", err)
	}
	if summary.Task.ContextName != inventoryCtx.Name || summary.Task.ContextPath != inventoryCtx.Path {
		t.Fatalf("task context = %q/%q", summary.Task.ContextName, summary.Task.ContextPath)
	}
	if len(seen) != 1 || !strings.Contains(seen[0].Prompt, inventoryCtx.Path) || !strings.Contains(seen[0].Prompt, inventoryCtx.Owner) {
		t.Fatalf("expected prompt to mention context path, saw %+v", seen)
	}
}

func TestQueueAndRunQueuedTasksUsesOldestFirst(t *testing.T) {
	root := t.TempDir()
	git(t, root, "init")
	git(t, root, "init")
	result, err := Init(root, InitOptions{})
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	first, err := EnqueueTask(result.Project, "First ERP bounded context", "cli")
	if err != nil {
		t.Fatalf("EnqueueTask(first) error = %v", err)
	}
	time.Sleep(5 * time.Millisecond)
	second, err := EnqueueTask(result.Project, "Second ERP bounded context", "cli")
	if err != nil {
		t.Fatalf("EnqueueTask(second) error = %v", err)
	}
	var seen []RunRequest
	summary, err := RunQueuedTasks(context.Background(), result.Project, result.Config, fakeRunner{
		result: &RunResult{Output: "ok"},
		seen:   &seen,
	}, QueueRunOptions{All: true})
	if err != nil {
		t.Fatalf("RunQueuedTasks() error = %v", err)
	}
	if len(summary.Executed) != 2 {
		t.Fatalf("executed = %d, want 2", len(summary.Executed))
	}
	if summary.Executed[0].Task.ID != first.ID || summary.Executed[1].Task.ID != second.ID {
		t.Fatalf("unexpected execution order: %+v", summary.Executed)
	}
}

func TestDependencyBlocksUntilPrerequisiteCompletes(t *testing.T) {
	root := t.TempDir()
	git(t, root, "init")
	git(t, root, "init")
	result, err := Init(root, InitOptions{})
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	first, err := EnqueueTask(result.Project, "Build ERP inventory aggregate", "cli")
	if err != nil {
		t.Fatalf("EnqueueTask(first) error = %v", err)
	}
	second, err := EnqueueTask(result.Project, "Build ERP pricing rules", "cli", QueueOptions{DependsOn: []string{first.ID}})
	if err != nil {
		t.Fatalf("EnqueueTask(second) error = %v", err)
	}
	loadedSecond, err := LoadTask(result.Project, second.ID)
	if err != nil {
		t.Fatalf("LoadTask(second) error = %v", err)
	}
	if loadedSecond.Status != TaskBlocked {
		t.Fatalf("loadedSecond.Status = %q, want %q", loadedSecond.Status, TaskBlocked)
	}

	_, err = ExecuteTask(context.Background(), result.Project, result.Config, first, fakeRunner{result: &RunResult{Output: "ok"}})
	if err != nil {
		t.Fatalf("ExecuteTask(first) error = %v", err)
	}
	tasks, err := ListTasks(result.Project)
	if err != nil {
		t.Fatalf("ListTasks() error = %v", err)
	}
	foundQueued := false
	for _, task := range tasks {
		if task.ID == second.ID && task.Status == TaskQueued {
			foundQueued = true
		}
	}
	if !foundQueued {
		t.Fatalf("expected dependent task to unblock after prerequisite completion")
	}
}

func TestExecuteTaskPersistsDeliveryEvidenceAndChangedFiles(t *testing.T) {
	root := t.TempDir()
	git(t, root, "init")
	git(t, root, "init")
	result, err := Init(root, InitOptions{})
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	task, err := EnqueueTask(result.Project, "Implement ERP purchasing aggregate", "cli")
	if err != nil {
		t.Fatalf("EnqueueTask() error = %v", err)
	}
	summary, err := ExecuteTask(context.Background(), result.Project, result.Config, task, writingRunner{
		relPath: "internal/purchasing/service.go",
		body:    "package purchasing\n",
		result:  &RunResult{Output: "done"},
	})
	if err != nil {
		t.Fatalf("ExecuteTask() error = %v", err)
	}
	if summary.Task.VerificationStatus != VerificationPassed {
		t.Fatalf("VerificationStatus = %q, want %q", summary.Task.VerificationStatus, VerificationPassed)
	}
	if len(summary.Task.ChangedFiles) == 0 {
		t.Fatalf("expected changed files to be captured")
	}
	found := false
	for _, path := range summary.Task.ChangedFiles {
		if path == "internal/purchasing/service.go" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected changed file in %+v", summary.Task.ChangedFiles)
	}
	data, err := os.ReadFile(summary.Task.VerificationReportPath)
	if err != nil {
		t.Fatalf("read delivery report: %v", err)
	}
	var report DeliveryReport
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatalf("unmarshal delivery report: %v", err)
	}
	if report.Check == nil || !report.Check.Passed {
		t.Fatalf("expected passing verification report, got %+v", report.Check)
	}
}

func TestCheckProjectRunsContextCommands(t *testing.T) {
	root := t.TempDir()
	git(t, root, "init")
	if err := os.MkdirAll(filepath.Join(root, "internal", "inventory"), 0755); err != nil {
		t.Fatalf("mkdir context: %v", err)
	}
	result, err := Init(root, InitOptions{})
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "internal", "inventory", "marker.txt"), []byte("ok"), 0644); err != nil {
		t.Fatalf("write marker: %v", err)
	}
	result.Config.Checks.Commands = nil
	for i := range result.Config.Contexts {
		if result.Config.Contexts[i].Path == filepath.Join("internal", "inventory") {
			result.Config.Contexts[i].Commands = []CommandCheck{{Name: "marker", Run: "test -f marker.txt"}}
		}
	}
	report, err := CheckProject(context.Background(), result.Project, result.Config, CheckOptions{RunCommands: true})
	if err != nil {
		t.Fatalf("CheckProject() error = %v", err)
	}
	if !report.Passed {
		t.Fatalf("expected context checks to pass, got %+v", report)
	}
	found := false
	for _, cmd := range report.Commands {
		if cmd.Scope != "" && cmd.Scope != "root" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected context-scoped command results, got %+v", report.Commands)
	}
}

func TestExecuteTaskContextVerificationOnlyRunsSelectedContext(t *testing.T) {
	root := t.TempDir()
	git(t, root, "init")
	if err := os.MkdirAll(filepath.Join(root, "internal", "inventory"), 0755); err != nil {
		t.Fatalf("mkdir inventory: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "internal", "pricing"), 0755); err != nil {
		t.Fatalf("mkdir pricing: %v", err)
	}
	git(t, root, "init")
	result, err := Init(root, InitOptions{})
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	result.Config.Checks.Commands = nil
	for i := range result.Config.Contexts {
		switch result.Config.Contexts[i].Path {
		case filepath.Join("internal", "inventory"):
			result.Config.Contexts[i].Commands = []CommandCheck{{Name: "inventory-marker", Run: "test -f ready.txt"}}
		case filepath.Join("internal", "pricing"):
			result.Config.Contexts[i].Commands = []CommandCheck{{Name: "pricing-marker", Run: "exit 1"}}
		}
	}
	summary, err := RunTaskWithOptions(context.Background(), result.Project, result.Config, "Implement inventory slice", writingRunner{
		relPath: filepath.Join("internal", "inventory", "ready.txt"),
		body:    "ready",
		result:  &RunResult{Output: "done"},
	}, RunTaskOptions{
		ContextName: "internal-inventory",
		ContextPath: filepath.Join("internal", "inventory"),
	})
	if err != nil {
		t.Fatalf("RunTaskWithOptions() error = %v", err)
	}
	if summary.Task.Status != TaskCompleted {
		t.Fatalf("Status = %q, want %q", summary.Task.Status, TaskCompleted)
	}
	if summary.Task.VerificationStatus != VerificationPassed {
		t.Fatalf("VerificationStatus = %q, want %q", summary.Task.VerificationStatus, VerificationPassed)
	}
}

func TestExecuteTaskFailsWhenDeliveryVerificationFails(t *testing.T) {
	root := t.TempDir()
	git(t, root, "init")
	git(t, root, "init")
	result, err := Init(root, InitOptions{})
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	result.Config.Checks.Commands = []CommandCheck{
		{Name: "failing-validation", Run: "exit 1"},
	}
	task, err := EnqueueTask(result.Project, "Ship failing ERP change", "cli")
	if err != nil {
		t.Fatalf("EnqueueTask() error = %v", err)
	}
	summary, err := ExecuteTask(context.Background(), result.Project, result.Config, task, fakeRunner{
		result: &RunResult{Output: "done"},
	})
	if err != nil {
		t.Fatalf("ExecuteTask() error = %v", err)
	}
	if summary.Task.Status != TaskFailed {
		t.Fatalf("Status = %q, want %q", summary.Task.Status, TaskFailed)
	}
	if summary.Task.VerificationStatus != VerificationFailed {
		t.Fatalf("VerificationStatus = %q, want %q", summary.Task.VerificationStatus, VerificationFailed)
	}
	if !strings.Contains(summary.Task.Error, "verification failed") {
		t.Fatalf("unexpected task error: %q", summary.Task.Error)
	}
	if !strings.Contains(summary.Task.Error, "failing-validation") {
		t.Fatalf("expected failing command summary in task error, got %q", summary.Task.Error)
	}
	if !strings.Contains(summary.Task.Error, summary.Task.VerificationReportPath) {
		t.Fatalf("expected delivery report path in task error, got %q", summary.Task.Error)
	}
}

func TestApproveTaskReviewMarksTaskApproved(t *testing.T) {
	root := t.TempDir()
	git(t, root, "init")
	git(t, root, "init")
	result, err := Init(root, InitOptions{})
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	summary, err := RunTask(context.Background(), result.Project, result.Config, "Review-ready ERP task", fakeRunner{
		result: &RunResult{Output: "done"},
	})
	if err != nil {
		t.Fatalf("RunTask() error = %v", err)
	}
	task, err := ApproveTaskReview(result.Project, summary.Task.ID, "looks good")
	if err != nil {
		t.Fatalf("ApproveTaskReview() error = %v", err)
	}
	if task.ReviewStatus != ReviewApproved {
		t.Fatalf("ReviewStatus = %q, want %q", task.ReviewStatus, ReviewApproved)
	}
	if task.ReviewNotes != "looks good" {
		t.Fatalf("ReviewNotes = %q", task.ReviewNotes)
	}
}

func TestRejectTaskReviewReturnsTaskToRetryFlow(t *testing.T) {
	root := t.TempDir()
	git(t, root, "init")
	git(t, root, "init")
	result, err := Init(root, InitOptions{})
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	summary, err := RunTask(context.Background(), result.Project, result.Config, "Rejectable ERP task", fakeRunner{
		result: &RunResult{Output: "done"},
	})
	if err != nil {
		t.Fatalf("RunTask() error = %v", err)
	}
	task, err := RejectTaskReview(result.Project, summary.Task.ID, "needs a cleaner boundary")
	if err != nil {
		t.Fatalf("RejectTaskReview() error = %v", err)
	}
	if task.ReviewStatus != ReviewRejected {
		t.Fatalf("ReviewStatus = %q, want %q", task.ReviewStatus, ReviewRejected)
	}
	if task.Status != TaskFailed {
		t.Fatalf("Status = %q, want %q", task.Status, TaskFailed)
	}
	if !strings.Contains(task.Error, "review rejected") {
		t.Fatalf("unexpected task error: %q", task.Error)
	}
}

func TestPromoteTaskMarksApprovedTaskPromoted(t *testing.T) {
	root := t.TempDir()
	git(t, root, "init")
	git(t, root, "init")
	result, err := Init(root, InitOptions{})
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	summary, err := RunTask(context.Background(), result.Project, result.Config, "Promotable ERP task", fakeRunner{
		result: &RunResult{Output: "done"},
	})
	if err != nil {
		t.Fatalf("RunTask() error = %v", err)
	}
	if _, err := ApproveTaskReview(result.Project, summary.Task.ID, "approved"); err != nil {
		t.Fatalf("ApproveTaskReview() error = %v", err)
	}
	task, err := PromoteTask(context.Background(), result.Project, summary.Task.ID, "landed")
	if err != nil {
		t.Fatalf("PromoteTask() error = %v", err)
	}
	if task.PromotionStatus != PromotionApplied {
		t.Fatalf("PromotionStatus = %q, want %q", task.PromotionStatus, PromotionApplied)
	}
	if task.PromotionNotes != "landed" {
		t.Fatalf("PromotionNotes = %q", task.PromotionNotes)
	}
}

func TestPromoteTaskMergesApprovedWorktreeBranch(t *testing.T) {
	root := t.TempDir()
	git(t, root, "init")
	git(t, root, "init", "--quiet")
	git(t, root, "config", "user.name", "Harness Test")
	git(t, root, "config", "user.email", "harness@example.com")
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("seed\n"), 0644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	git(t, root, "add", "README.md")
	git(t, root, "commit", "--no-verify", "-m", "init")

	result, err := Init(root, InitOptions{})
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	git(t, root, "add", ".")
	git(t, root, "commit", "--no-verify", "-m", "add harness scaffold")
	task, err := EnqueueTask(result.Project, "Promote worktree ERP task", "cli")
	if err != nil {
		t.Fatalf("EnqueueTask() error = %v", err)
	}
	summary, err := ExecuteTask(context.Background(), result.Project, result.Config, task, writingRunner{
		relPath: "feature.txt",
		body:    "promoted change\n",
		result:  &RunResult{Output: "done"},
	})
	if err != nil {
		t.Fatalf("ExecuteTask() error = %v", err)
	}
	if _, err := ApproveTaskReview(result.Project, summary.Task.ID, "ship it"); err != nil {
		t.Fatalf("ApproveTaskReview() error = %v", err)
	}
	if _, err := PromoteTask(context.Background(), result.Project, summary.Task.ID, "merged"); err != nil {
		t.Fatalf("PromoteTask() error = %v", err)
	}
	data, err := os.ReadFile(filepath.Join(root, "feature.txt"))
	if err != nil {
		t.Fatalf("read promoted file: %v", err)
	}
	if strings.TrimSpace(string(data)) != "promoted change" {
		t.Fatalf("feature.txt = %q", string(data))
	}
}

func TestPromoteTaskSkipsWorkspaceTodoState(t *testing.T) {
	root := t.TempDir()
	git(t, root, "init")
	git(t, root, "config", "user.name", "Harness Test")
	git(t, root, "config", "user.email", "harness@example.com")
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("seed\n"), 0644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	git(t, root, "add", "README.md")
	git(t, root, "commit", "--no-verify", "-m", "init")

	result, err := Init(root, InitOptions{})
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	git(t, root, "add", ".")
	git(t, root, "commit", "--no-verify", "-m", "add harness scaffold")
	task, err := EnqueueTask(result.Project, "Promote worktree without todos state", "cli")
	if err != nil {
		t.Fatalf("EnqueueTask() error = %v", err)
	}
	summary, err := ExecuteTask(context.Background(), result.Project, result.Config, task, fakeRunner{
		result: &RunResult{Output: "done"},
	})
	if err != nil {
		t.Fatalf("ExecuteTask() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(summary.Task.WorkspacePath, "feature.txt"), []byte("promoted change\n"), 0644); err != nil {
		t.Fatalf("write feature.txt: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(summary.Task.WorkspacePath, ".ggcode"), 0755); err != nil {
		t.Fatalf("mkdir .ggcode: %v", err)
	}
	if err := os.WriteFile(filepath.Join(summary.Task.WorkspacePath, ".ggcode", "todos.json"), []byte(`[]`), 0644); err != nil {
		t.Fatalf("write todos.json: %v", err)
	}
	if _, err := ApproveTaskReview(result.Project, summary.Task.ID, "ship it"); err != nil {
		t.Fatalf("ApproveTaskReview() error = %v", err)
	}
	if _, err := PromoteTask(context.Background(), result.Project, summary.Task.ID, "merged"); err != nil {
		t.Fatalf("PromoteTask() error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "feature.txt")); err != nil {
		t.Fatalf("expected promoted feature.txt: %v", err)
	}
	cmd := exec.Command("git", "ls-files", "--error-unmatch", ".ggcode/todos.json")
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err == nil {
		t.Fatalf("expected .ggcode/todos.json to stay out of git, got %s", strings.TrimSpace(string(out)))
	}
}

func TestPromoteTaskFailsWhenRootHasOverlappingDirtyFile(t *testing.T) {
	root := t.TempDir()
	git(t, root, "init")
	git(t, root, "config", "user.name", "Harness Test")
	git(t, root, "config", "user.email", "harness@example.com")
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("seed\n"), 0644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	git(t, root, "add", "README.md")
	git(t, root, "commit", "--no-verify", "-m", "init")

	result, err := Init(root, InitOptions{})
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	git(t, root, "add", ".")
	git(t, root, "commit", "--no-verify", "-m", "add harness scaffold")
	task, err := EnqueueTask(result.Project, "Promote worktree with overlapping Dockerfile", "cli")
	if err != nil {
		t.Fatalf("EnqueueTask() error = %v", err)
	}
	summary, err := ExecuteTask(context.Background(), result.Project, result.Config, task, fakeRunner{
		result: &RunResult{Output: "done"},
	})
	if err != nil {
		t.Fatalf("ExecuteTask() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(summary.Task.WorkspacePath, "Dockerfile"), []byte("FROM alpine\n"), 0644); err != nil {
		t.Fatalf("write task Dockerfile: %v", err)
	}
	if _, err := ApproveTaskReview(result.Project, summary.Task.ID, "ship it"); err != nil {
		t.Fatalf("ApproveTaskReview() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "Dockerfile"), []byte("FROM busybox\n"), 0644); err != nil {
		t.Fatalf("write root Dockerfile: %v", err)
	}
	_, err = PromoteTask(context.Background(), result.Project, summary.Task.ID, "merged")
	if err == nil {
		t.Fatal("expected PromoteTask() to refuse overlapping dirty root file")
	}
	if !strings.Contains(err.Error(), "sync them into the task worktree before promotion") || !strings.Contains(err.Error(), "Dockerfile") {
		t.Fatalf("expected overlap guidance mentioning Dockerfile, got %v", err)
	}
}

func TestPromoteApprovedTasksPromotesAllReadyTasks(t *testing.T) {
	root := t.TempDir()
	git(t, root, "init")
	git(t, root, "init")
	result, err := Init(root, InitOptions{})
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	for _, goal := range []string{"Promote ERP inventory", "Promote ERP pricing"} {
		summary, err := RunTask(context.Background(), result.Project, result.Config, goal, fakeRunner{
			result: &RunResult{Output: "done"},
		})
		if err != nil {
			t.Fatalf("RunTask(%q) error = %v", goal, err)
		}
		if _, err := ApproveTaskReview(result.Project, summary.Task.ID, "approved"); err != nil {
			t.Fatalf("ApproveTaskReview() error = %v", err)
		}
	}
	tasks, err := PromoteApprovedTasks(context.Background(), result.Project, "batch")
	if err != nil {
		t.Fatalf("PromoteApprovedTasks() error = %v", err)
	}
	if len(tasks) != 2 {
		t.Fatalf("promoted = %d, want 2", len(tasks))
	}
}

func TestRunQueuedTasksRespectsDependencies(t *testing.T) {
	root := t.TempDir()
	git(t, root, "init")
	result, err := Init(root, InitOptions{})
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	first, err := EnqueueTask(result.Project, "Implement ERP receiving", "cli")
	if err != nil {
		t.Fatalf("EnqueueTask(first) error = %v", err)
	}
	_, err = EnqueueTask(result.Project, "Implement ERP invoicing", "cli", QueueOptions{DependsOn: []string{first.ID}})
	if err != nil {
		t.Fatalf("EnqueueTask(second) error = %v", err)
	}
	var seen []RunRequest
	summary, err := RunQueuedTasks(context.Background(), result.Project, result.Config, fakeRunner{
		result: &RunResult{Output: "ok"},
		seen:   &seen,
	}, QueueRunOptions{All: true})
	if err != nil {
		t.Fatalf("RunQueuedTasks() error = %v", err)
	}
	if len(summary.Executed) != 2 {
		t.Fatalf("executed = %d, want 2", len(summary.Executed))
	}
	if len(seen) != 2 {
		t.Fatalf("seen = %d, want 2", len(seen))
	}
}

func TestRunQueuedTasksRetriesFailedTaskWhenEnabled(t *testing.T) {
	root := t.TempDir()
	git(t, root, "init")
	result, err := Init(root, InitOptions{})
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	result.Config.Run.MaxAttempts = 3
	task, err := EnqueueTask(result.Project, "Implement ERP retryable billing workflow", "cli")
	if err != nil {
		t.Fatalf("EnqueueTask() error = %v", err)
	}
	var seen []RunRequest
	runner := &sequenceRunner{
		results: []*RunResult{
			{Output: "boom", ExitCode: 1},
			{Output: "ok", ExitCode: 0},
		},
		seen: &seen,
	}
	summary, err := RunQueuedTasks(context.Background(), result.Project, result.Config, runner, QueueRunOptions{
		All:         true,
		RetryFailed: true,
	})
	if err != nil {
		t.Fatalf("RunQueuedTasks() error = %v", err)
	}
	if len(summary.Executed) != 2 {
		t.Fatalf("executed = %d, want 2", len(summary.Executed))
	}
	loaded, err := LoadTask(result.Project, task.ID)
	if err != nil {
		t.Fatalf("LoadTask() error = %v", err)
	}
	if loaded.Status != TaskCompleted {
		t.Fatalf("loaded.Status = %q, want %q", loaded.Status, TaskCompleted)
	}
	if loaded.Attempt != 2 {
		t.Fatalf("loaded.Attempt = %d, want 2", loaded.Attempt)
	}
	if len(seen) != 2 {
		t.Fatalf("seen = %d, want 2", len(seen))
	}
}

func TestRerunTaskRetriesSingleFailedTask(t *testing.T) {
	root := t.TempDir()
	git(t, root, "init")
	result, err := Init(root, InitOptions{})
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	result.Config.Run.MaxAttempts = 3
	task, err := EnqueueTask(result.Project, "Rerun failed ERP task", "cli")
	if err != nil {
		t.Fatalf("EnqueueTask() error = %v", err)
	}
	_, err = ExecuteTask(context.Background(), result.Project, result.Config, task, fakeRunner{
		result: &RunResult{Output: "boom", ExitCode: 1},
	})
	if err != nil {
		t.Fatalf("ExecuteTask() error = %v", err)
	}
	var seen []RunRequest
	summary, err := RerunTask(context.Background(), result.Project, result.Config, task.ID, fakeRunner{
		result: &RunResult{Output: "ok", ExitCode: 0},
		seen:   &seen,
	})
	if err != nil {
		t.Fatalf("RerunTask() error = %v", err)
	}
	if summary.Task == nil || summary.Task.Status != TaskCompleted {
		t.Fatalf("summary.Task = %+v, want completed", summary.Task)
	}
	if summary.Task.Attempt != 2 {
		t.Fatalf("summary.Task.Attempt = %d, want 2", summary.Task.Attempt)
	}
	if len(seen) != 1 || !strings.Contains(seen[0].Prompt, task.Goal) {
		t.Fatalf("unexpected seen requests: %+v", seen)
	}
}

func TestRerunTaskRejectsNonFailedTask(t *testing.T) {
	root := t.TempDir()
	git(t, root, "init")
	result, err := Init(root, InitOptions{})
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	task, err := EnqueueTask(result.Project, "Queued ERP task", "cli")
	if err != nil {
		t.Fatalf("EnqueueTask() error = %v", err)
	}
	if _, err := RerunTask(context.Background(), result.Project, result.Config, task.ID, fakeRunner{
		result: &RunResult{Output: "ok"},
	}); err == nil || !strings.Contains(err.Error(), "only failed tasks can be rerun") {
		t.Fatalf("RerunTask() error = %v, want failed-task guard", err)
	}
}

func TestRunQueuedTasksLeavesFailedTaskWhenRetryDisabled(t *testing.T) {
	root := t.TempDir()
	git(t, root, "init")
	result, err := Init(root, InitOptions{})
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	task, err := EnqueueTask(result.Project, "Implement ERP non-retry workflow", "cli")
	if err != nil {
		t.Fatalf("EnqueueTask() error = %v", err)
	}
	_, err = RunQueuedTasks(context.Background(), result.Project, result.Config, fakeRunner{
		result: &RunResult{Output: "boom", ExitCode: 1},
	}, QueueRunOptions{All: true})
	if err != nil {
		t.Fatalf("RunQueuedTasks() error = %v", err)
	}
	loaded, err := LoadTask(result.Project, task.ID)
	if err != nil {
		t.Fatalf("LoadTask() error = %v", err)
	}
	if loaded.Status != TaskFailed {
		t.Fatalf("loaded.Status = %q, want %q", loaded.Status, TaskFailed)
	}
	if loaded.Attempt != 1 {
		t.Fatalf("loaded.Attempt = %d, want 1", loaded.Attempt)
	}
}

func TestRunQueuedTasksResumesInterruptedTaskWhenEnabled(t *testing.T) {
	root := t.TempDir()
	git(t, root, "init")
	result, err := Init(root, InitOptions{})
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	task, err := EnqueueTask(result.Project, "Resume interrupted ERP fulfillment", "cli")
	if err != nil {
		t.Fatalf("EnqueueTask() error = %v", err)
	}
	task.Status = TaskRunning
	task.Attempt = 1
	if err := SaveTask(result.Project, task); err != nil {
		t.Fatalf("SaveTask() error = %v", err)
	}
	summary, err := RunQueuedTasks(context.Background(), result.Project, result.Config, fakeRunner{
		result: &RunResult{Output: "ok"},
	}, QueueRunOptions{ResumeInterrupted: true})
	if err != nil {
		t.Fatalf("RunQueuedTasks() error = %v", err)
	}
	if len(summary.Executed) != 1 {
		t.Fatalf("executed = %d, want 1", len(summary.Executed))
	}
	loaded, err := LoadTask(result.Project, task.ID)
	if err != nil {
		t.Fatalf("LoadTask() error = %v", err)
	}
	if loaded.Status != TaskCompleted {
		t.Fatalf("loaded.Status = %q, want %q", loaded.Status, TaskCompleted)
	}
	if loaded.Attempt != 2 {
		t.Fatalf("loaded.Attempt = %d, want 2", loaded.Attempt)
	}
}

func TestExecuteTaskUsesGitWorktreeWhenAvailable(t *testing.T) {
	root := t.TempDir()
	git(t, root, "init")
	git(t, root, "init", "--quiet")
	git(t, root, "config", "user.name", "Harness Test")
	git(t, root, "config", "user.email", "harness@example.com")
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("seed"), 0644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	git(t, root, "add", "README.md")
	git(t, root, "commit", "--no-verify", "-m", "init")

	result, err := Init(root, InitOptions{})
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	commitHarnessScaffold(t, root, result)
	task, err := EnqueueTask(result.Project, "Implement ERP inventory aggregate", "cli")
	if err != nil {
		t.Fatalf("EnqueueTask() error = %v", err)
	}
	var seen []RunRequest
	summary, err := ExecuteTask(context.Background(), result.Project, result.Config, task, fakeRunner{
		result: &RunResult{Output: "ok"},
		seen:   &seen,
	})
	if err != nil {
		t.Fatalf("ExecuteTask() error = %v", err)
	}
	if summary.Task.WorkspaceMode != "git-worktree" {
		t.Fatalf("WorkspaceMode = %q, want git-worktree", summary.Task.WorkspaceMode)
	}
	if !strings.HasPrefix(summary.Task.WorkspacePath, result.Project.WorktreesDir) {
		t.Fatalf("WorkspacePath = %q, want under %q", summary.Task.WorkspacePath, result.Project.WorktreesDir)
	}
	if len(seen) != 1 || seen[0].WorkingDir != summary.Task.WorkspacePath {
		t.Fatalf("runner saw %+v, want working dir %q", seen, summary.Task.WorkspacePath)
	}
	if got := seen[0].AllowedDirs; len(got) != 1 || got[0] != summary.Task.WorkspacePath {
		t.Fatalf("runner AllowedDirs = %#v, want [%q]", got, summary.Task.WorkspacePath)
	}
	if want := filepath.Join(result.Project.RootDir, ".ggcode"); len(seen[0].ReadOnlyAllowedDirs) != 1 || seen[0].ReadOnlyAllowedDirs[0] != want {
		t.Fatalf("runner ReadOnlyAllowedDirs = %#v, want [%q]", seen[0].ReadOnlyAllowedDirs, want)
	}
}

func TestExecuteTaskCheckpointsDirtyProjectFilesBeforeWorktree(t *testing.T) {
	root := t.TempDir()
	git(t, root, "init")
	git(t, root, "config", "user.name", "Harness Test")
	git(t, root, "config", "user.email", "harness@example.com")
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("seed\n"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	git(t, root, "add", "README.md")
	git(t, root, "commit", "--no-verify", "-m", "init")

	result, err := Init(root, InitOptions{})
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	commitHarnessScaffold(t, root, result)
	task, err := EnqueueTask(result.Project, "Implement ERP pricing logic", "cli")
	if err != nil {
		t.Fatalf("EnqueueTask() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "Dockerfile"), []byte("FROM alpine\n"), 0o644); err != nil {
		t.Fatalf("write Dockerfile: %v", err)
	}
	var seen []RunRequest
	summary, err := ExecuteTask(context.Background(), result.Project, result.Config, task, fakeRunner{
		result: &RunResult{Output: "ok"},
		seen:   &seen,
	})
	if err != nil {
		t.Fatalf("ExecuteTask() error = %v", err)
	}
	if summary == nil || summary.Task == nil || summary.Task.WorkspaceMode != "git-worktree" {
		t.Fatalf("expected worktree-backed summary, got %#v", summary)
	}
	if len(seen) != 1 {
		t.Fatalf("runner should start after checkpoint commit, got %+v", seen)
	}
	cmd := exec.Command("git", "log", "-1", "--pretty=%s")
	cmd.Dir = root
	out, logErr := cmd.CombinedOutput()
	if logErr != nil {
		t.Fatalf("git log: %v\n%s", logErr, out)
	}
	message := strings.TrimSpace(string(out))
	if !strings.Contains(message, "checkpoint workspace before harness run") {
		t.Fatalf("expected checkpoint commit message, got %q", message)
	}
	git(t, root, "ls-files", "--error-unmatch", "Dockerfile")
}

func TestExecuteTaskCancelsWhenDirtyWorkspaceCheckpointDeclined(t *testing.T) {
	root := t.TempDir()
	git(t, root, "init")
	git(t, root, "config", "user.name", "Harness Test")
	git(t, root, "config", "user.email", "harness@example.com")
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("seed\n"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	git(t, root, "add", "README.md")
	git(t, root, "commit", "--no-verify", "-m", "init")

	result, err := Init(root, InitOptions{})
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	commitHarnessScaffold(t, root, result)
	task, err := EnqueueTask(result.Project, "Implement ERP checkout logic", "cli")
	if err != nil {
		t.Fatalf("EnqueueTask() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "Dockerfile"), []byte("FROM alpine\n"), 0o644); err != nil {
		t.Fatalf("write Dockerfile: %v", err)
	}
	var seen []RunRequest
	_, err = ExecuteTask(context.Background(), result.Project, result.Config, task, fakeRunner{
		result: &RunResult{Output: "ok"},
		seen:   &seen,
	}, ExecuteTaskOptions{
		ConfirmDirtyWorkspace: func(checkpoint DirtyWorkspaceCheckpoint) (bool, error) {
			if !strings.Contains(checkpoint.Summary, "Dockerfile") {
				t.Fatalf("expected checkpoint summary to mention Dockerfile, got %+v", checkpoint)
			}
			return false, nil
		},
	})
	if err == nil {
		t.Fatal("expected ExecuteTask() to stop when checkpoint is declined")
	}
	if !strings.Contains(err.Error(), "checkpoint was not approved") {
		t.Fatalf("expected decline error, got %v", err)
	}
	if len(seen) != 0 {
		t.Fatalf("runner should not start when checkpoint is declined, got %+v", seen)
	}
}

func TestPrepareWorkspaceIgnoresHarnessRuntimeAndSharedRuntimeDirs(t *testing.T) {
	root := t.TempDir()
	git(t, root, "init")
	git(t, root, "config", "user.name", "Harness Test")
	git(t, root, "config", "user.email", "harness@example.com")
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("seed\n"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	git(t, root, "add", "README.md")
	git(t, root, "commit", "--no-verify", "-m", "init")
	if err := os.MkdirAll(filepath.Join(root, "node_modules"), 0o755); err != nil {
		t.Fatalf("mkdir node_modules: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "node_modules", "tool"), []byte("shim"), 0o644); err != nil {
		t.Fatalf("write shared runtime file: %v", err)
	}

	result, err := Init(root, InitOptions{})
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	commitHarnessScaffold(t, root, result)
	if err := os.WriteFile(filepath.Join(root, ".ggcode", "todos.json"), []byte(`[]`), 0o644); err != nil {
		t.Fatalf("write todos.json: %v", err)
	}
	task, err := EnqueueTask(result.Project, "Prepare clean worktree base", "cli")
	if err != nil {
		t.Fatalf("EnqueueTask() error = %v", err)
	}
	workspace, err := PrepareWorkspace(context.Background(), result.Project, result.Config, task)
	if err != nil {
		t.Fatalf("PrepareWorkspace() error = %v", err)
	}
	if workspace.Mode != "git-worktree" {
		t.Fatalf("workspace.Mode = %q, want git-worktree", workspace.Mode)
	}
}

func TestPrepareWorkspaceLinksNodeModulesIntoGitWorktree(t *testing.T) {
	root := t.TempDir()
	git(t, root, "init")
	git(t, root, "config", "user.name", "Harness Test")
	git(t, root, "config", "user.email", "harness@example.com")
	if err := os.MkdirAll(filepath.Join(root, "packages", "web"), 0o755); err != nil {
		t.Fatalf("mkdir web package: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "packages", "web", "package.json"), []byte(`{"name":"web"}`), 0o644); err != nil {
		t.Fatalf("write package.json: %v", err)
	}
	git(t, root, "add", "packages/web/package.json")
	git(t, root, "commit", "--no-verify", "-m", "init")
	if err := os.MkdirAll(filepath.Join(root, "packages", "web", "node_modules", ".bin"), 0o755); err != nil {
		t.Fatalf("mkdir package node_modules: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "packages", "web", "node_modules", ".bin", "playwright"), []byte("shim"), 0o644); err != nil {
		t.Fatalf("write playwright shim: %v", err)
	}

	result, err := Init(root, InitOptions{})
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	commitHarnessScaffold(t, root, result)
	task, err := EnqueueTask(result.Project, "Validate worktree deps", "cli")
	if err != nil {
		t.Fatalf("EnqueueTask() error = %v", err)
	}

	workspace, err := PrepareWorkspace(context.Background(), result.Project, result.Config, task)
	if err != nil {
		t.Fatalf("PrepareWorkspace() error = %v", err)
	}
	defer func() {
		task.WorkspacePath = workspace.Path
		_ = cleanupWorkspace(result.Project, task)
	}()
	if workspace.Mode != "git-worktree" {
		t.Fatalf("workspace.Mode = %q, want git-worktree", workspace.Mode)
	}

	target := filepath.Join(workspace.Path, "packages", "web", "node_modules")
	info, err := os.Lstat(target)
	if err != nil {
		t.Fatalf("Lstat(node_modules) error = %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("expected %s to be a symlink, mode=%v", target, info.Mode())
	}
	resolved, err := filepath.EvalSymlinks(target)
	if err != nil {
		t.Fatalf("EvalSymlinks(node_modules) error = %v", err)
	}
	want := filepath.Join(root, "packages", "web", "node_modules")
	wantResolved, err := filepath.EvalSymlinks(want)
	if err != nil {
		t.Fatalf("EvalSymlinks(want) error = %v", err)
	}
	if resolved != wantResolved {
		t.Fatalf("resolved node_modules = %q, want %q", resolved, wantResolved)
	}
}

func TestPrepareWorkspaceLinksPythonVirtualEnvIntoGitWorktree(t *testing.T) {
	root := t.TempDir()
	git(t, root, "init")
	git(t, root, "config", "user.name", "Harness Test")
	git(t, root, "config", "user.email", "harness@example.com")
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("seed"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	git(t, root, "add", "README.md")
	git(t, root, "commit", "--no-verify", "-m", "init")
	if err := os.MkdirAll(filepath.Join(root, ".venv", "bin"), 0o755); err != nil {
		t.Fatalf("mkdir .venv/bin: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, ".venv", "pyvenv.cfg"), []byte("home = /usr/bin\n"), 0o644); err != nil {
		t.Fatalf("write pyvenv.cfg: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, ".venv", "bin", "python"), []byte("#!/usr/bin/env python3\n"), 0o755); err != nil {
		t.Fatalf("write python shim: %v", err)
	}

	result, err := Init(root, InitOptions{})
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	commitHarnessScaffold(t, root, result)
	task, err := EnqueueTask(result.Project, "Validate python worktree deps", "cli")
	if err != nil {
		t.Fatalf("EnqueueTask() error = %v", err)
	}

	workspace, err := PrepareWorkspace(context.Background(), result.Project, result.Config, task)
	if err != nil {
		t.Fatalf("PrepareWorkspace() error = %v", err)
	}
	defer func() {
		task.WorkspacePath = workspace.Path
		_ = cleanupWorkspace(result.Project, task)
	}()

	target := filepath.Join(workspace.Path, ".venv")
	info, err := os.Lstat(target)
	if err != nil {
		t.Fatalf("Lstat(.venv) error = %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("expected %s to be a symlink, mode=%v", target, info.Mode())
	}
	resolved, err := filepath.EvalSymlinks(target)
	if err != nil {
		t.Fatalf("EvalSymlinks(.venv) error = %v", err)
	}
	wantResolved, err := filepath.EvalSymlinks(filepath.Join(root, ".venv"))
	if err != nil {
		t.Fatalf("EvalSymlinks(want .venv) error = %v", err)
	}
	if resolved != wantResolved {
		t.Fatalf("resolved .venv = %q, want %q", resolved, wantResolved)
	}
}

func TestExecuteTaskPassesHarnessReadOnlyDirsToWorker(t *testing.T) {
	root := t.TempDir()
	git(t, root, "init")
	git(t, root, "config", "user.name", "Harness Test")
	git(t, root, "config", "user.email", "harness@example.com")
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("seed"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	git(t, root, "add", "README.md")
	git(t, root, "commit", "--no-verify", "-m", "init")

	result, err := Init(root, InitOptions{})
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	commitHarnessScaffold(t, root, result)
	task, err := EnqueueTask(result.Project, "Read harness config from worker", "cli")
	if err != nil {
		t.Fatalf("EnqueueTask() error = %v", err)
	}
	var seen []RunRequest
	summary, err := ExecuteTask(context.Background(), result.Project, result.Config, task, fakeRunner{
		result: &RunResult{Output: "ok"},
		seen:   &seen,
	})
	if err != nil {
		t.Fatalf("ExecuteTask() error = %v", err)
	}
	if summary == nil || summary.Task == nil {
		t.Fatalf("expected task summary, got %#v", summary)
	}
	if len(seen) != 1 {
		t.Fatalf("expected one worker request, got %d", len(seen))
	}
	want := filepath.Join(root, ".ggcode")
	if got := seen[0].ReadOnlyAllowedDirs; len(got) != 1 || got[0] != want {
		t.Fatalf("ReadOnlyAllowedDirs = %#v, want [%q]", got, want)
	}
}

func TestDiscoverSharedRuntimeDirsIgnoresPlainEnvDir(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "env"), 0o755); err != nil {
		t.Fatalf("mkdir env: %v", err)
	}
	dirs, err := discoverSharedRuntimeDirs(root)
	if err != nil {
		t.Fatalf("discoverSharedRuntimeDirs() error = %v", err)
	}
	if len(dirs) != 0 {
		t.Fatalf("expected plain env dir to be ignored, got %#v", dirs)
	}
}

func TestExecuteTaskPersistsWorkerStateDuringRun(t *testing.T) {
	root := t.TempDir()
	git(t, root, "init")
	result, err := Init(root, InitOptions{})
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	result.Config.Run.ExecutionMode = "subagent"
	task, err := EnqueueTask(result.Project, "Implement ERP worker-backed backlog task", "cli")
	if err != nil {
		t.Fatalf("EnqueueTask() error = %v", err)
	}
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	done := make(chan *RunSummary, 1)
	errs := make(chan error, 1)
	go func() {
		summary, err := ExecuteTask(context.Background(), result.Project, result.Config, task, blockingRunner{
			started: started,
			release: release,
			result:  &RunResult{Output: "ok"},
		})
		if err != nil {
			errs <- err
			return
		}
		done <- summary
	}()

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("runner did not start")
	}

	var running *Task
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		loaded, err := LoadTask(result.Project, task.ID)
		if err != nil {
			t.Fatalf("LoadTask() error = %v", err)
		}
		if loaded.WorkerID != "" && loaded.WorkerStatus == "running" {
			running = loaded
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if running == nil {
		t.Fatal("expected running task with persisted worker state")
	}
	close(release)

	select {
	case err := <-errs:
		t.Fatalf("ExecuteTask() error = %v", err)
	case summary := <-done:
		if summary.Task.WorkerID == "" {
			t.Fatal("expected worker id on completed task")
		}
		if summary.Task.WorkerStatus != "completed" {
			t.Fatalf("WorkerStatus = %q, want completed", summary.Task.WorkerStatus)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for ExecuteTask")
	}
}

func TestRunGCArchivesOldTasksAndDeletesOldLogs(t *testing.T) {
	root := t.TempDir()
	git(t, root, "init")
	result, err := Init(root, InitOptions{})
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	result.Config.GC.ArchiveAfter = "1h"
	result.Config.GC.DeleteLogsAfter = "1h"
	task, err := NewTask("Old ERP cleanup", "cli")
	if err != nil {
		t.Fatalf("NewTask() error = %v", err)
	}
	old := time.Now().UTC().Add(-10 * 24 * time.Hour)
	task.Status = TaskCompleted
	task.UpdatedAt = old
	task.CreatedAt = old
	task.LogPath = filepath.Join(result.Project.LogsDir, task.ID+".log")
	if err := os.MkdirAll(result.Project.LogsDir, 0755); err != nil {
		t.Fatalf("mkdir logs: %v", err)
	}
	if err := os.WriteFile(task.LogPath, []byte("stale"), 0644); err != nil {
		t.Fatalf("write log: %v", err)
	}
	if err := os.Chtimes(task.LogPath, old, old); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
	data, err := json.MarshalIndent(task, "", "  ")
	if err != nil {
		t.Fatalf("MarshalIndent() error = %v", err)
	}
	if err := os.WriteFile(taskPath(result.Project, task.ID), data, 0644); err != nil {
		t.Fatalf("write task: %v", err)
	}
	report, err := RunGC(result.Project, result.Config, time.Now().UTC())
	if err != nil {
		t.Fatalf("RunGC() error = %v", err)
	}
	if report.ArchivedTasks != 1 {
		t.Fatalf("ArchivedTasks = %d, want 1", report.ArchivedTasks)
	}
	if report.DeletedLogs != 1 {
		t.Fatalf("DeletedLogs = %d, want 1", report.DeletedLogs)
	}
	if _, err := os.Stat(filepath.Join(result.Project.ArchiveDir, task.ID+".json")); err != nil {
		t.Fatalf("expected archived task: %v", err)
	}
}

func TestRunGCAbandonsStaleBlockedTasks(t *testing.T) {
	root := t.TempDir()
	git(t, root, "init")
	result, err := Init(root, InitOptions{})
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	result.Config.GC.AbandonAfter = "1h"
	task, err := NewTask("Blocked ERP workflow", "cli")
	if err != nil {
		t.Fatalf("NewTask() error = %v", err)
	}
	old := time.Now().UTC().Add(-3 * time.Hour)
	task.Status = TaskBlocked
	task.DependsOn = []string{"missing-upstream"}
	task.UpdatedAt = old
	task.CreatedAt = old
	if err := writeTaskFixture(result.Project, task); err != nil {
		t.Fatalf("writeTaskFixture() error = %v", err)
	}
	report, err := RunGC(result.Project, result.Config, time.Now().UTC())
	if err != nil {
		t.Fatalf("RunGC() error = %v", err)
	}
	if report.AbandonedTasks != 1 {
		t.Fatalf("AbandonedTasks = %d, want 1", report.AbandonedTasks)
	}
	loaded, err := LoadTask(result.Project, task.ID)
	if err != nil {
		t.Fatalf("LoadTask() error = %v", err)
	}
	if loaded.Status != TaskAbandoned {
		t.Fatalf("loaded.Status = %q, want %q", loaded.Status, TaskAbandoned)
	}
}

func TestRunGCRemovesOrphanedWorktrees(t *testing.T) {
	root := t.TempDir()
	git(t, root, "init")
	result, err := Init(root, InitOptions{})
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	orphan := filepath.Join(result.Project.WorktreesDir, "orphaned-task")
	if err := os.MkdirAll(orphan, 0755); err != nil {
		t.Fatalf("mkdir orphan worktree: %v", err)
	}
	report, err := RunGC(result.Project, result.Config, time.Now().UTC())
	if err != nil {
		t.Fatalf("RunGC() error = %v", err)
	}
	if report.RemovedWorktrees != 1 {
		t.Fatalf("RemovedWorktrees = %d, want 1", report.RemovedWorktrees)
	}
	if _, err := os.Stat(orphan); !os.IsNotExist(err) {
		t.Fatalf("expected orphaned worktree to be removed, stat err=%v", err)
	}
}

func TestDoctorReportsDriftSignals(t *testing.T) {
	root := t.TempDir()
	git(t, root, "init")
	result, err := Init(root, InitOptions{})
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	result.Config.Checks.Commands = nil
	result.Config.GC.AbandonAfter = "1h"
	blocked, err := NewTask("Blocked ERP drift", "cli")
	if err != nil {
		t.Fatalf("NewTask() error = %v", err)
	}
	old := time.Now().UTC().Add(-3 * time.Hour)
	blocked.Status = TaskBlocked
	blocked.DependsOn = []string{"missing-upstream"}
	blocked.UpdatedAt = old
	blocked.CreatedAt = old
	if err := writeTaskFixture(result.Project, blocked); err != nil {
		t.Fatalf("writeTaskFixture(blocked) error = %v", err)
	}
	running, err := NewTask("Running ERP drift", "cli")
	if err != nil {
		t.Fatalf("NewTask() error = %v", err)
	}
	running.Status = TaskRunning
	if err := SaveTask(result.Project, running); err != nil {
		t.Fatalf("SaveTask(running) error = %v", err)
	}
	orphan := filepath.Join(result.Project.WorktreesDir, "orphaned-task")
	if err := os.MkdirAll(orphan, 0755); err != nil {
		t.Fatalf("mkdir orphan worktree: %v", err)
	}
	report, err := Doctor(result.Project, result.Config)
	if err != nil {
		t.Fatalf("Doctor() error = %v", err)
	}
	if report.StaleBlocked != 1 {
		t.Fatalf("StaleBlocked = %d, want 1", report.StaleBlocked)
	}
	if report.OrphanedWorktrees != 1 {
		t.Fatalf("OrphanedWorktrees = %d, want 1", report.OrphanedWorktrees)
	}
	if report.WorkerDrift != 1 {
		t.Fatalf("WorkerDrift = %d, want 1", report.WorkerDrift)
	}
	rendered := FormatDoctorReport(report)
	if !strings.Contains(rendered, "Drift issues:") {
		t.Fatalf("expected drift issues in doctor report, got %q", rendered)
	}
}

func TestDoctorCountsReviewReadyTasks(t *testing.T) {
	root := t.TempDir()
	git(t, root, "init")
	git(t, root, "init")
	result, err := Init(root, InitOptions{})
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	result.Config.Checks.Commands = nil
	if _, err := RunTask(context.Background(), result.Project, result.Config, "Reviewable ERP change", fakeRunner{
		result: &RunResult{Output: "done"},
	}); err != nil {
		t.Fatalf("RunTask() error = %v", err)
	}
	report, err := Doctor(result.Project, result.Config)
	if err != nil {
		t.Fatalf("Doctor() error = %v", err)
	}
	if report.ReviewReady != 1 {
		t.Fatalf("ReviewReady = %d, want 1", report.ReviewReady)
	}
}

func TestDoctorCountsPromotionReadyTasks(t *testing.T) {
	root := t.TempDir()
	git(t, root, "init")
	git(t, root, "init")
	result, err := Init(root, InitOptions{})
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	result.Config.Checks.Commands = nil
	summary, err := RunTask(context.Background(), result.Project, result.Config, "Promotion-ready ERP change", fakeRunner{
		result: &RunResult{Output: "done"},
	})
	if err != nil {
		t.Fatalf("RunTask() error = %v", err)
	}
	if _, err := ApproveTaskReview(result.Project, summary.Task.ID, "approved"); err != nil {
		t.Fatalf("ApproveTaskReview() error = %v", err)
	}
	report, err := Doctor(result.Project, result.Config)
	if err != nil {
		t.Fatalf("Doctor() error = %v", err)
	}
	if report.PromotionReady != 1 {
		t.Fatalf("PromotionReady = %d, want 1", report.PromotionReady)
	}
}

func TestDoctorCountsReleaseReadyTasks(t *testing.T) {
	root := t.TempDir()
	git(t, root, "init")
	result, err := Init(root, InitOptions{})
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	task, err := NewTask("Release-ready ERP change", "cli")
	if err != nil {
		t.Fatalf("NewTask() error = %v", err)
	}
	task.Status = TaskCompleted
	task.VerificationStatus = VerificationPassed
	task.ReviewStatus = ReviewApproved
	task.PromotionStatus = PromotionApplied
	if err := writeTaskFixture(result.Project, task); err != nil {
		t.Fatalf("writeTaskFixture() error = %v", err)
	}
	report, err := Doctor(result.Project, result.Config)
	if err != nil {
		t.Fatalf("Doctor() error = %v", err)
	}
	if report.ReleaseReady != 1 {
		t.Fatalf("ReleaseReady = %d, want 1", report.ReleaseReady)
	}
}

func TestDoctorCountsRolloutStates(t *testing.T) {
	root := t.TempDir()
	git(t, root, "init")
	for _, rel := range []string{
		filepath.Join("internal", "inventory"),
		filepath.Join("internal", "pricing"),
	} {
		if err := os.MkdirAll(filepath.Join(root, rel), 0755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
	}
	result, err := Init(root, InitOptions{})
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	for i := range result.Config.Contexts {
		switch result.Config.Contexts[i].Path {
		case filepath.Join("internal", "inventory"):
			result.Config.Contexts[i].Owner = "inventory-team"
		case filepath.Join("internal", "pricing"):
			result.Config.Contexts[i].Owner = "pricing-team"
		}
	}
	for _, item := range []string{filepath.Join("internal", "inventory"), filepath.Join("internal", "pricing")} {
		task, err := NewTask("Ship "+item, "cli")
		if err != nil {
			t.Fatalf("NewTask() error = %v", err)
		}
		task.ContextPath = item
		task.ContextName = strings.ReplaceAll(item, string(filepath.Separator), "-")
		task.Status = TaskCompleted
		task.VerificationStatus = VerificationPassed
		task.ReviewStatus = ReviewApproved
		task.PromotionStatus = PromotionApplied
		if err := writeTaskFixture(result.Project, task); err != nil {
			t.Fatalf("writeTaskFixture() error = %v", err)
		}
	}
	waves, err := BuildReleaseWavePlan(result.Project, result.Config, ReleasePlanOptions{}, ReleaseGroupByOwner)
	if err != nil {
		t.Fatalf("BuildReleaseWavePlan() error = %v", err)
	}
	if _, err := ApplyReleaseWavePlan(result.Project, waves, "", "rollout-doctor"); err != nil {
		t.Fatalf("ApplyReleaseWavePlan() error = %v", err)
	}
	if _, err := RejectReleaseWaveGate(result.Project, "rollout-doctor", 2, "waiting for approval"); err != nil {
		t.Fatalf("RejectReleaseWaveGate() error = %v", err)
	}
	report, err := Doctor(result.Project, result.Config)
	if err != nil {
		t.Fatalf("Doctor() error = %v", err)
	}
	if report.Rollouts != 1 || report.ActiveRollouts != 1 || report.PlannedRollouts != 1 || report.ApprovedGates != 1 || report.RejectedGates != 1 {
		t.Fatalf("unexpected rollout counts: %+v", report)
	}
	if !strings.Contains(FormatDoctorReport(report), "gates: pending=0 approved=1 rejected=1") {
		t.Fatalf("expected gate counts in doctor report, got %q", FormatDoctorReport(report))
	}
}

func TestBuildContextReportSummarizesContextState(t *testing.T) {
	root := t.TempDir()
	git(t, root, "init")
	if err := os.MkdirAll(filepath.Join(root, "internal", "inventory"), 0755); err != nil {
		t.Fatalf("mkdir inventory: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "internal", "pricing"), 0755); err != nil {
		t.Fatalf("mkdir pricing: %v", err)
	}
	result, err := Init(root, InitOptions{})
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	for i := range result.Config.Contexts {
		if result.Config.Contexts[i].Path == filepath.Join("internal", "inventory") {
			result.Config.Contexts[i].Commands = []CommandCheck{{Name: "inventory-check", Run: "echo ok"}}
			result.Config.Contexts[i].Owner = "inventory-team"
		}
	}
	inventoryTask, err := NewTask("Inventory slice", "cli")
	if err != nil {
		t.Fatalf("NewTask() error = %v", err)
	}
	inventoryTask.ContextName = "internal-inventory"
	inventoryTask.ContextPath = filepath.Join("internal", "inventory")
	inventoryTask.Status = TaskCompleted
	inventoryTask.ReviewStatus = ReviewApproved
	old := time.Now().UTC().Add(-time.Minute)
	inventoryTask.UpdatedAt = old
	inventoryTask.CreatedAt = old
	if err := writeTaskFixture(result.Project, inventoryTask); err != nil {
		t.Fatalf("writeTaskFixture(inventory) error = %v", err)
	}
	pricingTask, err := NewTask("Pricing slice", "cli")
	if err != nil {
		t.Fatalf("NewTask() error = %v", err)
	}
	pricingTask.ContextName = "internal-pricing"
	pricingTask.ContextPath = filepath.Join("internal", "pricing")
	pricingTask.Status = TaskFailed
	pricingTask.VerificationStatus = VerificationFailed
	if err := writeTaskFixture(result.Project, pricingTask); err != nil {
		t.Fatalf("writeTaskFixture(pricing) error = %v", err)
	}
	unscopedTask, err := NewTask("Root maintenance", "cli")
	if err != nil {
		t.Fatalf("NewTask() error = %v", err)
	}
	unscopedTask.Status = TaskQueued
	if err := writeTaskFixture(result.Project, unscopedTask); err != nil {
		t.Fatalf("writeTaskFixture(unscoped) error = %v", err)
	}
	report, err := BuildContextReport(result.Project, result.Config)
	if err != nil {
		t.Fatalf("BuildContextReport() error = %v", err)
	}
	if len(report.Summaries) < 3 {
		t.Fatalf("expected at least 3 summaries, got %+v", report.Summaries)
	}
	var inventorySummary, pricingSummary, unscopedSummary *ContextSummary
	for i := range report.Summaries {
		summary := &report.Summaries[i]
		switch summary.Path {
		case filepath.Join("internal", "inventory"):
			inventorySummary = summary
		case filepath.Join("internal", "pricing"):
			pricingSummary = summary
		}
		if summary.Unscoped {
			unscopedSummary = summary
		}
	}
	if inventorySummary == nil || inventorySummary.CommandCount != 1 || inventorySummary.PromotionReady != 1 || inventorySummary.Owner != "inventory-team" {
		t.Fatalf("unexpected inventory summary: %+v", inventorySummary)
	}
	if pricingSummary == nil || pricingSummary.FailedTasks != 1 || pricingSummary.VerificationFailed != 1 {
		t.Fatalf("unexpected pricing summary: %+v", pricingSummary)
	}
	if unscopedSummary == nil || unscopedSummary.QueuedTasks != 1 {
		t.Fatalf("unexpected unscoped summary: %+v", unscopedSummary)
	}
	rendered := FormatContextReport(report)
	if !strings.Contains(rendered, "Harness contexts:") || !strings.Contains(rendered, "internal/inventory") {
		t.Fatalf("unexpected rendered context report: %q", rendered)
	}
	if !strings.Contains(rendered, "owner: inventory-team") {
		t.Fatalf("expected owner in rendered context report, got %q", rendered)
	}
}

func TestBuildContextReportIncludesRolloutStates(t *testing.T) {
	root := t.TempDir()
	git(t, root, "init")
	for _, rel := range []string{
		filepath.Join("internal", "inventory"),
		filepath.Join("internal", "pricing"),
	} {
		if err := os.MkdirAll(filepath.Join(root, rel), 0755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
	}
	result, err := Init(root, InitOptions{})
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	for i := range result.Config.Contexts {
		switch result.Config.Contexts[i].Path {
		case filepath.Join("internal", "inventory"):
			result.Config.Contexts[i].Owner = "inventory-team"
		case filepath.Join("internal", "pricing"):
			result.Config.Contexts[i].Owner = "pricing-team"
		}
	}
	for _, item := range []string{filepath.Join("internal", "inventory"), filepath.Join("internal", "pricing")} {
		task, err := NewTask("Ship "+item, "cli")
		if err != nil {
			t.Fatalf("NewTask() error = %v", err)
		}
		task.ContextPath = item
		task.ContextName = strings.ReplaceAll(item, string(filepath.Separator), "-")
		task.Status = TaskCompleted
		task.VerificationStatus = VerificationPassed
		task.ReviewStatus = ReviewApproved
		task.PromotionStatus = PromotionApplied
		if err := writeTaskFixture(result.Project, task); err != nil {
			t.Fatalf("writeTaskFixture() error = %v", err)
		}
	}
	waves, err := BuildReleaseWavePlan(result.Project, result.Config, ReleasePlanOptions{}, ReleaseGroupByContext)
	if err != nil {
		t.Fatalf("BuildReleaseWavePlan() error = %v", err)
	}
	if _, err := ApplyReleaseWavePlan(result.Project, waves, "", "rollout-contexts"); err != nil {
		t.Fatalf("ApplyReleaseWavePlan() error = %v", err)
	}
	if _, err := RejectReleaseWaveGate(result.Project, "rollout-contexts", 2, "waiting for approval"); err != nil {
		t.Fatalf("RejectReleaseWaveGate() error = %v", err)
	}
	report, err := BuildContextReport(result.Project, result.Config)
	if err != nil {
		t.Fatalf("BuildContextReport() error = %v", err)
	}
	var inventorySummary *ContextSummary
	for i := range report.Summaries {
		if report.Summaries[i].Path == filepath.Join("internal", "inventory") {
			inventorySummary = &report.Summaries[i]
			break
		}
	}
	if inventorySummary == nil || inventorySummary.ActiveRollouts != 1 || inventorySummary.ApprovedGates != 1 {
		t.Fatalf("unexpected inventory rollout summary: %+v", inventorySummary)
	}
	var pricingSummary *ContextSummary
	for i := range report.Summaries {
		if report.Summaries[i].Path == filepath.Join("internal", "pricing") {
			pricingSummary = &report.Summaries[i]
			break
		}
	}
	if pricingSummary == nil || pricingSummary.RejectedGates != 1 {
		t.Fatalf("unexpected pricing rollout summary: %+v", pricingSummary)
	}
	if !strings.Contains(FormatContextReport(report), "gates: pending=0 approved=1 rejected=0") {
		t.Fatalf("expected gate counts in context report, got %q", FormatContextReport(report))
	}
}

func TestBuildOwnerInboxGroupsActionableTasksByOwner(t *testing.T) {
	root := t.TempDir()
	git(t, root, "init")
	if err := os.MkdirAll(filepath.Join(root, "internal", "inventory"), 0755); err != nil {
		t.Fatalf("mkdir inventory: %v", err)
	}
	result, err := Init(root, InitOptions{})
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	for i := range result.Config.Contexts {
		if result.Config.Contexts[i].Path == filepath.Join("internal", "inventory") {
			result.Config.Contexts[i].Owner = "inventory-team"
		}
	}
	reviewTask, err := NewTask("Review inventory", "cli")
	if err != nil {
		t.Fatalf("NewTask() error = %v", err)
	}
	reviewTask.ContextName = "internal-inventory"
	reviewTask.ContextPath = filepath.Join("internal", "inventory")
	reviewTask.Status = TaskCompleted
	reviewTask.VerificationStatus = VerificationPassed
	reviewTask.ReviewStatus = ReviewPending
	if err := writeTaskFixture(result.Project, reviewTask); err != nil {
		t.Fatalf("writeTaskFixture(reviewTask) error = %v", err)
	}
	promotionTask, err := NewTask("Promote inventory", "cli")
	if err != nil {
		t.Fatalf("NewTask() error = %v", err)
	}
	promotionTask.ContextName = "internal-inventory"
	promotionTask.ContextPath = filepath.Join("internal", "inventory")
	promotionTask.Status = TaskCompleted
	promotionTask.VerificationStatus = VerificationPassed
	promotionTask.ReviewStatus = ReviewApproved
	if err := writeTaskFixture(result.Project, promotionTask); err != nil {
		t.Fatalf("writeTaskFixture(promotionTask) error = %v", err)
	}
	retryTask, err := NewTask("Retry root task", "cli")
	if err != nil {
		t.Fatalf("NewTask() error = %v", err)
	}
	retryTask.Status = TaskFailed
	retryTask.Attempt = 1
	if err := writeTaskFixture(result.Project, retryTask); err != nil {
		t.Fatalf("writeTaskFixture(retryTask) error = %v", err)
	}
	inbox, err := BuildOwnerInbox(result.Project, result.Config)
	if err != nil {
		t.Fatalf("BuildOwnerInbox() error = %v", err)
	}
	if len(inbox.Entries) != 2 {
		t.Fatalf("entries = %d, want 2", len(inbox.Entries))
	}
	var ownerEntry, unownedEntry *OwnerInboxEntry
	for i := range inbox.Entries {
		switch inbox.Entries[i].Owner {
		case "inventory-team":
			ownerEntry = &inbox.Entries[i]
		case unownedInboxOwner:
			unownedEntry = &inbox.Entries[i]
		}
	}
	if ownerEntry == nil || len(ownerEntry.ReviewReady) != 1 || len(ownerEntry.PromotionReady) != 1 {
		t.Fatalf("unexpected owner entry: %+v", ownerEntry)
	}
	if unownedEntry == nil || len(unownedEntry.Retryable) != 1 {
		t.Fatalf("unexpected unowned entry: %+v", unownedEntry)
	}
	rendered := FormatOwnerInbox(inbox)
	if !strings.Contains(rendered, "inventory-team") || !strings.Contains(rendered, "unowned") {
		t.Fatalf("unexpected rendered owner inbox: %q", rendered)
	}
}

func TestBuildOwnerInboxIncludesRolloutStates(t *testing.T) {
	root := t.TempDir()
	git(t, root, "init")
	for _, rel := range []string{
		filepath.Join("internal", "inventory"),
		filepath.Join("internal", "pricing"),
	} {
		if err := os.MkdirAll(filepath.Join(root, rel), 0755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
	}
	result, err := Init(root, InitOptions{})
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	for i := range result.Config.Contexts {
		switch result.Config.Contexts[i].Path {
		case filepath.Join("internal", "inventory"):
			result.Config.Contexts[i].Owner = "inventory-team"
		case filepath.Join("internal", "pricing"):
			result.Config.Contexts[i].Owner = "pricing-team"
		}
	}
	for _, item := range []string{filepath.Join("internal", "inventory"), filepath.Join("internal", "pricing")} {
		task, err := NewTask("Ship "+item, "cli")
		if err != nil {
			t.Fatalf("NewTask() error = %v", err)
		}
		task.ContextPath = item
		task.ContextName = strings.ReplaceAll(item, string(filepath.Separator), "-")
		task.Status = TaskCompleted
		task.VerificationStatus = VerificationPassed
		task.ReviewStatus = ReviewApproved
		task.PromotionStatus = PromotionApplied
		if err := writeTaskFixture(result.Project, task); err != nil {
			t.Fatalf("writeTaskFixture() error = %v", err)
		}
	}
	waves, err := BuildReleaseWavePlan(result.Project, result.Config, ReleasePlanOptions{}, ReleaseGroupByOwner)
	if err != nil {
		t.Fatalf("BuildReleaseWavePlan() error = %v", err)
	}
	if _, err := ApplyReleaseWavePlan(result.Project, waves, "", "rollout-inbox"); err != nil {
		t.Fatalf("ApplyReleaseWavePlan() error = %v", err)
	}
	if _, err := RejectReleaseWaveGate(result.Project, "rollout-inbox", 2, "waiting for approval"); err != nil {
		t.Fatalf("RejectReleaseWaveGate() error = %v", err)
	}
	inbox, err := BuildOwnerInbox(result.Project, result.Config)
	if err != nil {
		t.Fatalf("BuildOwnerInbox() error = %v", err)
	}
	for _, entry := range inbox.Entries {
		if entry.Owner == "inventory-team" && (entry.ActiveRollouts != 1 || entry.ApprovedGates != 1) {
			t.Fatalf("expected active rollout on inventory owner entry: %+v", entry)
		}
		if entry.Owner == "pricing-team" && entry.RejectedGates != 1 {
			t.Fatalf("expected rejected gate on pricing owner entry: %+v", entry)
		}
	}
	if !strings.Contains(FormatOwnerInbox(inbox), "gates: pending=0 approved=1 rejected=0") {
		t.Fatalf("expected gate counts in owner inbox, got %q", FormatOwnerInbox(inbox))
	}
}

func TestPromoteApprovedTasksForOwnerFiltersByOwner(t *testing.T) {
	root := t.TempDir()
	git(t, root, "init")
	if err := os.MkdirAll(filepath.Join(root, "internal", "inventory"), 0755); err != nil {
		t.Fatalf("mkdir inventory: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "internal", "pricing"), 0755); err != nil {
		t.Fatalf("mkdir pricing: %v", err)
	}
	result, err := Init(root, InitOptions{})
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	for i := range result.Config.Contexts {
		switch result.Config.Contexts[i].Path {
		case filepath.Join("internal", "inventory"):
			result.Config.Contexts[i].Owner = "inventory-team"
		case filepath.Join("internal", "pricing"):
			result.Config.Contexts[i].Owner = "pricing-team"
		}
	}
	for _, item := range []struct {
		path  string
		owner string
	}{
		{path: filepath.Join("internal", "inventory"), owner: "inventory-team"},
		{path: filepath.Join("internal", "pricing"), owner: "pricing-team"},
	} {
		task, err := NewTask(item.owner, "cli")
		if err != nil {
			t.Fatalf("NewTask() error = %v", err)
		}
		task.ContextName = "ctx-" + item.owner
		task.ContextPath = item.path
		task.Status = TaskCompleted
		task.VerificationStatus = VerificationPassed
		task.ReviewStatus = ReviewApproved
		if err := writeTaskFixture(result.Project, task); err != nil {
			t.Fatalf("writeTaskFixture() error = %v", err)
		}
	}
	promoted, err := PromoteApprovedTasksForOwner(context.Background(), result.Project, result.Config, "inventory-team", "batch")
	if err != nil {
		t.Fatalf("PromoteApprovedTasksForOwner() error = %v", err)
	}
	if len(promoted) != 1 {
		t.Fatalf("promoted = %d, want 1", len(promoted))
	}
	if promoted[0].ContextPath != filepath.Join("internal", "inventory") {
		t.Fatalf("unexpected promoted task: %+v", promoted[0])
	}
}

func TestRetryFailedTasksForOwnerFiltersByOwner(t *testing.T) {
	root := t.TempDir()
	git(t, root, "init")
	if err := os.MkdirAll(filepath.Join(root, "internal", "inventory"), 0755); err != nil {
		t.Fatalf("mkdir inventory: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "internal", "pricing"), 0755); err != nil {
		t.Fatalf("mkdir pricing: %v", err)
	}
	result, err := Init(root, InitOptions{})
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	result.Config.Run.MaxAttempts = 3
	for i := range result.Config.Contexts {
		switch result.Config.Contexts[i].Path {
		case filepath.Join("internal", "inventory"):
			result.Config.Contexts[i].Owner = "inventory-team"
		case filepath.Join("internal", "pricing"):
			result.Config.Contexts[i].Owner = "pricing-team"
		}
	}
	for _, item := range []struct {
		path string
		goal string
	}{
		{path: filepath.Join("internal", "inventory"), goal: "inventory"},
		{path: filepath.Join("internal", "pricing"), goal: "pricing"},
	} {
		task, err := NewTask(item.goal, "cli")
		if err != nil {
			t.Fatalf("NewTask() error = %v", err)
		}
		task.ContextName = item.goal
		task.ContextPath = item.path
		task.Status = TaskFailed
		task.Attempt = 1
		if err := writeTaskFixture(result.Project, task); err != nil {
			t.Fatalf("writeTaskFixture() error = %v", err)
		}
	}
	var seen []RunRequest
	summary, err := RetryFailedTasksForOwner(context.Background(), result.Project, result.Config, "inventory-team", fakeRunner{
		result: &RunResult{Output: "ok"},
		seen:   &seen,
	})
	if err != nil {
		t.Fatalf("RetryFailedTasksForOwner() error = %v", err)
	}
	if len(summary.Executed) != 1 {
		t.Fatalf("executed = %d, want 1", len(summary.Executed))
	}
	if len(seen) != 1 || !strings.Contains(seen[0].Prompt, "inventory") {
		t.Fatalf("unexpected seen requests: %+v", seen)
	}
}

func TestHarnessQueueReviewPromoteReleaseWaveChain(t *testing.T) {
	root := t.TempDir()
	git(t, root, "init")
	for _, rel := range []string{
		filepath.Join("internal", "inventory"),
		filepath.Join("internal", "pricing"),
	} {
		if err := os.MkdirAll(filepath.Join(root, rel), 0755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
	}
	result, err := Init(root, InitOptions{})
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	result.Config.Checks.Commands = nil
	for i := range result.Config.Contexts {
		switch result.Config.Contexts[i].Path {
		case filepath.Join("internal", "inventory"):
			result.Config.Contexts[i].Owner = "inventory-team"
		case filepath.Join("internal", "pricing"):
			result.Config.Contexts[i].Owner = "pricing-team"
		}
	}
	first, err := EnqueueTask(result.Project, "Inventory slice", "cli", QueueOptions{
		ContextName: "internal-inventory",
		ContextPath: filepath.Join("internal", "inventory"),
	})
	if err != nil {
		t.Fatalf("EnqueueTask(first) error = %v", err)
	}
	second, err := EnqueueTask(result.Project, "Pricing slice", "cli", QueueOptions{
		ContextName: "internal-pricing",
		ContextPath: filepath.Join("internal", "pricing"),
		DependsOn:   []string{first.ID},
	})
	if err != nil {
		t.Fatalf("EnqueueTask(second) error = %v", err)
	}
	queueSummary, err := RunQueuedTasks(context.Background(), result.Project, result.Config, &sequenceRunner{
		results: []*RunResult{{Output: "ok1"}, {Output: "ok2"}},
	}, QueueRunOptions{All: true})
	if err != nil {
		t.Fatalf("RunQueuedTasks() error = %v", err)
	}
	if len(queueSummary.Executed) != 2 || queueSummary.Executed[0].Task.ID != first.ID || queueSummary.Executed[1].Task.ID != second.ID {
		t.Fatalf("unexpected queue execution: %+v", queueSummary.Executed)
	}
	for _, item := range queueSummary.Executed {
		if _, err := ApproveTaskReview(result.Project, item.Task.ID, "approved"); err != nil {
			t.Fatalf("ApproveTaskReview(%s) error = %v", item.Task.ID, err)
		}
	}
	promoted, err := PromoteApprovedTasks(context.Background(), result.Project, "ship wave")
	if err != nil {
		t.Fatalf("PromoteApprovedTasks() error = %v", err)
	}
	if len(promoted) != 2 {
		t.Fatalf("promoted = %d, want 2", len(promoted))
	}
	waves, err := BuildReleaseWavePlan(result.Project, result.Config, ReleasePlanOptions{}, ReleaseGroupByOwner)
	if err != nil {
		t.Fatalf("BuildReleaseWavePlan() error = %v", err)
	}
	if len(waves.Groups) != 2 {
		t.Fatalf("expected 2 release waves, got %+v", waves.Groups)
	}
	waves, err = ApplyReleaseWavePlan(result.Project, waves, "stage rollout", "rollout-smoke")
	if err != nil {
		t.Fatalf("ApplyReleaseWavePlan() error = %v", err)
	}
	if waves.Groups[0].WaveStatus != ReleaseWaveActive || waves.Groups[1].WaveStatus != ReleaseWavePlanned {
		t.Fatalf("unexpected applied wave states: %+v", waves.Groups)
	}
	waves, err = ApproveReleaseWaveGate(result.Project, "rollout-smoke", 0, "qa ready")
	if err != nil {
		t.Fatalf("ApproveReleaseWaveGate() error = %v", err)
	}
	waves, err = AdvanceReleaseWaveRollout(result.Project, "rollout-smoke")
	if err != nil {
		t.Fatalf("AdvanceReleaseWaveRollout(first) error = %v", err)
	}
	waves, err = AdvanceReleaseWaveRollout(result.Project, "rollout-smoke")
	if err != nil {
		t.Fatalf("AdvanceReleaseWaveRollout(second) error = %v", err)
	}
	for _, group := range waves.Groups {
		if group.WaveStatus != ReleaseWaveCompleted {
			t.Fatalf("expected completed wave, got %+v", group)
		}
	}
}

func TestTaskEventsPersistToLogAndSnapshot(t *testing.T) {
	root := t.TempDir()
	git(t, root, "init")
	result, err := Init(root, InitOptions{})
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	result.Config.Run.ExecutionMode = "direct"
	result.Config.Checks.Commands = nil

	task, err := EnqueueTask(result.Project, "Implement ERP inventory projection", "cli")
	if err != nil {
		t.Fatalf("EnqueueTask() error = %v", err)
	}
	if _, err := ExecuteTask(context.Background(), result.Project, result.Config, task, fakeRunner{
		result: &RunResult{Output: "done"},
	}); err != nil {
		t.Fatalf("ExecuteTask() error = %v", err)
	}

	events := readHarnessEvents(t, result.Project)
	if len(events) < 3 {
		t.Fatalf("expected multiple harness events, got %d", len(events))
	}
	var sawCreated bool
	var sawCompleted bool
	for _, event := range events {
		if event.EntityID != task.ID {
			continue
		}
		if event.Kind == eventTaskCreated {
			sawCreated = true
		}
		if event.Kind == eventTaskStatusChanged && event.Status == string(TaskCompleted) {
			sawCompleted = true
		}
	}
	if !sawCreated || !sawCompleted {
		t.Fatalf("unexpected task events: %+v", events)
	}

	db := openSnapshotDB(t, result.Project)
	defer db.Close()
	var status string
	var logPath sql.NullString
	if err := db.QueryRow(`SELECT status, log_path FROM tasks WHERE task_id = ?`, task.ID).Scan(&status, &logPath); err != nil {
		t.Fatalf("query task snapshot: %v", err)
	}
	if status != string(TaskCompleted) {
		t.Fatalf("snapshot status = %q, want %q", status, TaskCompleted)
	}
	if !logPath.Valid || strings.TrimSpace(logPath.String) == "" {
		t.Fatalf("expected task log path in snapshot, got %+v", logPath)
	}
}

func TestReleaseWaveEventsPersistToLogAndSnapshot(t *testing.T) {
	root := t.TempDir()
	git(t, root, "init")
	for _, rel := range []string{
		filepath.Join("internal", "inventory"),
		filepath.Join("internal", "pricing"),
	} {
		if err := os.MkdirAll(filepath.Join(root, rel), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
	}
	result, err := Init(root, InitOptions{})
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	for i := range result.Config.Contexts {
		switch result.Config.Contexts[i].Path {
		case filepath.Join("internal", "inventory"):
			result.Config.Contexts[i].Owner = "inventory-team"
		case filepath.Join("internal", "pricing"):
			result.Config.Contexts[i].Owner = "pricing-team"
		}
	}
	for _, item := range []struct {
		goal string
		path string
		name string
	}{
		{goal: "Inventory wave", path: filepath.Join("internal", "inventory"), name: "internal-inventory"},
		{goal: "Pricing wave", path: filepath.Join("internal", "pricing"), name: "internal-pricing"},
	} {
		task, err := NewTask(item.goal, "cli")
		if err != nil {
			t.Fatalf("NewTask() error = %v", err)
		}
		task.ContextName = item.name
		task.ContextPath = item.path
		task.Status = TaskCompleted
		task.VerificationStatus = VerificationPassed
		task.ReviewStatus = ReviewApproved
		task.PromotionStatus = PromotionApplied
		if err := SaveTask(result.Project, task); err != nil {
			t.Fatalf("SaveTask(%s) error = %v", item.goal, err)
		}
	}

	waves, err := BuildReleaseWavePlan(result.Project, result.Config, ReleasePlanOptions{}, ReleaseGroupByOwner)
	if err != nil {
		t.Fatalf("BuildReleaseWavePlan() error = %v", err)
	}
	waves, err = ApplyReleaseWavePlan(result.Project, waves, "stage rollout", "rollout-events")
	if err != nil {
		t.Fatalf("ApplyReleaseWavePlan() error = %v", err)
	}
	secondBatchID := waves.Groups[1].BatchID
	if _, err := ApproveReleaseWaveGate(result.Project, "rollout-events", 0, "qa ready"); err != nil {
		t.Fatalf("ApproveReleaseWaveGate() error = %v", err)
	}
	if _, err := AdvanceReleaseWaveRollout(result.Project, "rollout-events"); err != nil {
		t.Fatalf("AdvanceReleaseWaveRollout() error = %v", err)
	}

	events := readHarnessEvents(t, result.Project)
	var sawWavePersisted bool
	var sawGateChanged bool
	var sawWaveStatusChanged bool
	for _, event := range events {
		if event.RolloutID != "rollout-events" {
			continue
		}
		switch event.Kind {
		case eventRolloutWavePersisted:
			sawWavePersisted = true
		case eventRolloutWaveGateStatus:
			sawGateChanged = true
		case eventRolloutWaveStatus:
			sawWaveStatusChanged = true
		}
	}
	if !sawWavePersisted || !sawGateChanged || !sawWaveStatusChanged {
		t.Fatalf("unexpected rollout events: %+v", events)
	}

	db := openSnapshotDB(t, result.Project)
	defer db.Close()
	var waveStatus string
	var gateStatus string
	if err := db.QueryRow(`SELECT wave_status, gate_status FROM release_plans WHERE batch_id = ?`, secondBatchID).Scan(&waveStatus, &gateStatus); err != nil {
		t.Fatalf("query release snapshot: %v", err)
	}
	if waveStatus != ReleaseWaveActive || gateStatus != ReleaseGateApproved {
		t.Fatalf("snapshot statuses = %q/%q, want %q/%q", waveStatus, gateStatus, ReleaseWaveActive, ReleaseGateApproved)
	}
}

func TestBuildMonitorReportSummarizesSnapshotState(t *testing.T) {
	root := t.TempDir()
	git(t, root, "init")
	result, err := Init(root, InitOptions{})
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	result.Config.Run.ExecutionMode = "direct"
	result.Config.Checks.Commands = nil

	task, err := EnqueueTask(result.Project, "Implement ERP monitor slice", "cli")
	if err != nil {
		t.Fatalf("EnqueueTask() error = %v", err)
	}
	if _, err := ExecuteTask(context.Background(), result.Project, result.Config, task, fakeRunner{
		result: &RunResult{Output: "done"},
	}); err != nil {
		t.Fatalf("ExecuteTask() error = %v", err)
	}

	report, err := BuildMonitorReport(result.Project, MonitorOptions{RecentEvents: 4, FocusTasks: 3})
	if err != nil {
		t.Fatalf("BuildMonitorReport() error = %v", err)
	}
	if report.TaskTotals.Total != 1 || report.TaskTotals.Completed != 1 {
		t.Fatalf("unexpected task totals: %+v", report.TaskTotals)
	}
	if len(report.FocusTasks) == 0 || report.FocusTasks[0].ID != task.ID {
		t.Fatalf("unexpected focus tasks: %+v", report.FocusTasks)
	}
	if len(report.RecentEvents) == 0 {
		t.Fatalf("expected recent events, got %+v", report.RecentEvents)
	}
	rendered := FormatMonitorReport(report)
	if !strings.Contains(rendered, "Harness monitor") || !strings.Contains(rendered, task.ID) || !strings.Contains(rendered, "Recent events:") {
		t.Fatalf("unexpected monitor report: %q", rendered)
	}
}

func TestBuildReleasePlanSummarizesPromotedTasks(t *testing.T) {
	root := t.TempDir()
	git(t, root, "init")
	if err := os.MkdirAll(filepath.Join(root, "internal", "inventory"), 0755); err != nil {
		t.Fatalf("mkdir inventory: %v", err)
	}
	result, err := Init(root, InitOptions{})
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	for i := range result.Config.Contexts {
		if result.Config.Contexts[i].Path == filepath.Join("internal", "inventory") {
			result.Config.Contexts[i].Owner = "inventory-team"
		}
	}
	releaseTask, err := NewTask("Ship inventory slice", "cli")
	if err != nil {
		t.Fatalf("NewTask() error = %v", err)
	}
	releaseTask.ContextName = "internal-inventory"
	releaseTask.ContextPath = filepath.Join("internal", "inventory")
	releaseTask.Status = TaskCompleted
	releaseTask.VerificationStatus = VerificationPassed
	releaseTask.ReviewStatus = ReviewApproved
	releaseTask.PromotionStatus = PromotionApplied
	if err := writeTaskFixture(result.Project, releaseTask); err != nil {
		t.Fatalf("writeTaskFixture(releaseTask) error = %v", err)
	}
	notReadyTask, err := NewTask("Still waiting", "cli")
	if err != nil {
		t.Fatalf("NewTask() error = %v", err)
	}
	notReadyTask.Status = TaskCompleted
	notReadyTask.VerificationStatus = VerificationPassed
	notReadyTask.ReviewStatus = ReviewApproved
	if err := writeTaskFixture(result.Project, notReadyTask); err != nil {
		t.Fatalf("writeTaskFixture(notReadyTask) error = %v", err)
	}
	plan, err := BuildReleasePlan(result.Project, result.Config)
	if err != nil {
		t.Fatalf("BuildReleasePlan() error = %v", err)
	}
	if len(plan.Tasks) != 1 {
		t.Fatalf("plan.Tasks = %d, want 1", len(plan.Tasks))
	}
	if plan.Owners["inventory-team"] != 1 {
		t.Fatalf("owner counts = %+v", plan.Owners)
	}
	if plan.Contexts[filepath.Join("internal", "inventory")] != 1 {
		t.Fatalf("context counts = %+v", plan.Contexts)
	}
	rendered := FormatReleasePlan(plan)
	if !strings.Contains(rendered, "Harness release plan") || !strings.Contains(rendered, releaseTask.ID) {
		t.Fatalf("unexpected rendered release plan: %q", rendered)
	}
}

func TestBuildReleasePlanFiltersByOwnerAndContext(t *testing.T) {
	root := t.TempDir()
	git(t, root, "init")
	for _, rel := range []string{
		filepath.Join("internal", "inventory"),
		filepath.Join("internal", "pricing"),
	} {
		if err := os.MkdirAll(filepath.Join(root, rel), 0755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
	}
	result, err := Init(root, InitOptions{})
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	for i := range result.Config.Contexts {
		switch result.Config.Contexts[i].Path {
		case filepath.Join("internal", "inventory"):
			result.Config.Contexts[i].Owner = "inventory-team"
		case filepath.Join("internal", "pricing"):
			result.Config.Contexts[i].Owner = "pricing-team"
		}
	}
	for _, item := range []struct {
		goal    string
		context string
	}{
		{goal: "Ship inventory", context: filepath.Join("internal", "inventory")},
		{goal: "Ship pricing", context: filepath.Join("internal", "pricing")},
	} {
		task, err := NewTask(item.goal, "cli")
		if err != nil {
			t.Fatalf("NewTask() error = %v", err)
		}
		task.ContextPath = item.context
		task.ContextName = strings.ReplaceAll(item.context, string(filepath.Separator), "-")
		task.Status = TaskCompleted
		task.VerificationStatus = VerificationPassed
		task.ReviewStatus = ReviewApproved
		task.PromotionStatus = PromotionApplied
		if err := writeTaskFixture(result.Project, task); err != nil {
			t.Fatalf("writeTaskFixture() error = %v", err)
		}
	}
	plan, err := BuildReleasePlanWithOptions(result.Project, result.Config, ReleasePlanOptions{
		Owner:   "inventory-team",
		Context: filepath.Join("internal", "inventory"),
	})
	if err != nil {
		t.Fatalf("BuildReleasePlanWithOptions() error = %v", err)
	}
	if len(plan.Tasks) != 1 || plan.Tasks[0].ContextPath != filepath.Join("internal", "inventory") {
		t.Fatalf("unexpected filtered plan: %+v", plan.Tasks)
	}
	if plan.OwnerFilter != "inventory-team" || plan.ContextFilter != filepath.Join("internal", "inventory") {
		t.Fatalf("unexpected plan filters: %+v", plan)
	}
	rendered := FormatReleasePlan(plan)
	if !strings.Contains(rendered, "scope: owner=inventory-team context=internal/inventory") {
		t.Fatalf("expected rendered scope, got %q", rendered)
	}
}

func TestBuildReleasePlanCarriesEnvironment(t *testing.T) {
	root := t.TempDir()
	git(t, root, "init")
	result, err := Init(root, InitOptions{})
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	task, err := NewTask("Ship promoted work", "cli")
	if err != nil {
		t.Fatalf("NewTask() error = %v", err)
	}
	task.Status = TaskCompleted
	task.VerificationStatus = VerificationPassed
	task.ReviewStatus = ReviewApproved
	task.PromotionStatus = PromotionApplied
	if err := writeTaskFixture(result.Project, task); err != nil {
		t.Fatalf("writeTaskFixture() error = %v", err)
	}
	plan, err := BuildReleasePlanWithOptions(result.Project, result.Config, ReleasePlanOptions{Environment: "staging"})
	if err != nil {
		t.Fatalf("BuildReleasePlanWithOptions() error = %v", err)
	}
	if plan.Environment != "staging" {
		t.Fatalf("plan.Environment = %q, want staging", plan.Environment)
	}
	if !strings.Contains(FormatReleasePlan(plan), "environment: staging") {
		t.Fatalf("expected environment in release plan output: %q", FormatReleasePlan(plan))
	}
}

func TestApplyReleasePlanMarksTasksReleased(t *testing.T) {
	root := t.TempDir()
	git(t, root, "init")
	result, err := Init(root, InitOptions{})
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	task, err := NewTask("Ship promoted work", "cli")
	if err != nil {
		t.Fatalf("NewTask() error = %v", err)
	}
	task.Status = TaskCompleted
	task.VerificationStatus = VerificationPassed
	task.ReviewStatus = ReviewApproved
	task.PromotionStatus = PromotionApplied
	if err := writeTaskFixture(result.Project, task); err != nil {
		t.Fatalf("writeTaskFixture() error = %v", err)
	}
	plan, err := BuildReleasePlan(result.Project, result.Config)
	if err != nil {
		t.Fatalf("BuildReleasePlan() error = %v", err)
	}
	plan.BatchID = "release-erp-001"
	applied, err := ApplyReleasePlan(result.Project, plan, "deploy to staging")
	if err != nil {
		t.Fatalf("ApplyReleasePlan() error = %v", err)
	}
	loaded, err := LoadTask(result.Project, task.ID)
	if err != nil {
		t.Fatalf("LoadTask() error = %v", err)
	}
	if loaded.ReleaseBatchID != "release-erp-001" || loaded.ReleaseNotes != "deploy to staging" || loaded.ReleasedAt == nil {
		t.Fatalf("unexpected released task: %+v", loaded)
	}
	if applied.ReportPath == "" {
		t.Fatal("expected release report path")
	}
	if _, err := os.Stat(applied.ReportPath); err != nil {
		t.Fatalf("expected release report to exist: %v", err)
	}
}

func TestApplyReleasePlanWithFilteredScopeOnlyReleasesMatchingTasks(t *testing.T) {
	root := t.TempDir()
	git(t, root, "init")
	for _, rel := range []string{
		filepath.Join("internal", "inventory"),
		filepath.Join("internal", "pricing"),
	} {
		if err := os.MkdirAll(filepath.Join(root, rel), 0755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
	}
	result, err := Init(root, InitOptions{})
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	for i := range result.Config.Contexts {
		switch result.Config.Contexts[i].Path {
		case filepath.Join("internal", "inventory"):
			result.Config.Contexts[i].Owner = "inventory-team"
		case filepath.Join("internal", "pricing"):
			result.Config.Contexts[i].Owner = "pricing-team"
		}
	}
	var inventoryID, pricingID string
	for _, item := range []struct {
		path string
		goal string
	}{
		{path: filepath.Join("internal", "inventory"), goal: "Ship inventory"},
		{path: filepath.Join("internal", "pricing"), goal: "Ship pricing"},
	} {
		task, err := NewTask(item.goal, "cli")
		if err != nil {
			t.Fatalf("NewTask() error = %v", err)
		}
		task.ContextPath = item.path
		task.ContextName = strings.ReplaceAll(item.path, string(filepath.Separator), "-")
		task.Status = TaskCompleted
		task.VerificationStatus = VerificationPassed
		task.ReviewStatus = ReviewApproved
		task.PromotionStatus = PromotionApplied
		if err := writeTaskFixture(result.Project, task); err != nil {
			t.Fatalf("writeTaskFixture() error = %v", err)
		}
		if item.path == filepath.Join("internal", "inventory") {
			inventoryID = task.ID
		} else {
			pricingID = task.ID
		}
	}
	plan, err := BuildReleasePlanWithOptions(result.Project, result.Config, ReleasePlanOptions{Owner: "inventory-team"})
	if err != nil {
		t.Fatalf("BuildReleasePlanWithOptions() error = %v", err)
	}
	applied, err := ApplyReleasePlan(result.Project, plan, "inventory wave")
	if err != nil {
		t.Fatalf("ApplyReleasePlan() error = %v", err)
	}
	if len(applied.Tasks) != 1 || applied.Tasks[0].ID != inventoryID {
		t.Fatalf("unexpected applied tasks: %+v", applied.Tasks)
	}
	inventoryTask, err := LoadTask(result.Project, inventoryID)
	if err != nil {
		t.Fatalf("LoadTask(inventory) error = %v", err)
	}
	pricingTask, err := LoadTask(result.Project, pricingID)
	if err != nil {
		t.Fatalf("LoadTask(pricing) error = %v", err)
	}
	if inventoryTask.ReleasedAt == nil || inventoryTask.ReleaseBatchID == "" {
		t.Fatalf("expected inventory task to be released: %+v", inventoryTask)
	}
	if pricingTask.ReleasedAt != nil || pricingTask.ReleaseBatchID != "" {
		t.Fatalf("expected pricing task to remain unreleased: %+v", pricingTask)
	}
}

func TestBuildReleaseWavePlanGroupsByOwner(t *testing.T) {
	root := t.TempDir()
	git(t, root, "init")
	for _, rel := range []string{
		filepath.Join("internal", "inventory"),
		filepath.Join("internal", "pricing"),
	} {
		if err := os.MkdirAll(filepath.Join(root, rel), 0755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
	}
	result, err := Init(root, InitOptions{})
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	for i := range result.Config.Contexts {
		switch result.Config.Contexts[i].Path {
		case filepath.Join("internal", "inventory"):
			result.Config.Contexts[i].Owner = "inventory-team"
		case filepath.Join("internal", "pricing"):
			result.Config.Contexts[i].Owner = "pricing-team"
		}
	}
	for _, item := range []struct {
		goal string
		path string
	}{
		{goal: "Ship inventory", path: filepath.Join("internal", "inventory")},
		{goal: "Ship pricing", path: filepath.Join("internal", "pricing")},
	} {
		task, err := NewTask(item.goal, "cli")
		if err != nil {
			t.Fatalf("NewTask() error = %v", err)
		}
		task.ContextPath = item.path
		task.ContextName = strings.ReplaceAll(item.path, string(filepath.Separator), "-")
		task.Status = TaskCompleted
		task.VerificationStatus = VerificationPassed
		task.ReviewStatus = ReviewApproved
		task.PromotionStatus = PromotionApplied
		if err := writeTaskFixture(result.Project, task); err != nil {
			t.Fatalf("writeTaskFixture() error = %v", err)
		}
	}
	waves, err := BuildReleaseWavePlan(result.Project, result.Config, ReleasePlanOptions{}, ReleaseGroupByOwner)
	if err != nil {
		t.Fatalf("BuildReleaseWavePlan() error = %v", err)
	}
	if len(waves.Groups) != 2 || waves.TotalTasks != 2 {
		t.Fatalf("unexpected release waves: %+v", waves)
	}
	if waves.Groups[0].GroupLabel != "inventory-team" || waves.Groups[1].GroupLabel != "pricing-team" {
		t.Fatalf("unexpected wave labels: %+v", waves.Groups)
	}
	rendered := FormatReleaseWavePlan(waves)
	if !strings.Contains(rendered, "Harness release waves by owner") || !strings.Contains(rendered, "inventory-team") {
		t.Fatalf("unexpected rendered release waves: %q", rendered)
	}
}

func TestBuildReleaseWavePlanGroupsByContext(t *testing.T) {
	root := t.TempDir()
	git(t, root, "init")
	for _, rel := range []string{
		filepath.Join("internal", "inventory"),
		filepath.Join("internal", "pricing"),
	} {
		if err := os.MkdirAll(filepath.Join(root, rel), 0755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
	}
	result, err := Init(root, InitOptions{})
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	for _, rel := range []string{
		filepath.Join("internal", "inventory"),
		filepath.Join("internal", "pricing"),
	} {
		task, err := NewTask("Ship "+rel, "cli")
		if err != nil {
			t.Fatalf("NewTask() error = %v", err)
		}
		task.ContextPath = rel
		task.ContextName = strings.ReplaceAll(rel, string(filepath.Separator), "-")
		task.Status = TaskCompleted
		task.VerificationStatus = VerificationPassed
		task.ReviewStatus = ReviewApproved
		task.PromotionStatus = PromotionApplied
		if err := writeTaskFixture(result.Project, task); err != nil {
			t.Fatalf("writeTaskFixture() error = %v", err)
		}
	}
	waves, err := BuildReleaseWavePlan(result.Project, result.Config, ReleasePlanOptions{}, ReleaseGroupByContext)
	if err != nil {
		t.Fatalf("BuildReleaseWavePlan() error = %v", err)
	}
	if len(waves.Groups) != 2 {
		t.Fatalf("unexpected release waves: %+v", waves)
	}
	if waves.Groups[0].GroupLabel != filepath.Join("internal", "inventory") || waves.Groups[1].GroupLabel != filepath.Join("internal", "pricing") {
		t.Fatalf("unexpected context waves: %+v", waves.Groups)
	}
}

func TestApplyReleaseWavePlanAssignsDistinctBatchIDs(t *testing.T) {
	root := t.TempDir()
	git(t, root, "init")
	for _, rel := range []string{
		filepath.Join("internal", "inventory"),
		filepath.Join("internal", "pricing"),
	} {
		if err := os.MkdirAll(filepath.Join(root, rel), 0755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
	}
	result, err := Init(root, InitOptions{})
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	for i := range result.Config.Contexts {
		switch result.Config.Contexts[i].Path {
		case filepath.Join("internal", "inventory"):
			result.Config.Contexts[i].Owner = "inventory-team"
		case filepath.Join("internal", "pricing"):
			result.Config.Contexts[i].Owner = "pricing-team"
		}
	}
	var inventoryID, pricingID string
	for _, item := range []struct {
		goal string
		path string
	}{
		{goal: "Ship inventory", path: filepath.Join("internal", "inventory")},
		{goal: "Ship pricing", path: filepath.Join("internal", "pricing")},
	} {
		task, err := NewTask(item.goal, "cli")
		if err != nil {
			t.Fatalf("NewTask() error = %v", err)
		}
		task.ContextPath = item.path
		task.ContextName = strings.ReplaceAll(item.path, string(filepath.Separator), "-")
		task.Status = TaskCompleted
		task.VerificationStatus = VerificationPassed
		task.ReviewStatus = ReviewApproved
		task.PromotionStatus = PromotionApplied
		if err := writeTaskFixture(result.Project, task); err != nil {
			t.Fatalf("writeTaskFixture() error = %v", err)
		}
		if strings.Contains(item.goal, "inventory") {
			inventoryID = task.ID
		} else {
			pricingID = task.ID
		}
	}
	waves, err := BuildReleaseWavePlan(result.Project, result.Config, ReleasePlanOptions{}, ReleaseGroupByOwner)
	if err != nil {
		t.Fatalf("BuildReleaseWavePlan() error = %v", err)
	}
	applied, err := ApplyReleaseWavePlan(result.Project, waves, "wave rollout", "release-erp")
	if err != nil {
		t.Fatalf("ApplyReleaseWavePlan() error = %v", err)
	}
	if len(applied.Groups) != 2 {
		t.Fatalf("unexpected applied groups: %+v", applied.Groups)
	}
	inventoryTask, err := LoadTask(result.Project, inventoryID)
	if err != nil {
		t.Fatalf("LoadTask(inventory) error = %v", err)
	}
	pricingTask, err := LoadTask(result.Project, pricingID)
	if err != nil {
		t.Fatalf("LoadTask(pricing) error = %v", err)
	}
	if inventoryTask.ReleaseBatchID != "release-erp-owner-inventory-team" {
		t.Fatalf("unexpected inventory batch id: %+v", inventoryTask)
	}
	if pricingTask.ReleaseBatchID != "release-erp-owner-pricing-team" {
		t.Fatalf("unexpected pricing batch id: %+v", pricingTask)
	}
	if inventoryTask.ReleasedAt == nil || pricingTask.ReleasedAt == nil {
		t.Fatalf("expected both tasks to be released: %+v %+v", inventoryTask, pricingTask)
	}
}

func TestReleaseWaveRolloutAdvanceProgressesWaveStates(t *testing.T) {
	root := t.TempDir()
	git(t, root, "init")
	for _, rel := range []string{
		filepath.Join("internal", "inventory"),
		filepath.Join("internal", "pricing"),
	} {
		if err := os.MkdirAll(filepath.Join(root, rel), 0755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
	}
	result, err := Init(root, InitOptions{})
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	for i := range result.Config.Contexts {
		switch result.Config.Contexts[i].Path {
		case filepath.Join("internal", "inventory"):
			result.Config.Contexts[i].Owner = "inventory-team"
		case filepath.Join("internal", "pricing"):
			result.Config.Contexts[i].Owner = "pricing-team"
		}
	}
	for _, item := range []struct {
		goal string
		path string
	}{
		{goal: "Ship inventory", path: filepath.Join("internal", "inventory")},
		{goal: "Ship pricing", path: filepath.Join("internal", "pricing")},
	} {
		task, err := NewTask(item.goal, "cli")
		if err != nil {
			t.Fatalf("NewTask() error = %v", err)
		}
		task.ContextPath = item.path
		task.ContextName = strings.ReplaceAll(item.path, string(filepath.Separator), "-")
		task.Status = TaskCompleted
		task.VerificationStatus = VerificationPassed
		task.ReviewStatus = ReviewApproved
		task.PromotionStatus = PromotionApplied
		if err := writeTaskFixture(result.Project, task); err != nil {
			t.Fatalf("writeTaskFixture() error = %v", err)
		}
	}
	waves, err := BuildReleaseWavePlan(result.Project, result.Config, ReleasePlanOptions{}, ReleaseGroupByOwner)
	if err != nil {
		t.Fatalf("BuildReleaseWavePlan() error = %v", err)
	}
	applied, err := ApplyReleaseWavePlan(result.Project, waves, "wave rollout", "rollout-erp")
	if err != nil {
		t.Fatalf("ApplyReleaseWavePlan() error = %v", err)
	}
	rollouts, err := ListReleaseWaveRollouts(result.Project)
	if err != nil {
		t.Fatalf("ListReleaseWaveRollouts() error = %v", err)
	}
	if len(rollouts) != 1 || rollouts[0].RolloutID != "rollout-erp" {
		t.Fatalf("unexpected rollouts: %+v", rollouts)
	}
	if applied.Groups[0].WaveStatus != ReleaseWaveActive || applied.Groups[0].GateStatus != ReleaseGateApproved || applied.Groups[1].WaveStatus != ReleaseWavePlanned || applied.Groups[1].GateStatus != ReleaseGatePending {
		t.Fatalf("unexpected initial wave states: %+v", applied.Groups)
	}
	if _, err := AdvanceReleaseWaveRollout(result.Project, "rollout-erp"); err == nil {
		t.Fatal("expected advance to require an approved gate on the next wave")
	}
	approved, err := ApproveReleaseWaveGate(result.Project, "rollout-erp", 0, "change board approved")
	if err != nil {
		t.Fatalf("ApproveReleaseWaveGate() error = %v", err)
	}
	if approved.Groups[1].GateStatus != ReleaseGateApproved || approved.Groups[1].GateNote != "change board approved" {
		t.Fatalf("unexpected gate approval state: %+v", approved.Groups[1])
	}
	advanced, err := AdvanceReleaseWaveRollout(result.Project, "rollout-erp")
	if err != nil {
		t.Fatalf("AdvanceReleaseWaveRollout(first) error = %v", err)
	}
	if advanced.Groups[0].WaveStatus != ReleaseWaveCompleted || advanced.Groups[1].WaveStatus != ReleaseWaveActive {
		t.Fatalf("unexpected advanced wave states: %+v", advanced.Groups)
	}
	advanced, err = AdvanceReleaseWaveRollout(result.Project, "rollout-erp")
	if err != nil {
		t.Fatalf("AdvanceReleaseWaveRollout(second) error = %v", err)
	}
	if advanced.Groups[1].WaveStatus != ReleaseWaveCompleted {
		t.Fatalf("expected final wave completed: %+v", advanced.Groups)
	}
	rendered := FormatReleaseWaveRollouts([]*ReleaseWavePlan{advanced})
	if !strings.Contains(rendered, "rollout=rollout-erp") || !strings.Contains(rendered, "status=completed") {
		t.Fatalf("unexpected rendered rollouts: %q", rendered)
	}
	if !strings.Contains(rendered, "gate=approved") {
		t.Fatalf("expected rendered rollouts to include gate status, got %q", rendered)
	}
}

func TestReleaseWaveGateRejectBlocksAdvanceUntilApproved(t *testing.T) {
	root := t.TempDir()
	git(t, root, "init")
	for _, rel := range []string{
		filepath.Join("internal", "inventory"),
		filepath.Join("internal", "pricing"),
	} {
		if err := os.MkdirAll(filepath.Join(root, rel), 0755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
	}
	result, err := Init(root, InitOptions{})
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	for i := range result.Config.Contexts {
		switch result.Config.Contexts[i].Path {
		case filepath.Join("internal", "inventory"):
			result.Config.Contexts[i].Owner = "inventory-team"
		case filepath.Join("internal", "pricing"):
			result.Config.Contexts[i].Owner = "pricing-team"
		}
	}
	for _, item := range []string{filepath.Join("internal", "inventory"), filepath.Join("internal", "pricing")} {
		task, err := NewTask("Ship "+item, "cli")
		if err != nil {
			t.Fatalf("NewTask() error = %v", err)
		}
		task.ContextPath = item
		task.ContextName = strings.ReplaceAll(item, string(filepath.Separator), "-")
		task.Status = TaskCompleted
		task.VerificationStatus = VerificationPassed
		task.ReviewStatus = ReviewApproved
		task.PromotionStatus = PromotionApplied
		if err := writeTaskFixture(result.Project, task); err != nil {
			t.Fatalf("writeTaskFixture() error = %v", err)
		}
	}
	waves, err := BuildReleaseWavePlan(result.Project, result.Config, ReleasePlanOptions{}, ReleaseGroupByOwner)
	if err != nil {
		t.Fatalf("BuildReleaseWavePlan() error = %v", err)
	}
	if _, err := ApplyReleaseWavePlan(result.Project, waves, "", "rollout-gates"); err != nil {
		t.Fatalf("ApplyReleaseWavePlan() error = %v", err)
	}
	rejected, err := RejectReleaseWaveGate(result.Project, "rollout-gates", 0, "waiting for policy review")
	if err != nil {
		t.Fatalf("RejectReleaseWaveGate() error = %v", err)
	}
	if rejected.Groups[1].GateStatus != ReleaseGateRejected || rejected.Groups[1].GateNote != "waiting for policy review" {
		t.Fatalf("unexpected rejected gate state: %+v", rejected.Groups[1])
	}
	if _, err := AdvanceReleaseWaveRollout(result.Project, "rollout-gates"); err == nil {
		t.Fatal("expected rejected gate to block advance")
	}
	approved, err := ApproveReleaseWaveGate(result.Project, "rollout-gates", 2, "policy review passed")
	if err != nil {
		t.Fatalf("ApproveReleaseWaveGate() error = %v", err)
	}
	if approved.Groups[1].GateStatus != ReleaseGateApproved || approved.Groups[1].GateNote != "policy review passed" {
		t.Fatalf("unexpected approved gate state: %+v", approved.Groups[1])
	}
	advanced, err := AdvanceReleaseWaveRollout(result.Project, "rollout-gates")
	if err != nil {
		t.Fatalf("AdvanceReleaseWaveRollout() error = %v", err)
	}
	if advanced.Groups[1].WaveStatus != ReleaseWaveActive {
		t.Fatalf("expected second wave active after approval: %+v", advanced.Groups[1])
	}
}

func TestReleaseWaveRolloutPauseResumeAbortControls(t *testing.T) {
	root := t.TempDir()
	git(t, root, "init")
	for _, rel := range []string{
		filepath.Join("internal", "inventory"),
		filepath.Join("internal", "pricing"),
	} {
		if err := os.MkdirAll(filepath.Join(root, rel), 0755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
	}
	result, err := Init(root, InitOptions{})
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	for i := range result.Config.Contexts {
		switch result.Config.Contexts[i].Path {
		case filepath.Join("internal", "inventory"):
			result.Config.Contexts[i].Owner = "inventory-team"
		case filepath.Join("internal", "pricing"):
			result.Config.Contexts[i].Owner = "pricing-team"
		}
	}
	for _, item := range []struct {
		goal string
		path string
	}{
		{goal: "Ship inventory", path: filepath.Join("internal", "inventory")},
		{goal: "Ship pricing", path: filepath.Join("internal", "pricing")},
	} {
		task, err := NewTask(item.goal, "cli")
		if err != nil {
			t.Fatalf("NewTask() error = %v", err)
		}
		task.ContextPath = item.path
		task.ContextName = strings.ReplaceAll(item.path, string(filepath.Separator), "-")
		task.Status = TaskCompleted
		task.VerificationStatus = VerificationPassed
		task.ReviewStatus = ReviewApproved
		task.PromotionStatus = PromotionApplied
		if err := writeTaskFixture(result.Project, task); err != nil {
			t.Fatalf("writeTaskFixture() error = %v", err)
		}
	}
	waves, err := BuildReleaseWavePlan(result.Project, result.Config, ReleasePlanOptions{}, ReleaseGroupByOwner)
	if err != nil {
		t.Fatalf("BuildReleaseWavePlan() error = %v", err)
	}
	applied, err := ApplyReleaseWavePlan(result.Project, waves, "wave rollout", "rollout-controls")
	if err != nil {
		t.Fatalf("ApplyReleaseWavePlan() error = %v", err)
	}
	if applied.Groups[0].WaveStatus != ReleaseWaveActive {
		t.Fatalf("expected initial active wave, got %+v", applied.Groups)
	}
	paused, err := PauseReleaseWaveRollout(result.Project, "rollout-controls", "waiting for signoff")
	if err != nil {
		t.Fatalf("PauseReleaseWaveRollout() error = %v", err)
	}
	if paused.Groups[0].WaveStatus != ReleaseWavePaused || paused.Groups[0].StatusNote != "waiting for signoff" || paused.Groups[0].PausedAt == nil {
		t.Fatalf("unexpected paused rollout: %+v", paused.Groups[0])
	}
	if _, err := AdvanceReleaseWaveRollout(result.Project, "rollout-controls"); err == nil {
		t.Fatal("expected advance to fail while rollout is paused")
	}
	resumed, err := ResumeReleaseWaveRollout(result.Project, "rollout-controls", "signoff received")
	if err != nil {
		t.Fatalf("ResumeReleaseWaveRollout() error = %v", err)
	}
	if resumed.Groups[0].WaveStatus != ReleaseWaveActive || resumed.Groups[0].StatusNote != "signoff received" || resumed.Groups[0].PausedAt != nil {
		t.Fatalf("unexpected resumed rollout: %+v", resumed.Groups[0])
	}
	aborted, err := AbortReleaseWaveRollout(result.Project, "rollout-controls", "freeze window")
	if err != nil {
		t.Fatalf("AbortReleaseWaveRollout() error = %v", err)
	}
	for _, group := range aborted.Groups {
		if group.WaveStatus != ReleaseWaveAborted || group.StatusNote != "freeze window" || group.AbortedAt == nil {
			t.Fatalf("expected aborted rollout group, got %+v", group)
		}
	}
	if _, err := ResumeReleaseWaveRollout(result.Project, "rollout-controls", "retry"); err == nil {
		t.Fatal("expected resume to fail for aborted rollout")
	}
	if _, err := AdvanceReleaseWaveRollout(result.Project, "rollout-controls"); err == nil {
		t.Fatal("expected advance to fail for aborted rollout")
	}
	report, err := Doctor(result.Project, result.Config)
	if err != nil {
		t.Fatalf("Doctor() error = %v", err)
	}
	if report.AbortedRollouts != 2 || !strings.Contains(FormatDoctorReport(report), "aborted=2") {
		t.Fatalf("unexpected doctor rollout report: %+v", report)
	}
	contexts, err := BuildContextReport(result.Project, result.Config)
	if err != nil {
		t.Fatalf("BuildContextReport() error = %v", err)
	}
	foundAbortedContext := false
	for _, summary := range contexts.Summaries {
		if summary.AbortedRollouts > 0 {
			foundAbortedContext = true
			break
		}
	}
	if !foundAbortedContext || !strings.Contains(FormatContextReport(contexts), "aborted=1") {
		t.Fatalf("unexpected context rollout report: %+v", contexts.Summaries)
	}
	inbox, err := BuildOwnerInbox(result.Project, result.Config)
	if err != nil {
		t.Fatalf("BuildOwnerInbox() error = %v", err)
	}
	foundAbortedOwner := false
	for _, entry := range inbox.Entries {
		if entry.AbortedRollouts > 0 {
			foundAbortedOwner = true
			break
		}
	}
	if !foundAbortedOwner || !strings.Contains(FormatOwnerInbox(inbox), "aborted=1") {
		t.Fatalf("unexpected owner inbox rollout report: %+v", inbox.Entries)
	}
}

func TestReleaseWaveRolloutsFilterByEnvironment(t *testing.T) {
	root := t.TempDir()
	git(t, root, "init")
	for _, rel := range []string{
		filepath.Join("internal", "inventory"),
		filepath.Join("internal", "pricing"),
	} {
		if err := os.MkdirAll(filepath.Join(root, rel), 0755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
	}
	result, err := Init(root, InitOptions{})
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	for _, env := range []string{"staging", "prod"} {
		for _, item := range []string{filepath.Join("internal", "inventory"), filepath.Join("internal", "pricing")} {
			task, err := NewTask(env+"-"+item, "cli")
			if err != nil {
				t.Fatalf("NewTask() error = %v", err)
			}
			task.ContextPath = item
			task.ContextName = strings.ReplaceAll(item, string(filepath.Separator), "-")
			task.Status = TaskCompleted
			task.VerificationStatus = VerificationPassed
			task.ReviewStatus = ReviewApproved
			task.PromotionStatus = PromotionApplied
			if err := writeTaskFixture(result.Project, task); err != nil {
				t.Fatalf("writeTaskFixture() error = %v", err)
			}
		}
		waves, err := BuildReleaseWavePlan(result.Project, result.Config, ReleasePlanOptions{Environment: env}, ReleaseGroupByContext)
		if err != nil {
			t.Fatalf("BuildReleaseWavePlan() error = %v", err)
		}
		if _, err := ApplyReleaseWavePlan(result.Project, waves, "", "rollout-"+env); err != nil {
			t.Fatalf("ApplyReleaseWavePlan() error = %v", err)
		}
	}
	rollouts, err := ListReleaseWaveRollouts(result.Project)
	if err != nil {
		t.Fatalf("ListReleaseWaveRollouts() error = %v", err)
	}
	filtered := FilterReleaseWaveRolloutsByEnvironment(rollouts, "prod")
	if len(filtered) != 1 || filtered[0].Environment != "prod" {
		t.Fatalf("unexpected filtered rollouts: %+v", filtered)
	}
}

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	// Disable global hooks (e.g., ggshield) to avoid API rate limits in tests.
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, out)
	}
}

// gitDisableHooks sets core.hooksPath to /dev/null in the test repo
// so that Init()'s internal git commands also skip global hooks.
func gitDisableHooks(t *testing.T, dir string) {
	t.Helper()
	cmd := exec.Command("git", "config", "core.hooksPath", "/dev/null")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git config hooksPath failed: %v\n%s", err, out)
	}
}

func commitHarnessScaffold(t *testing.T, dir string, result *InitResult) {
	t.Helper()
	if result == nil || len(result.CreatedPaths) == 0 {
		return
	}
	args := append([]string{"add", "--"}, result.CreatedPaths...)
	git(t, dir, args...)
	// Use --allow-empty in case Init already committed these files.
	// Use --no-verify to skip global hooks (e.g., ggshield) in test environments.
	git(t, dir, "commit", "--allow-empty", "--no-verify", "-m", "add harness scaffold")
}

func writeTaskFixture(project Project, task *Task) error {
	data, err := json.MarshalIndent(task, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(project.TasksDir, 0755); err != nil {
		return err
	}
	return os.WriteFile(taskPath(project, task.ID), data, 0644)
}

func readHarnessEvents(t *testing.T, project Project) []harnessEvent {
	t.Helper()
	data, err := os.ReadFile(project.EventLogPath)
	if err != nil {
		t.Fatalf("read harness event log: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	events := make([]harnessEvent, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var event harnessEvent
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatalf("decode harness event %q: %v", line, err)
		}
		events = append(events, event)
	}
	return events
}

func openSnapshotDB(t *testing.T, project Project) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", project.SnapshotPath)
	if err != nil {
		t.Fatalf("open snapshot db: %v", err)
	}
	return db
}
