package agent

import (
	"encoding/json"
	"testing"
)

// --- #471: stripLeadingShellComment / extractCommandFromArgs ---

func TestStripLeadingShellComment(t *testing.T) {
	cases := []struct{ in, want string }{
		{"# Run tests\ngo test ./...", "go test ./..."},
		{"# label\n# second line\nmake test", "make test"},
		{"go build ./...", "go build ./..."},
		{"#only comment", ""},
		{"  \n# x\nls -la", "ls -la"},
	}
	for _, c := range cases {
		if got := stripLeadingShellComment(c.in); got != c.want {
			t.Errorf("stripLeadingShellComment(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestExtractCommandFromArgsStripsComment(t *testing.T) {
	args := json.RawMessage(`{"command":"# Verify build\ngo build -tags goolm ./..."}`)
	if cmd := extractCommandFromArgs(args); cmd != "go build -tags goolm ./..." {
		t.Fatalf("expected stripped command, got %q", cmd)
	}
	if !isVerifyCommand(extractCommandFromArgs(args)) {
		t.Fatal("isVerifyCommand must recognize conventionally-formatted commands after stripping")
	}
}

// --- #469: isStrictVerifyCommand excludes formatters and bare task-runners ---

func TestIsStrictVerifyCommand(t *testing.T) {
	strict := []string{"go test ./...", "go build ./...", "npm test", "pytest", "cargo test"}
	for _, c := range strict {
		if !isStrictVerifyCommand(c) {
			t.Errorf("expected strict verify: %q", c)
		}
	}
	excluded := []string{"gofmt -w .", "make", "make clean", "make deploy", "just", "cargo fmt"}
	for _, c := range excluded {
		if isStrictVerifyCommand(c) {
			t.Errorf("expected NOT strict verify: %q", c)
		}
	}
}

// --- #472: convergence lock excludes pure formatters ---

func TestIsConvergenceVerifyCommandExcludesFormatters(t *testing.T) {
	if isConvergenceVerifyCommand("gofmt -w .") {
		t.Error("gofmt must not arm the convergence lock (#472)")
	}
	if isConvergenceVerifyCommand("prettier --write src/") {
		t.Error("prettier must not arm the convergence lock (#472)")
	}
	if isConvergenceVerifyCommand("cargo fmt") {
		t.Error("cargo fmt must not arm the convergence lock (#472)")
	}
	if !isConvergenceVerifyCommand("go test ./...") {
		t.Error("go test must still arm the convergence lock")
	}
	if !isConvergenceVerifyCommand("make test") {
		t.Error("make test must still arm the convergence lock")
	}
}

// --- #463: readArgsHaveWindow ---

func TestReadArgsHaveWindow(t *testing.T) {
	windowed := []json.RawMessage{
		json.RawMessage(`{"path":"a.go","offset":4000}`),
		json.RawMessage(`{"path":"a.go","limit":100}`),
		json.RawMessage(`{"files":[{"path":"a.go","offset":10},{"path":"b.go"}]}`),
	}
	for _, a := range windowed {
		if !readArgsHaveWindow(a) {
			t.Errorf("expected window detection for %s", a)
		}
	}
	full := []json.RawMessage{
		json.RawMessage(`{"path":"a.go"}`),
		json.RawMessage(`{"files":[{"path":"a.go"}]}`),
		json.RawMessage(`{}`),
	}
	for _, a := range full {
		if readArgsHaveWindow(a) {
			t.Errorf("expected NO window for %s", a)
		}
	}
}

// --- #465: serial read unknown tools are neutral (fail-open) ---

func TestSerialReadUnknownToolNeutral(t *testing.T) {
	s := newSerialReadState()
	s.recordToolCall("read_file")
	s.recordToolCall("todo_write") // unknown → neutral, must NOT set mutation
	if s.currentTurnHasMutation {
		t.Error("unknown tool (todo_write) must be neutral, not a mutation (#465)")
	}
	if s.currentTurnReadOnly != 1 {
		t.Errorf("read count = %d, want 1", s.currentTurnReadOnly)
	}
	s.recordToolCall("edit_file")
	if !s.currentTurnHasMutation {
		t.Error("known mutating tool (edit_file) must set mutation")
	}
}
