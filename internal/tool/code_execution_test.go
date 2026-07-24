package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestCodeExecution_BasicConsoleLog verifies that simple JS code runs and
// returns console.log output.
func TestCodeExecution_BasicConsoleLog(t *testing.T) {
	reg := NewRegistry()
	ce := CodeExecution{Registry: reg}

	result, err := ce.Execute(context.Background(), json.RawMessage(`{"code": "console.log('hello world'); console.log(1 + 2);"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error result: %s", result.Content)
	}
	if !strings.Contains(result.Content, "hello world") {
		t.Errorf("expected 'hello world' in output, got: %s", result.Content)
	}
	if !strings.Contains(result.Content, "3") {
		t.Errorf("expected '3' in output, got: %s", result.Content)
	}
}

// TestCodeExecution_EmptyCode verifies empty code returns an error.
func TestCodeExecution_EmptyCode(t *testing.T) {
	reg := NewRegistry()
	ce := CodeExecution{Registry: reg}

	result, _ := ce.Execute(context.Background(), json.RawMessage(`{"code": ""}`))
	if !result.IsError {
		t.Error("expected error for empty code")
	}
}

// TestCodeExecution_NoOutput verifies graceful handling when no console.log.
func TestCodeExecution_NoOutput(t *testing.T) {
	reg := NewRegistry()
	ce := CodeExecution{Registry: reg}

	result, _ := ce.Execute(context.Background(), json.RawMessage(`{"code": "var x = 1 + 2;"}`))
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content)
	}
	if !strings.Contains(result.Content, "no console.log") {
		t.Errorf("expected no-output message, got: %s", result.Content)
	}
}

// TestCodeExecution_ToolCall verifies that a tool is callable from JS.
func TestCodeExecution_ToolCall(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.txt")
	os.WriteFile(testFile, []byte("hello from test file\nline 2"), 0644)

	reg := NewRegistry()
	reg.Register(ReadFile{})
	ce := CodeExecution{Registry: reg}

	// Use string concatenation (ES5 compatible) instead of template literals.
	code := fmt.Sprintf(
		"var r = await tools.read_file({path: %q});\n"+
			"var lines = r.split(\"\\n\");\n"+
			"console.log(\"Lines: \" + lines.length);\n"+
			"console.log(\"First: \" + lines[0]);\n",
		testFile,
	)

	result, err := ce.Execute(context.Background(), json.RawMessage(
		fmt.Sprintf(`{"code": %q}`, code),
	))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("tool execution error: %s", result.Content)
	}
	if !strings.Contains(result.Content, "Lines: 3") { // 2 content lines + trailing empty
		t.Errorf("expected 'Lines: 3', got: %s", result.Content)
	}
	if !strings.Contains(result.Content, "First:") {
		t.Errorf("expected 'First:' in output, got: %s", result.Content)
	}
	if !strings.Contains(result.Content, "[tool calls made by code: read_file]") {
		t.Errorf("expected tool call log, got: %s", result.Content)
	}
}

// TestCodeExecution_MultiToolBatch verifies multiple tool calls in one script.
func TestCodeExecution_MultiToolBatch(t *testing.T) {
	tmpDir := t.TempDir()
	os.WriteFile(filepath.Join(tmpDir, "a.txt"), []byte("content A"), 0644)
	os.WriteFile(filepath.Join(tmpDir, "b.txt"), []byte("content B"), 0644)

	reg := NewRegistry()
	reg.Register(ReadFile{})
	reg.Register(ListDir{})
	ce := CodeExecution{Registry: reg}

	code := fmt.Sprintf(
		"var listing = await tools.list_directory({path: %q});\n"+
			"var lines = listing.trim().split(\"\\n\");\n"+
			"console.log(\"Entries: \" + lines.length);\n"+
			"for (var i = 0; i < lines.length; i++) {\n"+
			"    var line = lines[i];\n"+
			"    if (line.indexOf(\".txt\") > -1) {\n"+
			"        var name = line.split(\"  \")[0];\n"+
			"        var content = await tools.read_file({path: %q + \"/\" + name});\n"+
			"        console.log(name + \": \" + content.split(\"\\n\")[0]);\n"+
			"    }\n"+
			"}\n",
		tmpDir, tmpDir,
	)

	result, err := ce.Execute(context.Background(), json.RawMessage(
		fmt.Sprintf(`{"code": %q}`, code),
	))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("execution error: %s", result.Content)
	}
	if !strings.Contains(result.Content, "a.txt") || !strings.Contains(result.Content, "b.txt") {
		t.Errorf("expected both files in output, got: %s", result.Content)
	}
}

// TestCodeExecution_SecurityNoFS verifies the sandbox cannot access Node.js APIs.
func TestCodeExecution_SecurityNoFS(t *testing.T) {
	reg := NewRegistry()
	ce := CodeExecution{Registry: reg}

	// require is not defined in goja — calling it should throw a ReferenceError.
	// goja does NOT treat this as a syntax error; it throws at runtime.
	result, err := ce.Execute(context.Background(), json.RawMessage(
		fmt.Sprintf(`{"code": %q}`, "require('fs');"),
	))
	// Should produce an error (ReferenceError: require is not defined).
	if err == nil && !result.IsError {
		t.Error("expected error for require('fs'), but got none")
	}
}

// TestCodeExecution_JSControlFlow verifies loops and conditionals work.
func TestCodeExecution_JSControlFlow(t *testing.T) {
	reg := NewRegistry()
	ce := CodeExecution{Registry: reg}

	code := "var sum = 0;\n" +
		"for (var i = 1; i <= 10; i++) { sum += i; }\n" +
		"console.log(\"Sum 1-10: \" + sum);\n" +
		"var items = [\"apple\", \"banana\", \"cherry\"];\n" +
		"var filtered = items.filter(function(x) { return x.charAt(0) === \"b\"; });\n" +
		"console.log(\"Filtered: \" + filtered.join(\", \"));\n" +
		"if (sum > 50) { console.log(\"Sum is large\"); } else { console.log(\"Sum is small\"); }\n"

	result, err := ce.Execute(context.Background(), json.RawMessage(
		fmt.Sprintf(`{"code": %q}`, code),
	))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("execution error: %s", result.Content)
	}
	if !strings.Contains(result.Content, "Sum 1-10: 55") {
		t.Errorf("expected sum 55, got: %s", result.Content)
	}
	if !strings.Contains(result.Content, "Filtered: banana") {
		t.Errorf("expected banana, got: %s", result.Content)
	}
	if !strings.Contains(result.Content, "Sum is large") {
		t.Errorf("expected 'Sum is large', got: %s", result.Content)
	}
}

// TestCodeExecution_Timeout verifies that long-running code is interrupted.
func TestCodeExecution_Timeout(t *testing.T) {
	reg := NewRegistry()
	ce := CodeExecution{Registry: reg}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	done := make(chan struct{})
	go func() {
		result, _ := ce.Execute(ctx, json.RawMessage(
			fmt.Sprintf(`{"code": %q}`, "while(true) {}"),
		))
		if !result.IsError {
			t.Error("expected error for infinite loop")
		}
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("timeout: execution did not return after 10s — interrupt not working")
	}
}

// TestCodeExecution_ErrorHandling verifies that JS errors are captured.
func TestCodeExecution_ErrorHandling(t *testing.T) {
	reg := NewRegistry()
	ce := CodeExecution{Registry: reg}

	result, err := ce.Execute(context.Background(), json.RawMessage(
		fmt.Sprintf(`{"code": %q}`, "nonExistentVar.property;"),
	))
	if err == nil && !result.IsError {
		t.Error("expected error for referencing undefined variable")
	}
}

// TestCodeExecution_StdoutTruncation verifies stdout is capped.
func TestCodeExecution_StdoutTruncation(t *testing.T) {
	reg := NewRegistry()
	ce := CodeExecution{Registry: reg}

	code := "var s = \"\";\n" +
		"for (var i = 0; i < 10000; i++) { s += \"x\" + \"\\n\"; }\n" +
		"console.log(s);\n"

	result, _ := ce.Execute(context.Background(), json.RawMessage(
		fmt.Sprintf(`{"code": %q}`, code),
	))
	if len(result.Content) > maxStdoutLen+1000 {
		t.Errorf("output too long: %d bytes (expected ~%d)", len(result.Content), maxStdoutLen)
	}
}

// TestCodeExecution_WriteToolsExcluded verifies write tools are not available.
func TestCodeExecution_WriteToolsExcluded(t *testing.T) {
	reg := NewRegistry()
	reg.Register(WriteFile{})
	ce := CodeExecution{Registry: reg}

	code := "try {\n" +
		"    await tools.write_file({path: \"/tmp/pwned\", content: \"hacked\"});\n" +
		"    console.log(\"ERROR: write_file was accessible!\");\n" +
		"} catch(e) {\n" +
		"    console.log(\"Good: write_file not accessible: \" + e);\n" +
		"}\n"

	result, err := ce.Execute(context.Background(), json.RawMessage(
		fmt.Sprintf(`{"code": %q}`, code),
	))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("execution error: %s", result.Content)
	}
	if !strings.Contains(result.Content, "not accessible") {
		t.Errorf("expected write_file to be inaccessible, got: %s", result.Content)
	}
}
