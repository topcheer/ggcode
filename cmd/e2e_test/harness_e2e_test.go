package e2e_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// --- Helpers ---

// ggcodeBinary returns the path to the ggcode binary for e2e tests.
func ggcodeBinary(t *testing.T) string {
	t.Helper()
	// Prefer the locally-built binary
	local := filepath.Join(filepath.Join("..", ".."), "bin", "ggcode")
	if abs, err := filepath.Abs(local); err == nil {
		if _, err := os.Stat(abs); err == nil {
			return abs
		}
	}
	// Fall back to installed binary
	path, err := exec.LookPath("ggcode")
	if err != nil {
		t.Skip("ggcode binary not found; run `make build` first")
	}
	return path
}

// ggcodeIsolatedEnv returns isolated environment variables for ggcode e2e tests.
// Prevents ggcode from finding global config (~/.ggcode) that may trigger LLM calls.
func ggcodeIsolatedEnv(t *testing.T) []string {
	t.Helper()
	// Each test gets a stable isolated HOME for the duration of the test
	return []string{
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_SYSTEM=/dev/null",
		"HOME=" + t.TempDir(),
	}
}

// runGGCode executes a ggcode subcommand in the given directory and returns its combined output.
func runGGCode(t *testing.T, dir string, args ...string) string {
	t.Helper()
	bin := ggcodeBinary(t)
	cmd := exec.Command(bin, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), ggcodeIsolatedEnv(t)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("ggcode %v failed: %v\n%s", args, err, out)
	}
	return string(out)
}

// runGGCodeOk runs a ggcode command and returns output, allowing non-zero exit.
func runGGCodeOk(t *testing.T, dir string, args ...string) (string, error) {
	t.Helper()
	bin := ggcodeBinary(t)
	cmd := exec.Command(bin, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), ggcodeIsolatedEnv(t)...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// setupE2EHarnessProject creates a temp directory with git init + initial commit,
// then runs ggcode harness init. Returns the project root directory.
func setupE2EHarnessProject(t *testing.T) string {
	t.Helper()
	return setupE2EHarnessProjectWithGoal(t, "e2e test project")
}

func setupE2EHarnessProjectWithGoal(t *testing.T, goal string) string {
	t.Helper()
	root := t.TempDir()

	// git init + config
	gitE2E(t, root, "init")
	gitE2E(t, root, "config", "user.name", "E2E Test")
	gitE2E(t, root, "config", "user.email", "e2e@test.com")

	// Seed file so we have a real commit
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("# E2E Test\n"), 0644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	gitE2E(t, root, "add", "README.md")
	gitE2E(t, root, "commit", "--no-verify", "-m", "init")

	// Create internal dirs with .keep so contexts are auto-detected
	os.MkdirAll(filepath.Join(root, "internal", "api"), 0755)
	os.MkdirAll(filepath.Join(root, "internal", "web"), 0755)
	os.WriteFile(filepath.Join(root, "internal", "api", ".keep"), []byte(""), 0644)
	os.WriteFile(filepath.Join(root, "internal", "web", ".keep"), []byte(""), 0644)
	gitE2E(t, root, "add", "internal")
	gitE2E(t, root, "commit", "--no-verify", "-m", "add internal dirs")

	// ggcode harness init
	output := runGGCode(t, root, "harness", "init", "--goal", goal)
	if !strings.Contains(output, "Harness initialized") {
		t.Fatalf("harness init failed: %s", output)
	}
	return root
}

// gitE2E runs a git command in the given directory (with hooks disabled).
func gitE2E(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_SYSTEM=/dev/null",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// extractTaskID extracts a harness task ID from ggcode output.
var taskIDPattern = regexp.MustCompile(`([0-9a-f]{16})`)

func extractTaskID(t *testing.T, output string) string {
	t.Helper()
	m := taskIDPattern.FindString(output)
	if m == "" {
		t.Fatalf("no task ID found in output: %s", output)
	}
	return m
}

// --- Test: Init ---

func TestE2EHarnessInit(t *testing.T) {
	root := setupE2EHarnessProject(t)

	for _, path := range []string{
		".ggcode/harness.yaml",
		"AGENTS.md",
		"docs/runbooks/harness.md",
	} {
		if _, err := os.Stat(filepath.Join(root, path)); err != nil {
			t.Errorf("expected scaffold file %s: %v", path, err)
		}
	}

	cmd := exec.Command("git", "log", "--oneline")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git log: %v", err)
	}
	if !strings.Contains(string(out), "harness scaffold") && !strings.Contains(string(out), "init") {
		t.Errorf("expected scaffold commit in git log, got:\n%s", out)
	}
}

// --- Test: Queue + Tasks ---

func TestE2EHarnessQueueAndTasks(t *testing.T) {
	root := setupE2EHarnessProject(t)

	output := runGGCode(t, root, "harness", "queue", "implement user auth")
	if !strings.Contains(output, "Queued harness task") {
		t.Fatalf("expected queue output, got: %s", output)
	}
	taskID := extractTaskID(t, output)

	tasksOutput := runGGCode(t, root, "harness", "tasks")
	if !strings.Contains(tasksOutput, taskID) {
		t.Errorf("expected task %s in tasks list, got:\n%s", taskID, tasksOutput)
	}
	if !strings.Contains(tasksOutput, "queued") {
		t.Errorf("expected 'queued' status in tasks list")
	}
	if !strings.Contains(tasksOutput, "implement user auth") {
		t.Errorf("expected goal text in tasks list")
	}

	output2 := runGGCode(t, root, "harness", "queue", "write auth tests", "--depends-on", taskID)
	if !strings.Contains(output2, "Queued harness task") {
		t.Fatalf("expected second queue output, got: %s", output2)
	}

	tasksOutput2 := runGGCode(t, root, "harness", "tasks")
	taskID2 := extractTaskID(t, output2)
	for _, id := range []string{taskID, taskID2} {
		if !strings.Contains(tasksOutput2, id) {
			t.Errorf("expected task %s in tasks list", id)
		}
	}
}

// --- Test: Check ---

func TestE2EHarnessCheck(t *testing.T) {
	root := setupE2EHarnessProject(t)
	output := runGGCode(t, root, "harness", "check")
	if !strings.Contains(output, "Harness check") {
		t.Errorf("expected check output, got:\n%s", output)
	}
}

// --- Test: Doctor ---

func TestE2EHarnessDoctor(t *testing.T) {
	root := setupE2EHarnessProject(t)
	output := runGGCode(t, root, "harness", "doctor")
	for _, want := range []string{"Harness doctor", "structure: ok", "contexts:"} {
		if !strings.Contains(output, want) {
			t.Errorf("expected %q in doctor output", want)
		}
	}
}

// --- Test: GC ---

func TestE2EHarnessGC(t *testing.T) {
	root := setupE2EHarnessProject(t)
	output := runGGCode(t, root, "harness", "gc")
	if !strings.Contains(output, "Harness gc complete") {
		t.Errorf("expected gc output, got:\n%s", output)
	}
}

// --- Test: Contexts ---

func TestE2EHarnessContexts(t *testing.T) {
	root := setupE2EHarnessProject(t)
	output := runGGCode(t, root, "harness", "contexts")
	if !strings.Contains(output, "Harness contexts:") {
		t.Errorf("expected contexts header, got:\n%s", output)
	}
	if !strings.Contains(output, "internal") {
		t.Errorf("expected internal context in output")
	}
}

// --- Test: Monitor ---

func TestE2EHarnessMonitor(t *testing.T) {
	root := setupE2EHarnessProject(t)
	output := runGGCode(t, root, "harness", "monitor")
	for _, want := range []string{"Harness monitor", "Tasks:", "total="} {
		if !strings.Contains(output, want) {
			t.Errorf("expected %q in monitor output", want)
		}
	}
}

// --- Test: Inbox ---

func TestE2EHarnessInbox(t *testing.T) {
	root := setupE2EHarnessProject(t)
	output := runGGCode(t, root, "harness", "inbox")
	if !strings.Contains(output, "harness owner inbox") && !strings.Contains(output, "No harness owner inbox") {
		t.Errorf("expected inbox output, got:\n%s", output)
	}
}

// --- Test: Review (no tasks waiting) ---

func TestE2EHarnessReviewEmpty(t *testing.T) {
	root := setupE2EHarnessProject(t)
	output := runGGCode(t, root, "harness", "review")
	if !strings.Contains(output, "No harness tasks are waiting for review") {
		t.Errorf("expected no-review message, got:\n%s", output)
	}
}

// --- Test: Promote (no tasks ready) ---

func TestE2EHarnessPromoteEmpty(t *testing.T) {
	root := setupE2EHarnessProject(t)
	output := runGGCode(t, root, "harness", "promote")
	if !strings.Contains(output, "No harness tasks are ready for promotion") {
		t.Errorf("expected no-promotion message, got:\n%s", output)
	}
}

// --- Test: Release (no tasks ready) ---

func TestE2EHarnessReleaseEmpty(t *testing.T) {
	root := setupE2EHarnessProject(t)
	output := runGGCode(t, root, "harness", "release")
	if !strings.Contains(output, "No harness tasks are ready for release") {
		t.Errorf("expected no-release message, got:\n%s", output)
	}
}

// --- Test: Rerun nonexistent task ---

func TestE2EHarnessRerunNonexistent(t *testing.T) {
	root := setupE2EHarnessProject(t)
	output, err := runGGCodeOk(t, root, "harness", "rerun", "deadbeef00000000")
	if err == nil {
		t.Errorf("expected rerun to fail for nonexistent task, got: %s", output)
	}
}

// --- Test: Full task lifecycle (queue -> doctor -> contexts -> monitor -> gc) ---

func TestE2EHarnessFullLifecycleNoLLM(t *testing.T) {
	root := setupE2EHarnessProject(t)

	// 1. Queue a task
	queueOutput := runGGCode(t, root, "harness", "queue", "build API endpoint")
	taskID := extractTaskID(t, queueOutput)

	// 2. Verify in tasks list
	tasksOutput := runGGCode(t, root, "harness", "tasks")
	if !strings.Contains(tasksOutput, taskID) {
		t.Fatalf("task not in list after queue")
	}

	// 3. Doctor shows 1 task
	doctorOutput := runGGCode(t, root, "harness", "doctor")
	if !strings.Contains(doctorOutput, "total=1") {
		t.Errorf("expected total=1 in doctor, got:\n%s", doctorOutput)
	}

	// 4. Contexts shows unscoped task
	contextsOutput := runGGCode(t, root, "harness", "contexts")
	if !strings.Contains(contextsOutput, "total=1") {
		t.Errorf("expected total=1 in contexts")
	}

	// 5. Monitor shows task
	monitorOutput := runGGCode(t, root, "harness", "monitor")
	if !strings.Contains(monitorOutput, taskID) {
		t.Errorf("expected task %s in monitor", taskID)
	}

	// 6. GC reports 0 archived
	gcOutput := runGGCode(t, root, "harness", "gc")
	if !strings.Contains(gcOutput, "archived tasks: 0") {
		t.Errorf("expected 0 archived in gc, got:\n%s", gcOutput)
	}
}

// --- Test: Run with real LLM ---
// Skipped unless GGCODE_E2E_API_KEY or ZAI_API_KEY is set.
// Creates a proper vendor/endpoint config and runs a real LLM task.

func TestE2EHarnessRunWithLLM(t *testing.T) {
	apiKey := os.Getenv("GGCODE_E2E_API_KEY")
	if apiKey == "" {
		apiKey = os.Getenv("ZAI_API_KEY")
	}
	if apiKey == "" {
		t.Skip("GGCODE_E2E_API_KEY or ZAI_API_KEY required for LLM e2e test")
	}

	root := setupE2EHarnessProject(t)

	// Write a proper vendor/endpoint config (API key in file, not env)
	ggcodeYAML := "vendor: e2e-test\n" +
		"vendors:\n" +
		"  e2e-test:\n" +
		"    api_key: " + apiKey + "\n" +
		"    display_name: E2E Test\n" +
		"    endpoints:\n" +
		"      default:\n" +
		"        auth_type: api_key\n" +
		"        base_url: https://open.bigmodel.cn/api/anthropic\n" +
		"        default_model: glm-5-turbo\n" +
		"        max_tokens: 512\n" +
		"        protocol: anthropic\n" +
		"endpoint: default\n" +
		"model: glm-5-turbo\n"
	if err := os.WriteFile(filepath.Join(root, "ggcode.yaml"), []byte(ggcodeYAML), 0644); err != nil {
		t.Fatalf("write ggcode.yaml: %v", err)
	}
	gitE2E(t, root, "add", "ggcode.yaml")
	gitE2E(t, root, "commit", "--no-verify", "-m", "add ggcode config")

	bin := ggcodeBinary(t)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	// Run harness task with isolated HOME so only project-level ggcode.yaml is used
	cmd := exec.CommandContext(ctx, bin, "harness", "run", "Read the file README.md and output the text DONE")
	cmd.Dir = root
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_SYSTEM=/dev/null",
		"HOME="+t.TempDir(),
	)
	out, err := cmd.CombinedOutput()
	output := string(out)
	t.Logf("harness run output:\n%s", output)

	if err != nil {
		t.Logf("harness run returned error: %v", err)
	}

	// Should have run summary
	if !strings.Contains(output, "Harness run") {
		t.Errorf("expected 'Harness run' in output, got:\n%s", output)
	}

	// Verify task was recorded
	tasksOutput := runGGCode(t, root, "harness", "tasks")
	t.Logf("tasks after run:\n%s", tasksOutput)
	if !strings.Contains(tasksOutput, "completed") && !strings.Contains(tasksOutput, "failed") {
		t.Errorf("expected completed or failed status in tasks")
	}
}

// --- Test: BinaryRunner direct integration ---

func TestE2EBinaryRunnerRealExec(t *testing.T) {
	apiKey := os.Getenv("GGCODE_E2E_API_KEY")
	if apiKey == "" {
		apiKey = os.Getenv("ZAI_API_KEY")
	}
	if apiKey == "" {
		t.Skip("GGCODE_E2E_API_KEY or ZAI_API_KEY required")
	}

	root := t.TempDir()
	// Write project-level ggcode.yaml with vendor config
	ggcodeYAML := "vendor: e2e-test\n" +
		"vendors:\n" +
		"  e2e-test:\n" +
		"    api_key: " + apiKey + "\n" +
		"    display_name: E2E Test\n" +
		"    endpoints:\n" +
		"      default:\n" +
		"        auth_type: api_key\n" +
		"        base_url: https://open.bigmodel.cn/api/anthropic\n" +
		"        default_model: glm-5-turbo\n" +
		"        max_tokens: 512\n" +
		"        protocol: anthropic\n" +
		"endpoint: default\n" +
		"model: glm-5-turbo\n"
	if err := os.WriteFile(filepath.Join(root, "ggcode.yaml"), []byte(ggcodeYAML), 0644); err != nil {
		t.Fatalf("write ggcode.yaml: %v", err)
	}

	bin := ggcodeBinary(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, bin, "--bypass", "--prompt", "Output exactly: HELLO_E2E")
	cmd.Dir = root
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_SYSTEM=/dev/null",
		"HOME="+t.TempDir(),
	)
	out, err := cmd.CombinedOutput()
	output := strings.TrimSpace(string(out))

	t.Logf("BinaryRunner output (%v): %s", err, output)

	// Even if exit code is non-zero, we should get some output
	if output == "" {
		t.Error("expected non-empty output from ggcode --prompt")
	}
}
