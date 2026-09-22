package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func buildTestSystemPrompt(n int) string {
	var sb strings.Builder
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&sb, "prompt line %d\n", i)
	}
	return sb.String()
}

func TestFormatSystemPromptSummaryPreviewTruncates(t *testing.T) {
	prompt := buildTestSystemPrompt(100)
	out := formatSystemPromptSummary(prompt, 40)

	if !strings.Contains(out, "Assembled System Prompt") {
		t.Fatalf("missing header: %q", out[:80])
	}
	if !strings.Contains(out, "first 40 of 100 lines") {
		t.Fatalf("missing truncation notice: %q", out[:200])
	}
	if strings.Contains(out, "prompt line 100") {
		t.Fatal("preview should not contain the tail of the prompt")
	}
	if !strings.Contains(out, "prompt line 40") {
		t.Fatal("preview should contain the first 40 lines")
	}
}

func TestFormatSystemPromptSummaryFull(t *testing.T) {
	prompt := buildTestSystemPrompt(100)
	out := formatSystemPromptSummary(prompt, 0)

	if !strings.Contains(out, "prompt line 100") {
		t.Fatal("full output must contain the entire prompt")
	}
	if strings.Contains(out, "first ") {
		t.Fatal("full output must not show a truncation notice")
	}
}

func TestWriteSystemPromptFileDefaultTempDir(t *testing.T) {
	prompt := "hello system prompt"
	path, err := writeSystemPromptFile(prompt, "")
	if err != nil {
		t.Fatalf("writeSystemPromptFile: %v", err)
	}
	defer os.Remove(path)

	if filepath.Dir(path) != filepath.Clean(os.TempDir()) {
		t.Fatalf("expected file in temp dir, got %s", path)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(got) != prompt {
		t.Fatalf("content mismatch: %q", string(got))
	}
	if !strings.HasPrefix(filepath.Base(path), "ggcode-system-prompt-") {
		t.Fatalf("unexpected file name: %s", filepath.Base(path))
	}
}

func TestWriteSystemPromptFileDirectoryArg(t *testing.T) {
	dir := t.TempDir()
	path, err := writeSystemPromptFile("content", dir)
	if err != nil {
		t.Fatalf("writeSystemPromptFile: %v", err)
	}
	if filepath.Dir(path) != dir {
		t.Fatalf("expected file inside %s, got %s", dir, path)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(got) != "content" {
		t.Fatalf("content mismatch: %q", string(got))
	}
}

func TestPromptCommandExecutableWhileBusy(t *testing.T) {
	if !shouldExecuteWhileBusy("/prompt") {
		t.Fatal("/prompt should be executable while the agent is busy (read-only inspection)")
	}
}
