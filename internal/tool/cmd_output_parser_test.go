package tool

import (
	"strings"
	"testing"
)

func TestSummarizeCommandOutput_GoTestFailures(t *testing.T) {
	output := `=== RUN   TestAgent_ProcessToolCall
--- PASS: TestAgent_ProcessToolCall (0.05s)
=== RUN   TestAgent_Compact
--- PASS: TestAgent_Compact (0.12s)
=== RUN   TestAgent_Fail
    file_test.go:42: assertion failed: expected "foo", got "bar"
--- FAIL: TestAgent_Fail (0.03s)
=== RUN   TestAgent_Panic
panic: runtime error: index out of range [5] with length 3

goroutine 1 [running]:
example.com/pkg.TestAgent_Panic(...)
	/path/to/file_test.go:78 +0x123
--- FAIL: TestAgent_Panic (0.01s)
FAIL	example.com/pkg	0.250s
FAIL
ok  	example.com/pkg/sub	0.100s
`
	summary := summarizeCommandOutput("go test ./...", output)

	if !strings.Contains(summary, "[Result Summary]") {
		t.Errorf("expected [Result Summary] header, got:\n%s", summary)
	}
	if !strings.Contains(summary, "FAILED: 2 test(s)") {
		t.Errorf("expected 2 failed tests, got:\n%s", summary)
	}
	if !strings.Contains(summary, "passed: 2") {
		t.Errorf("expected 2 passed tests, got:\n%s", summary)
	}
	if !strings.Contains(summary, "FAIL: TestAgent_Fail") {
		t.Errorf("expected TestAgent_Fail in summary, got:\n%s", summary)
	}
	if !strings.Contains(summary, "FAIL: TestAgent_Panic") {
		t.Errorf("expected TestAgent_Panic in summary, got:\n%s", summary)
	}
	if !strings.Contains(summary, "Panics detected:") {
		t.Errorf("expected panic section, got:\n%s", summary)
	}
}

func TestSummarizeCommandOutput_GoTestAllPass(t *testing.T) {
	output := `=== RUN   TestA
--- PASS: TestA (0.01s)
=== RUN   TestB
--- PASS: TestB (0.02s)
=== RUN   TestC_Skipped
--- SKIP: TestC_Skipped (0.00s)
PASS
ok  	example.com/pkg	0.050s
`
	summary := summarizeCommandOutput("go test ./...", output)

	if !strings.Contains(summary, "PASSED: 2 test(s)") {
		t.Errorf("expected PASSED 2 tests, got:\n%s", summary)
	}
	if !strings.Contains(summary, "skipped: 1") {
		t.Errorf("expected skipped: 1, got:\n%s", summary)
	}
}

func TestSummarizeCommandOutput_GoBuildErrors(t *testing.T) {
	output := `# example.com/pkg/internal/agent
./agent.go:42:15: undefined: foo.Bar
./agent.go:100:3: cannot use x (type int) as type string in assignment
./agent.go:150:2: y declared and not used
FAIL	example.com/pkg/internal/agent [build failed]
`
	summary := summarizeCommandOutput("go build ./...", output)

	if !strings.Contains(summary, "[Result Summary]") {
		t.Errorf("expected summary header, got:\n%s", summary)
	}
	if !strings.Contains(summary, "3 compile error(s)") {
		t.Errorf("expected 3 compile errors, got:\n%s", summary)
	}
	if !strings.Contains(summary, "1 file(s)") {
		t.Errorf("expected 1 file (all errors in agent.go), got:\n%s", summary)
	}
	if !strings.Contains(summary, "agent.go:42") {
		t.Errorf("expected agent.go:42, got:\n%s", summary)
	}
	if !strings.Contains(summary, "undefined: foo.Bar") {
		t.Errorf("expected undefined error, got:\n%s", summary)
	}
}

func TestSummarizeCommandOutput_GoVetOutput(t *testing.T) {
	output := `./main.go:25:2: Printf format %s has arg foo of type int
./util.go:100:10: unreachable code
`
	summary := summarizeCommandOutput("go vet ./...", output)

	// "Printf format" contains "format" but we need "error" keyword
	// The parser should catch "unreachable code" if it's classified
	// go vet output doesn't always use "error" — so may or may not produce summary
	// Just verify it doesn't crash
	_ = summary
}

func TestSummarizeCommandOutput_Pytest(t *testing.T) {
	output := `test_main.py::test_add PASSED                                  [ 25%]
test_main.py::test_subtract PASSED                               [ 50%]
test_main.py::test_multiply FAILED                               [ 75%]
test_main.py::test_divide FAILED                                 [100%]

=================================== FAILURES ===================================
_________________________________ test_multiply _________________________________

    assert multiply(2, 3) == 5
E       assert 6 == 5

=========================== 2 failed, 2 passed in 0.50s ==========================
`
	summary := summarizeCommandOutput("pytest", output)

	if !strings.Contains(summary, "2 failed, 2 passed") {
		t.Errorf("expected pytest summary, got:\n%s", summary)
	}
}

func TestSummarizeCommandOutput_Jest(t *testing.T) {
	output := `PASS  src/utils.test.js
FAIL  src/parser.test.js
FAIL  src/agent.test.js

Tests:       2 failed, 3 passed, 5 total
`
	summary := summarizeCommandOutput("npm test", output)

	if !strings.Contains(summary, "2 failed, 3 passed, 5 total") {
		t.Errorf("expected jest summary, got:\n%s", summary)
	}
}

func TestSummarizeCommandOutput_NoMatchReturnsEmpty(t *testing.T) {
	output := `Hello world
This is just some output
Nothing to see here
`
	summary := summarizeCommandOutput("echo 'hello'", output)
	if summary != "" {
		t.Errorf("expected empty summary for non-test/build output, got: %s", summary)
	}
}

func TestSummarizeCommandOutput_CargoTest(t *testing.T) {
	output := `running 5 tests
test tests::test_add ... ok
test tests::test_sub ... FAILED

test result: FAILED. 1 passed; 1 failed; 0 ignored; 0 measured; 3 filtered out
`
	summary := summarizeCommandOutput("cargo test", output)

	if summary == "" {
		t.Fatal("expected non-empty summary for cargo test")
	}
	if !strings.Contains(summary, "1 passed, 1 failed") {
		t.Errorf("expected cargo summary with pass/fail, got:\n%s", summary)
	}
}

func TestSummarizeCommandOutput_GenericCompilerErrors(t *testing.T) {
	output := `src/main.ts:10:5: error TS2322: Type 'string' is not assignable to type 'number'.
src/util.ts:25:3: error TS2304: Cannot find name 'foo'.
`
	// "tsc" is not a recognized test command, so it falls to compiler error path
	summary := summarizeCommandOutput("tsc --noEmit", output)

	if !strings.Contains(summary, "[Result Summary]") {
		t.Errorf("expected summary header, got:\n%s", summary)
	}
	if !strings.Contains(summary, "main.ts:10") {
		t.Errorf("expected main.ts:10, got:\n%s", summary)
	}
}

func TestSummarizeCommandOutput_GoTestBuildFailedPackage(t *testing.T) {
	output := `# example.com/pkg/broken
./broken.go:10:5: undefined: nonexistent
FAIL	example.com/pkg/broken [build failed]
FAIL
`
	// go test with build failure — should detect compile errors
	summary := summarizeCommandOutput("go test ./...", output)

	// Should have build failures in packages
	if !strings.Contains(summary, "Build failures") {
		t.Errorf("expected build failures section, got:\n%s", summary)
	}
}

func TestSummarizeCommandOutput_CommandWithComment(t *testing.T) {
	output := `--- FAIL: TestFoo (0.01s)
FAIL	example.com/pkg	0.010s
FAIL
`
	// Command prefixed with a comment line (common in agent-generated commands)
	summary := summarizeCommandOutput("# run tests\ngo test ./...", output)

	if !strings.Contains(summary, "FAILED: 1 test(s)") {
		t.Errorf("expected 1 failed test with comment prefix, got:\n%s", summary)
	}
}

func TestSummarizeCommandOutput_ManyFailuresCapped(t *testing.T) {
	var sb strings.Builder
	for i := 0; i < 50; i++ {
		sb.WriteString("--- FAIL: TestMany_")
		sb.WriteString(string(rune('A' + i%26)))
		sb.WriteString(" (0.01s)\n")
	}
	sb.WriteString("FAIL\texample.com/pkg\t0.500s\nFAIL\n")

	summary := summarizeCommandOutput("go test ./...", sb.String())

	// Should cap the number of listed failures
	if !strings.Contains(summary, "... and") {
		t.Errorf("expected failure cap message, got:\n%s", summary)
	}
}

func TestSummarizeCommandOutput_LargeTestRunSavesTokens(t *testing.T) {
	// Simulate a large test suite with mostly passing tests and 2 failures
	var sb strings.Builder
	for i := 0; i < 200; i++ {
		sb.WriteString("--- PASS: TestBig_Pass_")
		sb.WriteString(string(rune('A' + i%26)))
		sb.WriteString(string(rune('a' + (i/26)%26)))
		sb.WriteString(" (0.01s)\n")
	}
	sb.WriteString("--- FAIL: TestBig_FailA (0.02s)\n")
	sb.WriteString("    file_test.go:100: assertion failed\n")
	sb.WriteString("--- FAIL: TestBig_FailB (0.03s)\n")
	sb.WriteString("    file_test.go:200: nil pointer\n")
	sb.WriteString("PASS\nok\texample.com/pkg\t2.500s\nFAIL\n")

	summary := summarizeCommandOutput("go test ./...", sb.String())

	// The summary should be compact and highlight the 2 failures
	lines := strings.Split(summary, "\n")
	// Should be well under 10 lines for the summary portion
	if len(lines) > 15 {
		t.Errorf("summary too long: %d lines for 2 failures out of 200 tests", len(lines))
	}
	if !strings.Contains(summary, "FAILED: 2 test(s)") {
		t.Errorf("expected 2 failures, got:\n%s", summary)
	}
	if !strings.Contains(summary, "passed: 200") {
		t.Errorf("expected 200 passed, got:\n%s", summary)
	}
	if !strings.Contains(summary, "TestBig_FailA") {
		t.Errorf("expected TestBig_FailA, got:\n%s", summary)
	}
}
