package agent

import (
	"encoding/json"
	"testing"
)

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

func TestSerialReadUnknownToolNeutral(t *testing.T) {
	s := newSerialReadState()
	s.recordToolCall("read_file")
	s.recordToolCall("todo_write")
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
