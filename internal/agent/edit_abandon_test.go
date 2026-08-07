package agent

import "testing"

func TestEditAbandon_NoWarnBelowThreshold(t *testing.T) {
	s := newEditAbandonState()
	// Only 1 edited file -- below minEditedFiles of 2
	s.recordToolCall("edit_file", `{"path":"a.go"}`)
	s.recordToolCall("read_file", `{"path":"b.go"}`)
	s.recordToolCall("read_file", `{"path":"c.go"}`)
	s.recordToolCall("read_file", `{"path":"d.go"}`)
	if msg := s.maybeWarn(); msg != "" {
		t.Fatalf("expected no warning for single edit, got: %s", msg)
	}
}

func TestEditAbandon_NoWarnWhenReadingEditedFiles(t *testing.T) {
	s := newEditAbandonState()
	s.recordToolCall("edit_file", `{"path":"a.go"}`)
	s.recordToolCall("edit_file", `{"path":"b.go"}`)
	// Reading the SAME edited files resets attention shift
	s.recordToolCall("read_file", `{"path":"a.go"}`)
	s.recordToolCall("read_file", `{"path":"b.go"}`)
	s.recordToolCall("read_file", `{"path":"a.go"}`)
	if msg := s.maybeWarn(); msg != "" {
		t.Fatalf("expected no warning when reading edited files, got: %s", msg)
	}
	if s.consecutiveNonEdit != 0 {
		t.Fatalf("expected consecutiveNonEdit=0, got %d", s.consecutiveNonEdit)
	}
}

func TestEditAbandon_WarnsOnAttentionShift(t *testing.T) {
	s := newEditAbandonState()
	// Edit two files
	s.recordToolCall("edit_file", `{"path":"a.go"}`)
	s.recordToolCall("edit_file", `{"path":"b.go"}`)
	// Shift attention to unrelated files (3 consecutive)
	s.recordToolCall("read_file", `{"path":"c.go"}`)
	s.recordToolCall("read_file", `{"path":"d.go"}`)
	s.recordToolCall("read_file", `{"path":"e.go"}`)
	// totalCalls=5, below minTotalCalls of 6
	if msg := s.maybeWarn(); msg != "" {
		t.Fatalf("expected no warning below minTotalCalls, got: %s", msg)
	}
	// One more unrelated call to reach totalCalls=6
	s.recordToolCall("grep", `{"pattern":"foo"}`)
	if msg := s.maybeWarn(); msg == "" {
		t.Fatal("expected warning for abandoned edits")
	}
}

func TestEditAbandon_VerifyClearsState(t *testing.T) {
	s := newEditAbandonState()
	s.recordToolCall("edit_file", `{"path":"a.go"}`)
	s.recordToolCall("edit_file", `{"path":"b.go"}`)
	s.recordToolCall("read_file", `{"path":"c.go"}`)
	s.recordToolCall("read_file", `{"path":"d.go"}`)
	s.recordToolCall("read_file", `{"path":"e.go"}`)
	s.recordToolCall("run_command", `{"command":"go build ./..."}`)
	if msg := s.maybeWarn(); msg != "" {
		t.Fatalf("expected no warning after verify, got: %s", msg)
	}
	if len(s.editedFiles) != 0 {
		t.Fatalf("expected editedFiles cleared, got %d", len(s.editedFiles))
	}
}

func TestEditAbandon_LSPDiagnosticsIsVerify(t *testing.T) {
	s := newEditAbandonState()
	s.recordToolCall("edit_file", `{"path":"a.go"}`)
	s.recordToolCall("edit_file", `{"path":"b.go"}`)
	s.recordToolCall("read_file", `{"path":"c.go"}`)
	s.recordToolCall("read_file", `{"path":"d.go"}`)
	s.recordToolCall("read_file", `{"path":"e.go"}`)
	s.recordToolCall("lsp_diagnostics", `{"path":"a.go"}`)
	if msg := s.maybeWarn(); msg != "" {
		t.Fatalf("lsp_diagnostics should clear debt, got: %s", msg)
	}
}

func TestEditAbandon_MaxWarningsPerRun(t *testing.T) {
	s := newEditAbandonState()
	// Build up abandoned edits
	for i := 0; i < 3; i++ {
		s.recordToolCall("edit_file", `{"path":"f`+string(rune('a'+i))+`.go"}`)
	}
	for i := 0; i < 5; i++ {
		s.recordToolCall("read_file", `{"path":"x`+string(rune('a'+i))+`.go"}`)
	}
	msg1 := s.maybeWarn()
	if msg1 == "" {
		t.Fatal("expected first warning")
	}
	// New edits allow re-trigger
	s.recordToolCall("edit_file", `{"path":"new.go"}`)
	for i := 0; i < 4; i++ {
		s.recordToolCall("read_file", `{"path":"y`+string(rune('a'+i))+`.go"}`)
	}
	msg2 := s.maybeWarn()
	if msg2 == "" {
		t.Fatal("expected second warning")
	}
	msg3 := s.maybeWarn()
	if msg3 != "" {
		t.Fatal("expected no third warning (max 2)")
	}
}

func TestEditAbandon_Reset(t *testing.T) {
	s := newEditAbandonState()
	s.recordToolCall("edit_file", `{"path":"a.go"}`)
	s.recordToolCall("edit_file", `{"path":"b.go"}`)
	s.consecutiveNonEdit = 5
	s.totalCalls = 10
	s.reset()
	if s.totalCalls != 0 || s.consecutiveNonEdit != 0 || len(s.editedFiles) != 0 {
		t.Fatal("reset did not clear state")
	}
}

func TestEditAbandon_PartialShiftDoesNotTrigger(t *testing.T) {
	s := newEditAbandonState()
	s.recordToolCall("edit_file", `{"path":"a.go"}`)
	s.recordToolCall("edit_file", `{"path":"b.go"}`)
	// Only 2 consecutive unrelated calls (below threshold of 3)
	s.recordToolCall("read_file", `{"path":"c.go"}`)
	s.recordToolCall("read_file", `{"path":"d.go"}`)
	// Then return to an edited file
	s.recordToolCall("read_file", `{"path":"a.go"}`)
	s.recordToolCall("read_file", `{"path":"e.go"}`)
	s.recordToolCall("read_file", `{"path":"f.go"}`)
	if msg := s.maybeWarn(); msg != "" {
		t.Fatalf("should not warn -- shift was interrupted, got: %s", msg)
	}
	if s.consecutiveNonEdit != 2 {
		t.Fatalf("expected consecutiveNonEdit=2, got %d", s.consecutiveNonEdit)
	}
}

func TestEditAbandon_WriteFileCounts(t *testing.T) {
	s := newEditAbandonState()
	s.recordToolCall("write_file", `{"path":"new.go"}`)
	s.recordToolCall("write_file", `{"path":"other.go"}`)
	s.recordToolCall("read_file", `{"path":"a.go"}`)
	s.recordToolCall("read_file", `{"path":"b.go"}`)
	s.recordToolCall("read_file", `{"path":"c.go"}`)
	s.recordToolCall("read_file", `{"path":"d.go"}`)
	if len(s.editedFiles) != 2 {
		t.Fatalf("expected 2 edited files, got %d", len(s.editedFiles))
	}
	if msg := s.maybeWarn(); msg == "" {
		t.Fatal("expected warning for abandoned write_file edits")
	}
}

func TestExtractEAPaths(t *testing.T) {
	tests := []struct {
		args string
		want int
	}{
		{`{"path":"foo.go"}`, 1},
		{`{"file":"bar.go"}`, 1},
		{`{"file_path":"baz.go"}`, 1},
		{`{"other":"val"}`, 0},
		{"", 0},
		{`{"path":"a.go","file":"b.go"}`, 2},
	}
	for _, tc := range tests {
		got := extractEAPaths(tc.args)
		if len(got) != tc.want {
			t.Errorf("extractEAPaths(%q) = %v (len %d), want len %d", tc.args, got, len(got), tc.want)
		}
	}
}

func TestEAItoa(t *testing.T) {
	if eaItoa(0) != "0" {
		t.Fatal("eaItoa(0) failed")
	}
	if eaItoa(5) != "5" {
		t.Fatal("eaItoa(5) failed")
	}
	if eaItoa(-3) != "-3" {
		t.Fatal("eaItoa(-3) failed")
	}
	if eaItoa(42) != "42" {
		t.Fatal("eaItoa(42) failed")
	}
}
