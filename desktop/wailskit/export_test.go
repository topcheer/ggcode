//go:build goolm

package wailskit

import (
	"strings"
	"testing"
)

func TestFormatMessagesAsMarkdown_Format(t *testing.T) {
	msgs := []SessionMessage{
		{Role: "user", Content: "Hello, can you help me?"},
		{Role: "assistant", Content: "Sure! Here's what I can do."},
		{Role: "tool", ToolName: "read_file", ToolDisplay: "Read File", ToolDetail: "path: main.go", Content: "package main"},
	}

	md := formatMessagesAsMarkdown(msgs, "My Session")

	// Title
	if !strings.HasPrefix(md, "# My Session\n") {
		t.Errorf("expected title header, got: %q", md[:50])
	}

	// User section
	if !strings.Contains(md, "## User") {
		t.Error("missing User section")
	}
	if !strings.Contains(md, "Hello, can you help me?") {
		t.Error("missing user content")
	}

	// Assistant section
	if !strings.Contains(md, "## Assistant") {
		t.Error("missing Assistant section")
	}
	if !strings.Contains(md, "Sure! Here's what I can do.") {
		t.Error("missing assistant content")
	}

	// Tool section
	if !strings.Contains(md, "### Read File") {
		t.Error("missing tool header with display name")
	}
	if !strings.Contains(md, "package main") {
		t.Error("missing tool content")
	}
}

func TestFormatMessagesAsMarkdown_Truncation(t *testing.T) {
	longContent := strings.Repeat("x", 3000)
	msgs := []SessionMessage{
		{Role: "tool", ToolName: "run_command", Content: longContent},
	}

	md := formatMessagesAsMarkdown(msgs, "")
	if !strings.Contains(md, "(truncated)") {
		t.Error("expected truncation marker for long tool output")
	}
	if len(md) > 2500 {
		t.Errorf("expected truncated output, got %d bytes", len(md))
	}
}

func TestFormatMessagesAsMarkdown_EmptyTitle(t *testing.T) {
	msgs := []SessionMessage{
		{Role: "user", Content: "test"},
	}

	md := formatMessagesAsMarkdown(msgs, "")
	if !strings.HasPrefix(md, "# GGCode Session\n") {
		t.Errorf("expected default title, got: %q", md[:30])
	}
}

func TestFormatMessagesAsJSON_Valid(t *testing.T) {
	msgs := []SessionMessage{
		{Role: "user", Content: "test message"},
	}

	js, err := formatMessagesAsJSON(msgs, "Test Session")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(js, "\"title\": \"Test Session\"") {
		t.Error("missing title in JSON export")
	}
	if !strings.Contains(js, "\"role\": \"user\"") {
		t.Error("missing role in JSON export")
	}
	if !strings.Contains(js, "\"content\": \"test message\"") {
		t.Error("missing content in JSON export")
	}
}
