package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/topcheer/ggcode/internal/provider"
)

func TestShortCommit(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"abcdef1234567890", "abcdef123456"},
		{"abc", "abc"},
		{"  abcdef1234567890  ", "abcdef123456"},
		{"", ""},
	}
	for _, tt := range tests {
		got := shortCommit(tt.input)
		if got != tt.expected {
			t.Errorf("shortCommit(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestHarnessFirstNonEmpty(t *testing.T) {
	tests := []struct {
		input    []string
		expected string
	}{
		{[]string{"", "", "hello"}, "hello"},
		{[]string{"first", "second"}, "first"},
		{[]string{"", "  spaced  "}, "  spaced  "},
		{[]string{"", ""}, ""},
		{[]string{}, ""},
	}
	for _, tt := range tests {
		got := harnessFirstNonEmpty(tt.input...)
		if got != tt.expected {
			t.Errorf("harnessFirstNonEmpty(%v) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestValueOrEmpty(t *testing.T) {
	hello := "hello"
	empty := ""
	space := "  trimmed  "

	tests := []struct {
		input    *string
		expected string
	}{
		{nil, ""},
		{&hello, "hello"},
		{&empty, ""},
		{&space, "trimmed"},
	}
	for _, tt := range tests {
		got := valueOrEmpty(tt.input)
		if got != tt.expected {
			t.Errorf("valueOrEmpty(%v) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestFormatInitResult_Nil(t *testing.T) {
	got := formatInitResult(nil)
	if got == "" {
		t.Error("expected non-empty for nil result")
	}
}

func TestRelOrAbs(t *testing.T) {
	tests := []struct {
		root     string
		path     string
		expected string
	}{
		{"/home/user/project", "/home/user/project/file.txt", "file.txt"},
		{"/home/user/project", "/other/path", "/other/path"},
	}
	for _, tt := range tests {
		got := relOrAbs(tt.root, tt.path)
		if got != tt.expected {
			t.Errorf("relOrAbs(%q, %q) = %q, want %q", tt.root, tt.path, got, tt.expected)
		}
	}
}

func TestValueOr(t *testing.T) {
	got := valueOr("", "default")
	if got != "default" {
		t.Errorf("expected 'default', got %q", got)
	}
	got = valueOr("actual", "default")
	if got != "actual" {
		t.Errorf("expected 'actual', got %q", got)
	}
}

func TestMaskSecret(t *testing.T) {
	got := maskSecret("sk-1234567890abcdef")
	if got == "sk-1234567890abcdef" {
		t.Error("expected masked secret")
	}
	if len(got) < 4 {
		t.Error("masked secret too short")
	}
}

func TestParseA2ATimeout(t *testing.T) {
	got := parseA2ATimeout("30s")
	if got != 30*time.Second {
		t.Errorf("expected 30s, got %v", got)
	}
	got = parseA2ATimeout("5m")
	if got != 5*time.Minute {
		t.Errorf("expected 5m, got %v", got)
	}
	got = parseA2ATimeout("")
	if got != 5*time.Minute {
		t.Errorf("expected default 5m, got %v", got)
	}
}

func TestSummarizePipeToolArguments(t *testing.T) {
	got := summarizePipeToolArguments(json.RawMessage(`{"file_path": "/some/path"}`))
	if got == "" {
		t.Error("expected non-empty summary")
	}
}

// TestFormatPipeProgressEvent tests that a text event formats correctly.
func TestFormatPipeProgressEvent(t *testing.T) {
	got := formatPipeProgressEvent(provider.StreamEvent{
		Type: provider.StreamEventDone,
	})
	// Done events may produce empty text, that's fine — just no panic
	_ = got
}

func TestLooksLikeIndexSelection(t *testing.T) {
	tests := []struct {
		input    string
		expected bool
	}{
		{"1", true},
		{"1,2,3", true},
		{"1, 3, 5", true},
		{"hello", false},
		{"", false},
	}
	for _, tt := range tests {
		got := looksLikeIndexSelection(tt.input)
		if got != tt.expected {
			t.Errorf("looksLikeIndexSelection(%q) = %v, want %v", tt.input, got, tt.expected)
		}
	}
}

func TestNormalizeInputList(t *testing.T) {
	got := normalizeInputList([]string{"a", "b", "c"})
	if len(got) != 3 {
		t.Errorf("expected 3, got %d", len(got))
	}
	got = normalizeInputList([]string{})
	if got != nil {
		t.Errorf("expected nil, got %v", got)
	}
}

func TestParseOneBasedIndex(t *testing.T) {
	idx, ok := parseOneBasedIndex("3", 5)
	if !ok || idx != 2 {
		t.Errorf("expected (2, true), got (%d, %v)", idx, ok)
	}
	_, ok = parseOneBasedIndex("0", 5)
	if ok {
		t.Error("expected false for 0")
	}
	_, ok = parseOneBasedIndex("6", 5)
	if ok {
		t.Error("expected false for out of range")
	}
}

func TestFirstNonEmpty(t *testing.T) {
	got := firstNonEmpty("", "", "hello", "world")
	if got != "hello" {
		t.Errorf("expected 'hello', got %q", got)
	}
	got = firstNonEmpty()
	if got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

func TestNormalizeWorkspacePath(t *testing.T) {
	got := normalizeWorkspacePath("/some/path")
	if got == "" {
		t.Error("expected non-empty")
	}
}

func TestMergedFlags(t *testing.T) {
	// Just verify it doesn't panic with a minimal command
	cmd := NewRootCmd()
	if cmd == nil {
		t.Fatal("expected non-nil root cmd")
	}
	_ = mergedFlags(cmd)
}

func TestWriteCommandList(t *testing.T) {
	var b strings.Builder
	writeCommandList(&b, "Test", []*cobra.Command{NewRootCmd()})
	// May produce empty if commands have no name, just verify no panic
	_ = b.String()
}

func TestWriteFlagList(t *testing.T) {
	var b strings.Builder
	f := pflag.NewFlagSet("test", pflag.ContinueOnError)
	f.String("test-flag", "", "test usage")
	var flags []*pflag.Flag
	f.VisitAll(func(f *pflag.Flag) { flags = append(flags, f) })
	writeFlagList(&b, "Test", flags)
	if b.Len() == 0 {
		t.Error("expected non-empty output")
	}
}
