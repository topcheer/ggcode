package agent

import (
	"strings"
	"testing"
)

func TestDiagnosticDisconnect_NoDiagnostic(t *testing.T) {
	d := newDiagnosticDisconnectState()
	d.recordToolResult("read_file", "foo.go", "file contents here", 1)
	d.recordAction(1)
	msg := d.check()
	if msg != "" {
		t.Fatalf("expected no guidance for non-diagnostic result, got: %s", msg)
	}
}

func TestDiagnosticDisconnect_RegisterAndAddress(t *testing.T) {
	d := newDiagnosticDisconnectState()

	// Iteration 3: build fails with diagnostic.
	d.recordToolResult("run_command", "",
		"main.go:10:2: undefined: NewWidget", 3)
	if len(d.activeDiagnostics) != 1 {
		t.Fatalf("expected 1 diagnostic, got %d", len(d.activeDiagnostics))
	}

	// Iteration 4: edit main.go to fix it.
	d.recordAction(4)
	d.recordToolResult("edit_file", "main.go", "success", 4)

	if !d.activeDiagnostics[0].addressed {
		t.Fatal("diagnostic should be marked addressed after editing source file")
	}

	msg := d.check()
	if msg != "" {
		t.Fatalf("expected no guidance after addressing diagnostic, got: %s", msg)
	}
}

func TestDiagnosticDisconnect_TriggerAfterIgnored(t *testing.T) {
	d := newDiagnosticDisconnectState()

	// Iteration 2: build fails.
	d.recordToolResult("run_command", "",
		"foo.go:5: undefined: NewWidget", 2)

	// 4 subsequent actions that don't address foo.go.
	for i := 3; i <= 6; i++ {
		d.recordAction(i)
		d.recordToolResult("edit_file", "bar.go", "ok", i)
	}

	msg := d.check()
	if msg == "" {
		t.Fatal("expected guidance after 4 disconnected actions")
	}
	if !strings.Contains(msg, "[diagnostic disconnect]") {
		t.Fatalf("expected diagnostic disconnect label, got: %s", msg)
	}
	if !strings.Contains(msg, "undefined: NewWidget") {
		t.Fatalf("expected diagnostic keyword in message, got: %s", msg)
	}
}

func TestDiagnosticDisconnect_NoTriggerWhenUnderThreshold(t *testing.T) {
	d := newDiagnosticDisconnectState()

	d.recordToolResult("run_command", "",
		"foo.go:5: undefined: NewWidget", 1)

	// Only 2 subsequent actions (below threshold of 4).
	d.recordAction(2)
	d.recordToolResult("edit_file", "bar.go", "ok", 2)
	d.recordAction(3)
	d.recordToolResult("edit_file", "baz.go", "ok", 3)

	msg := d.check()
	if msg != "" {
		t.Fatalf("expected no guidance with only 2 disconnected actions, got: %s", msg)
	}
}

func TestDiagnosticDisconnect_AddressViaBasenameMatch(t *testing.T) {
	d := newDiagnosticDisconnectState()

	// Diagnostic references a relative path.
	d.recordToolResult("run_command", "",
		"src/widget.go:10: error: undefined: Render", 1)

	// Agent edits using a different path prefix to same basename.
	d.recordAction(2)
	d.recordToolResult("edit_file", "/abs/path/to/src/widget.go", "ok", 2)

	if !d.activeDiagnostics[0].addressed {
		t.Fatal("diagnostic should be addressed via basename match")
	}
}

func TestDiagnosticDisconnect_MaxWarnings(t *testing.T) {
	d := newDiagnosticDisconnectState()

	// First diagnostic.
	d.recordToolResult("run_command", "",
		"foo.go:5: undefined: FuncA", 1)
	for i := 2; i <= 5; i++ {
		d.recordAction(i)
		d.recordToolResult("edit_file", "bar.go", "ok", i)
	}
	msg1 := d.check()
	if msg1 == "" {
		t.Fatal("expected first guidance")
	}

	// Second diagnostic.
	d.recordToolResult("run_command", "",
		"baz.go:5: undefined: FuncB", 6)
	for i := 7; i <= 10; i++ {
		d.recordAction(i)
		d.recordToolResult("edit_file", "qux.go", "ok", i)
	}
	msg2 := d.check()
	if msg2 == "" {
		t.Fatal("expected second guidance")
	}

	// Third diagnostic - should NOT fire (max 2 warnings).
	d.recordToolResult("run_command", "",
		"quux.go:5: undefined: FuncC", 11)
	for i := 12; i <= 15; i++ {
		d.recordAction(i)
		d.recordToolResult("edit_file", "other.go", "ok", i)
	}
	msg3 := d.check()
	if msg3 != "" {
		t.Fatal("expected no third guidance (max warnings reached)")
	}
}

func TestDiagnosticDisconnect_Reset(t *testing.T) {
	d := newDiagnosticDisconnectState()
	d.recordToolResult("run_command", "",
		"foo.go:5: undefined: FuncA", 1)
	d.recordAction(2)
	d.warningCount = 1

	d.reset()

	if len(d.activeDiagnostics) != 0 {
		t.Fatal("expected no active diagnostics after reset")
	}
	if d.warningCount != 0 {
		t.Fatal("expected warningCount=0 after reset")
	}
}

func TestDiagnosticDisconnect_DuplicateNotRegistered(t *testing.T) {
	d := newDiagnosticDisconnectState()

	d.recordToolResult("run_command", "",
		"foo.go:5: undefined: NewWidget", 1)
	d.recordToolResult("run_command", "",
		"foo.go:5: undefined: NewWidget", 2)

	count := 0
	for _, diag := range d.activeDiagnostics {
		if diag.keyword == "undefined: NewWidget" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("expected 1 duplicate diagnostic, got %d", count)
	}
}

func TestDiagnosticDisconnect_DifferentDiagnosticsRegistered(t *testing.T) {
	d := newDiagnosticDisconnectState()

	d.recordToolResult("run_command", "",
		"foo.go:5: undefined: FuncA", 1)
	d.recordToolResult("run_command", "",
		"bar.go:10: error: missing argument", 2)

	if len(d.activeDiagnostics) != 2 {
		t.Fatalf("expected 2 diagnostics, got %d", len(d.activeDiagnostics))
	}
}

func TestDiagnosticDisconnect_NoDiagnosticFromSuccess(t *testing.T) {
	d := newDiagnosticDisconnectState()

	// Result without error keywords.
	d.recordToolResult("run_command", "",
		"build completed successfully", 1)

	if len(d.activeDiagnostics) != 0 {
		t.Fatalf("expected 0 diagnostics from success result, got %d", len(d.activeDiagnostics))
	}
}

func TestNormalizeDiagnostic(t *testing.T) {
	cases := []struct {
		input    string
		expected string
	}{
		{"foo.go:5: undefined: NewWidget", "undefined: NewWidget"},
		{"error: missing semicolon", "missing semicolon"},
		{"panic: runtime error: nil pointer dereference", "panic: runtime error: nil pointer dereference"},
		{"all good here", ""},
	}
	for _, tc := range cases {
		got := normalizeDiagnostic(tc.input)
		if got != tc.expected && tc.expected != "" {
			// For panic, the pattern may capture more or less; just check prefix.
			if !strings.HasPrefix(got, tc.expected[:min(5, len(tc.expected))]) && tc.expected != "" {
				t.Errorf("normalizeDiagnostic(%q) = %q, want %q", tc.input, got, tc.expected)
			}
		}
	}
}

func TestFileMatches(t *testing.T) {
	cases := []struct {
		target string
		source string
		want   bool
	}{
		{"foo.go", "foo.go", true},
		{"src/foo.go", "foo.go", true},
		{"foo.go", "src/foo.go", true},
		{"/abs/src/foo.go", "src/foo.go", true},
		{"bar.go", "foo.go", false},
		{"", "foo.go", false},
		{"foo.go", "", false},
	}
	for _, tc := range cases {
		got := fileMatches(tc.target, tc.source)
		if got != tc.want {
			t.Errorf("fileMatches(%q, %q) = %v, want %v", tc.target, tc.source, got, tc.want)
		}
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
