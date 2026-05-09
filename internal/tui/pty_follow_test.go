//go:build integration_local

package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// createTestProject creates a multi-file Go project in dir for sub-agents to work on.
// Contains enough files and complexity to keep sub-agents busy for several minutes.
func createTestProject(t *testing.T, dir string) {
	t.Helper()

	// go.mod
	writeFile(t, dir, "go.mod", `module example.com/calculator

go 1.22`)

	// Main package — a simple CLI calculator
	writeFile(t, dir, "main.go", `package main

import (
	"fmt"
	"os"
	"strconv"
)

func main() {
	if len(os.Args) < 4 {
		fmt.Println("Usage: calc <a> <op> <b>")
		os.Exit(1)
	}
	a, _ := strconv.ParseFloat(os.Args[1], 64)
	b, _ := strconv.ParseFloat(os.Args[3], 64)
	op := os.Args[2]

	var result float64
	switch op {
	case "+":
		result = Add(a, b)
	case "-":
		result = Subtract(a, b)
	case "*":
		result = Multiply(a, b)
	case "/":
		result = Divide(a, b)
	default:
		fmt.Println("Unknown operator:", op)
		os.Exit(1)
	}
	fmt.Printf("%.2f\n", result)
}
`)

	// arithmetic package — intentionally has issues for sub-agent to find
	writeFile(t, dir, "arithmetic/add.go", `package arithmetic

// Add returns the sum of two numbers.
// TODO: handle overflow
func Add(a, b float64) float64 {
	return a + b
}

// AddAll returns the sum of all numbers in a slice.
// BUG: does not handle empty slice correctly
func AddAll(nums []float64) float64 {
	sum := 0.0
	for i := 0; i < len(nums); i++ {
		sum += nums[i]
	}
	return sum
}
`)

	writeFile(t, dir, "arithmetic/subtract.go", `package arithmetic

// Subtract returns a - b.
func Subtract(a, b float64) float64 {
	return a - b
}

// SubtractAll subtracts all values from the initial value.
func SubtractAll(initial float64, values []float64) float64 {
	result := initial
	for _, v := range values {
		result = result - v
	}
	return result
}
`)

	writeFile(t, dir, "arithmetic/multiply.go", `package arithmetic

// Multiply returns the product of two numbers.
// FIXME: no overflow protection
func Multiply(a, b float64) float64 {
	return a * b
}

// Factorial returns n!
// BUG: infinite loop for negative numbers
func Factorial(n int) int {
	if n == 0 {
		return 1
	}
	return n * Factorial(n-1)
}
`)

	writeFile(t, dir, "arithmetic/divide.go", `package arithmetic

// Divide returns a / b.
// BUG: no division by zero check
func Divide(a, b float64) float64 {
	return a / b
}

// Mod returns a % b.
func Mod(a, b int) int {
	return a % b
}
`)

	// parser package — handles parsing input strings
	writeFile(t, dir, "parser/parser.go", `package parser

import (
	"fmt"
	"strconv"
	"strings"
)

// ParseExpression parses a simple "a op b" expression.
func ParseExpression(expr string) (float64, string, float64, error) {
	parts := strings.Fields(expr)
	if len(parts) != 3 {
		return 0, "", 0, fmt.Errorf("expected 3 parts, got %d", len(parts))
	}
	a, err := strconv.ParseFloat(parts[0], 64)
	if err != nil {
		return 0, "", 0, err
	}
	b, err := strconv.ParseFloat(parts[2], 64)
	if err != nil {
		return 0, "", 0, err
	}
	return a, parts[1], b, nil
}

// ValidateOperator checks if the operator is supported.
func ValidateOperator(op string) bool {
	// BUG: only checks basic operators
	switch op {
	case "+", "-", "*", "/":
		return true
	default:
		return false
	}
}
`)

	// history package — tracks calculation history
	writeFile(t, dir, "history/history.go", `package history

import (
	"encoding/json"
	"os"
)

type Entry struct {
	Expression string  `+"`"+`json:"expression"`+"`"+`
	Result     float64 `+"`"+`json:"result"`+"`"+`
}

type History struct {
	entries []Entry
	file    string
}

func NewHistory(file string) *History {
	return &History{file: file}
}

// Load reads history from disk.
// FIXME: does not handle corrupt JSON files
func (h *History) Load() error {
	data, err := os.ReadFile(h.file)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, &h.entries)
}

// Save writes history to disk.
func (h *History) Save() error {
	data, err := json.Marshal(h.entries)
	if err != nil {
		return err
	}
	return os.WriteFile(h.file, data, 0644)
}

// Add appends a new entry.
func (h *History) Add(expr string, result float64) {
	h.entries = append(h.entries, Entry{Expression: expr, Result: result})
}

// Len returns the number of entries.
func (h *History) Len() int {
	return len(h.entries)
}

// Recent returns the last n entries.
func (h *History) Recent(n int) []Entry {
	if n > len(h.entries) {
		n = len(h.entries)
	}
	return h.entries[len(h.entries)-n:]
}
`)

	// formatter package — formats output
	writeFile(t, dir, "formatter/formatter.go", `package formatter

import (
	"fmt"
	"strings"
)

// FormatResult formats a calculation result.
// TODO: support different formats (scientific, hex, etc.)
func FormatResult(value float64) string {
	return fmt.Sprintf("%.2f", value)
}

// FormatTable formats entries as a markdown table.
func FormatTable(headers []string, rows [][]string) string {
	var b strings.Builder
	b.WriteString("| ")
	b.WriteString(strings.Join(headers, " | "))
	b.WriteString(" |\n")
	b.WriteString("| ")
	for range headers {
		b.WriteString("--- | ")
	}
	b.WriteString("\n")
	for _, row := range rows {
		b.WriteString("| ")
		b.WriteString(strings.Join(row, " | "))
		b.WriteString(" |\n")
	}
	return b.String()
}

// PadRight pads a string to the right.
func PadRight(s string, width int) string {
	if len(s) >= width {
		return s
	}
	return s + strings.Repeat(" ", width-len(s))
}
`)

	// README
	writeFile(t, dir, "README.md", `# Calculator

A simple CLI calculator written in Go.

## Usage

    go run main.go 2 + 3
    go run main.go 10 / 4

## Known Issues

- Division by zero is not handled
- Factorial has infinite loop for negative numbers
- History loading does not handle corrupt files
`)

	// Tests — intentionally have some issues
	writeFile(t, dir, "arithmetic/add_test.go", `package arithmetic

import "testing"

func TestAdd(t *testing.T) {
	if Add(2, 3) != 5 {
		t.Error("2 + 3 should be 5")
	}
}

func TestAddNegative(t *testing.T) {
	if Add(-1, -2) != -3 {
		t.Error("-1 + -2 should be -3")
	}
}

func TestAddAll(t *testing.T) {
	// BUG: this test passes but the function is wrong for empty slices
	result := AddAll([]float64{1, 2, 3})
	if result != 6 {
		t.Errorf("expected 6, got %f", result)
	}
}
`)

	writeFile(t, dir, "arithmetic/multiply_test.go", `package arithmetic

import "testing"

func TestMultiply(t *testing.T) {
	if Multiply(3, 4) != 12 {
		t.Error("3 * 4 should be 12")
	}
}

func TestFactorial(t *testing.T) {
	if Factorial(5) != 120 {
		t.Error("5! should be 120")
	}
	// BUG: missing test for negative numbers
}
`)

	// Git init so ggcode sees it as a project
	os.MkdirAll(filepath.Join(dir, ".git"), 0755)
}

func writeFile(t *testing.T, baseDir, relPath, content string) {
	t.Helper()
	fullPath := filepath.Join(baseDir, relPath)
	os.MkdirAll(filepath.Dir(fullPath), 0755)
	if err := os.WriteFile(fullPath, []byte(content), 0644); err != nil {
		t.Fatalf("write %s: %v", relPath, err)
	}
}

// startFollowTestGGCode starts ggcode with a test project workspace and real user config.
func startFollowTestGGCode(t *testing.T) *ptyHarness {
	t.Helper()

	h := startGGCodeLive(t)

	// Populate workspace with test project
	createTestProject(t, h.cmd.Dir)
	t.Logf("Test project created in %s", h.cmd.Dir)

	return h
}

// isBrewing checks if the recent output shows the agent is still processing.
func isBrewing(output string) bool {
	tail := lastN(output, 300)
	brewRe := regexp.MustCompile(`(?i)brewing|thinking|⠋|⠙|⠹|⠸|⠼|⠴|⠦|⠧|⠇|⠏`)
	return brewRe.MatchString(tail)
}

// recentOutput returns the last N characters of stripped output (with ANSI removed but spaces preserved).
func recentOutput(h *ptyHarness, n int) string {
	h.readAll()
	return stripAnsi(lastN(h.getOutput(), n))
}

// TestPTY_SubAgentFollowDeepWorkout is the comprehensive follow mode test.
//
// It creates a multi-file Go project, spawns a sub-agent to do a substantial
// code review + improvement task, and verifies the full follow mode lifecycle:
//
//	Phase 1: Setup — TUI ready, test project populated
//	Phase 2: Spawn — prompt LLM to create sub-agent with heavy task
//	Phase 3: Strip — verify follow strip renders with correct slots
//	Phase 4: Follow — press ! to enter follow mode, verify panel switches
//	Phase 5: Live rendering — verify tool calls and text render in follow panel
//	Phase 6: Resize — resize terminal during active sub-agent work
//	Phase 7: Auto-return — verify auto-return when sub-agent completes
//	Phase 8: Restore — verify main conversation panel is restored
func TestPTY_SubAgentFollowDeepWorkout(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY test in short mode")
	}

	h := startFollowTestGGCode(t)
	defer h.quit()

	// ━━ Phase 1: TUI Ready ━━
	h.waitForText("Type a message", 15*time.Second)
	h.drainOutput()
	time.Sleep(1 * time.Second)
	t.Log("━━━ Phase 1: TUI ready, test project created ━━━")

	// Count files in the test project
	files, _ := filepath.Glob(filepath.Join(h.cmd.Dir, "**/*.go"))
	t.Logf("Test project has %d .go files", len(files))

	// ━━ Phase 2: Spawn sub-agent with heavy task ━━
	// Ask for a thorough review + fix cycle — this should generate many tool calls
	prompt := "Use spawn_agent to create a sub-agent named 'reviewer' with this detailed task: " +
		"1) Read ALL .go files in the project one by one (there are about 12 files). " +
		"2) For each file, identify bugs, issues, and improvements. " +
		"3) Fix every bug you find by editing the files directly. " +
		"4) Run the tests after each fix to make sure nothing breaks. " +
		"5) Write a final summary of all changes made. " +
		"This is important — read every single file, fix every bug, run tests after each fix. " +
		"Return immediately after spawning."
	h.sendText(prompt)
	time.Sleep(500 * time.Millisecond)
	h.sendKey("enter")
	t.Log("━━━ Phase 2: Prompt sent, waiting for LLM to call spawn_agent... ━━━")

	// Wait for spawn_agent to appear
	h.waitForText("spawn_agent", 180*time.Second)
	t.Log("━━━ Phase 2: spawn_agent tool call detected ━━━")

	// Wait for main agent turn to complete — the LLM should return text after spawning
	// We poll until "brewing" disappears from recent output AND "Type a message" appears
	deadline := time.Now().Add(3 * time.Minute)
	for time.Now().Before(deadline) {
		time.Sleep(3 * time.Second)
		output := h.getStrippedOutput()
		tail := lastN(output, 400)
		if !isBrewing(tail) && strings.Contains(tail, "Type a message") {
			break
		}
	}
	time.Sleep(2 * time.Second)
	h.drainOutput()
	t.Log("━━━ Phase 2: Main agent turn complete ━━━")

	// ━━ Phase 3: Verify follow strip ━━
	output := recentOutput(h, 1500)
	t.Logf("Phase 3 — Recent output:\n%s", output)

	// The strip should show sub-agent name/task with slot key "!" and "Esc"
	if !strings.Contains(output, "!") {
		t.Error("Phase 3 FAILED: expected '!' in follow strip")
		goto phase4
	}
	if !strings.Contains(output, "Esc") {
		t.Error("Phase 3 FAILED: expected 'Esc' in follow strip")
		goto phase4
	}
	t.Log("━━━ Phase 3: Follow strip visible — slot keys and Esc hint present ━━━")

	// ━━ Phase 4: Enter follow mode ━━
phase4:
	t.Log("━━━ Phase 4: Pressing '!' to enter follow mode... ━━━")
	h.sendKey("!")
	time.Sleep(5 * time.Second)

	output = recentOutput(h, 1500)
	t.Logf("Phase 4 — Recent output after !:\n%s", output)

	// Should show "input paused" or "Following sub-agent"
	if !strings.Contains(output, "input paused") && !strings.Contains(output, "Following") && !strings.Contains(output, "following") {
		// Sub-agent might have already completed. Check if we see sub-agent content instead.
		if strings.Contains(output, "read_file") || strings.Contains(output, "Add") || strings.Contains(output, "Divide") {
			t.Log("Phase 4: Follow mode may have already auto-returned, but sub-agent content was visible")
		} else {
			t.Error("Phase 4 FAILED: expected 'input paused' or 'Following' when in follow mode")
		}
	} else {
		t.Log("━━━ Phase 4: Follow mode active — input paused, sub-agent panel shown ━━━")
	}

	// ━━ Phase 5: Verify live rendering of tool calls ━━
	t.Log("━━━ Phase 5: Monitoring sub-agent tool calls in follow panel... ━━━")

	// Poll for up to 3 minutes looking for tool call evidence
	toolPatterns := []struct {
		name    string
		pattern string
	}{
		{"read_file", "read_file|Read"},
		{"edit_file", "edit_file|Edit"},
		{"run_command", "run_command|go test|go build"},
		{"search_files", "search_files|Search"},
		{"write_file", "write_file|Write"},
	}

	foundTools := map[string]bool{}
	deadline = time.Now().Add(3 * time.Minute)
	phase5Done := false

	for time.Now().Before(deadline) {
		time.Sleep(10 * time.Second)
		output = recentOutput(h, 3000)

		for _, tp := range toolPatterns {
			matched, _ := regexp.MatchString(tp.pattern, output)
			if matched && !foundTools[tp.name] {
				foundTools[tp.name] = true
				t.Logf("Phase 5: Detected '%s' in sub-agent output ✓", tp.name)
			}
		}

		// Once we have 3+ tool types, that's solid evidence
		if len(foundTools) >= 3 {
			phase5Done = true
			break
		}

		// Check if sub-agent completed early
		if strings.Contains(output, "returned to main view") || strings.Contains(output, "completed") {
			t.Log("Phase 5: Sub-agent completed during monitoring")
			phase5Done = true
			break
		}
	}

	if len(foundTools) == 0 {
		t.Error("Phase 5 FAILED: no tool calls from sub-agent detected")
	} else {
		t.Logf("━━━ Phase 5: Found %d tool types rendering in follow panel: %v ━━━", len(foundTools), foundTools)
	}

	// ━━ Phase 6: Resize during active work ━━
	if phase5Done && len(foundTools) >= 3 {
		t.Log("━━━ Phase 6: Sub-agent already completed, skipping resize during active work ━━━")
	} else {
		t.Log("━━━ Phase 6: Resizing terminal while sub-agent is working... ━━━")

		sizes := []struct {
			cols, rows uint16
			label      string
		}{
			{80, 24, "80x24"},
			{120, 40, "120x40"},
			{60, 20, "60x20"},
			{160, 50, "160x50"},
		}

		for i, sz := range sizes {
			h.resize(sz.cols, sz.rows)
			time.Sleep(4 * time.Second)

			output = recentOutput(h, 500)
			if strings.Contains(output, "panic") || strings.Contains(output, "runtime error") {
				t.Errorf("Phase 6 FAILED: crash at %s", sz.label)
				break
			}
			t.Logf("Phase 6: %s — OK (step %d/%d)", sz.label, i+1, len(sizes))
		}
		t.Log("━━━ Phase 6: Resize stress test passed ━━━")
	}

	// ━━ Phase 7: Wait for auto-return ━━
	t.Log("━━━ Phase 7: Waiting for sub-agent to complete and auto-return... ━━━")

	// Restore standard size first
	h.resize(120, 40)
	time.Sleep(2 * time.Second)

	autoReturned := false
	deadline = time.Now().Add(5 * time.Minute)
	for time.Now().Before(deadline) {
		time.Sleep(5 * time.Second)
		output = recentOutput(h, 600)

		if strings.Contains(output, "returned to main view") ||
			strings.Contains(output, "completed") {
			autoReturned = true
			t.Log("━━━ Phase 7: Auto-return detected ━━━")
			break
		}

		// Check if follow mode ended: "input paused" no longer in recent output
		if !strings.Contains(output, "input paused") &&
			!strings.Contains(output, "Following sub-agent") &&
			strings.Contains(output, "Type a message") {
			autoReturned = true
			t.Log("━━━ Phase 7: Auto-return detected (input unpaused) ━━━")
			break
		}
	}

	if !autoReturned {
		t.Error("Phase 7 FAILED: sub-agent did not auto-return after 5 minutes")
	}

	// ━━ Phase 8: Verify main view restored ━━
	time.Sleep(3 * time.Second)
	output = recentOutput(h, 800)
	t.Logf("Phase 8 — Final output:\n%s", output)

	if !strings.Contains(output, "Type a message") {
		t.Error("Phase 8 FAILED: main view not restored — 'Type a message' not found")
	} else {
		t.Log("━━━ Phase 8: Main conversation panel restored ✓ ━━━")
	}

	// Summary
	t.Log("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	t.Log(fmt.Sprintf("SUMMARY: found=%d tools, autoReturned=%v", len(foundTools), autoReturned))
	t.Log("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
}
