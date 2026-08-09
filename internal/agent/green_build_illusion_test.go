package agent

import (
	"strings"
	"testing"
)

func TestGBIBasicDetection(t *testing.T) {
	g := newGreenBuildIllusionState()

	// Simulate: edit source file, build succeeds, declare done
	g.recordToolCall("edit_file", `{"file_path": "/src/main.go"}`, false)
	g.recordToolCall("run_command", "go build ./...", false)
	g.recordAssistantText("The implementation is complete. All done!")

	msg := g.maybeWarn()
	if msg == "" {
		t.Fatal("expected warning for green build illusion pattern")
	}
	if !strings.Contains(msg, "test") {
		t.Errorf("warning should mention running tests, got: %s", msg)
	}
}

func TestGBINoWarnWhenTestsRun(t *testing.T) {
	g := newGreenBuildIllusionState()

	g.recordToolCall("edit_file", `{"file_path": "/src/main.go"}`, false)
	g.recordToolCall("run_command", "go build ./...", false)
	g.recordToolCall("run_command", "go test ./...", false) // tests run
	g.recordAssistantText("Done!")

	msg := g.maybeWarn()
	if msg != "" {
		t.Errorf("should not warn when tests were run, got: %s", msg)
	}
}

func TestGBINoWarnWithoutBuild(t *testing.T) {
	g := newGreenBuildIllusionState()

	g.recordToolCall("edit_file", `{"file_path": "/src/main.go"}`, false)
	// No build run, no test run
	g.recordAssistantText("Done!")

	msg := g.maybeWarn()
	if msg != "" {
		t.Errorf("should not warn without build success, got: %s", msg)
	}
}

func TestGBINoWarnWithoutCompletionSignal(t *testing.T) {
	g := newGreenBuildIllusionState()

	g.recordToolCall("edit_file", `{"file_path": "/src/main.go"}`, false)
	g.recordToolCall("run_command", "go build ./...", false)
	// No completion signal
	msg := g.maybeWarn()
	if msg != "" {
		t.Errorf("should not warn without completion signal, got: %s", msg)
	}
}

func TestGBINoWarnForTestFileEdits(t *testing.T) {
	g := newGreenBuildIllusionState()

	// Editing a test file shouldn't count as source modification
	g.recordToolCall("edit_file", `{"file_path": "/src/main_test.go"}`, false)
	g.recordToolCall("run_command", "go build ./...", false)
	g.recordAssistantText("Done!")

	msg := g.maybeWarn()
	if msg != "" {
		t.Errorf("should not warn when only test files were edited, got: %s", msg)
	}
}

func TestGBINoWarnWhenBuildFails(t *testing.T) {
	g := newGreenBuildIllusionState()

	g.recordToolCall("edit_file", `{"file_path": "/src/main.go"}`, false)
	g.recordToolCall("run_command", "go build ./...", true) // build fails
	g.recordAssistantText("Done!")

	msg := g.maybeWarn()
	if msg != "" {
		t.Errorf("should not warn when build failed, got: %s", msg)
	}
}

func TestGBIFiresOnce(t *testing.T) {
	g := newGreenBuildIllusionState()

	g.recordToolCall("edit_file", `{"file_path": "/src/main.go"}`, false)
	g.recordToolCall("run_command", "go build ./...", false)
	g.recordAssistantText("Complete.")

	first := g.maybeWarn()
	second := g.maybeWarn()

	if first == "" {
		t.Fatal("expected first warning")
	}
	if second != "" {
		t.Error("should not fire more than once per run")
	}
}

func TestGBITestRunClearsModifiedFiles(t *testing.T) {
	g := newGreenBuildIllusionState()

	g.recordToolCall("edit_file", `{"file_path": "/src/main.go"}`, false)
	g.recordToolCall("run_command", "go test ./...", false) // tests run, clears state
	g.recordToolCall("edit_file", `{"file_path": "/src/main.go"}`, false)
	g.recordToolCall("run_command", "go build ./...", false)
	g.recordAssistantText("Done!")

	msg := g.maybeWarn()
	if msg == "" {
		t.Fatal("expected warning: post-test edit without re-testing")
	}
}

func TestGBIIsTestFile(t *testing.T) {
	cases := map[string]bool{
		"foo_test.go": true,
		"main.go":     false,
		"bar.test.js": true,
		"bar.spec.ts": true,
		"app.ts":      false,
		"test_foo.py": true,
		"foo.py":      false,
	}
	for path, expected := range cases {
		if got := gbiIsTestFile(path); got != expected {
			t.Errorf("gbiIsTestFile(%q) = %v, want %v", path, got, expected)
		}
	}
}

func TestGBIBuildVsTestCommands(t *testing.T) {
	buildCmds := []string{
		"go build ./...",
		"go vet ./...",
		"npm run build",
		"cargo build",
		"make build",
		"tsc",
	}
	for _, cmd := range buildCmds {
		if !isBuildOnlyCommand(cmd) {
			t.Errorf("isBuildOnlyCommand(%q) = false, want true", cmd)
		}
		if isTestCommand(cmd) {
			t.Errorf("isTestCommand(%q) = true, want false", cmd)
		}
	}

	testCmds := []string{
		"go test ./...",
		"npm test",
		"cargo test",
		"make test",
		"pytest",
		"jest",
	}
	for _, cmd := range testCmds {
		if !isTestCommand(cmd) {
			t.Errorf("isTestCommand(%q) = false, want true", cmd)
		}
	}
}

func TestGBIMultipleFiles(t *testing.T) {
	g := newGreenBuildIllusionState()

	g.recordToolCall("edit_file", `{"file_path": "/a/foo.go"}`, false)
	g.recordToolCall("edit_file", `{"file_path": "/b/bar.go"}`, false)
	g.recordToolCall("write_file", `{"path": "/c/baz.go"}`, false)
	g.recordToolCall("run_command", "go build ./...", false)
	g.recordAssistantText("All done!")

	msg := g.maybeWarn()
	if msg == "" {
		t.Fatal("expected warning for multiple modified files")
	}
}

func TestGBIReset(t *testing.T) {
	g := newGreenBuildIllusionState()
	g.recordToolCall("edit_file", `{"file_path": "/src/main.go"}`, false)
	g.recordToolCall("run_command", "go build ./...", false)
	g.recordAssistantText("Done!")
	g.maybeWarn()

	g.reset()
	if len(g.modifiedFiles) != 0 || g.fired || g.buildRunSucceeded || g.completionSignaled {
		t.Error("reset did not clear state")
	}
}
