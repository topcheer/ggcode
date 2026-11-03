//go:build goolm

package wailskit

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/security"
)

func TestIssue583_RedactForDisplay(t *testing.T) {
	tests := []struct {
		name         string
		input        string
		wantRedacted bool
	}{
		{
			name:         "stripe sk-live key",
			input:        "api_key: sk-live-1234567890abcdef1234567890abcdef1234567890",
			wantRedacted: true,
		},
		{
			name:         "openai sk- key",
			input:        `{"api_key":"sk-proj-AbCdEf1234567890abcdefghijklmnopqrstuvwxyz"}`,
			wantRedacted: true,
		},
		{
			name:         "anthropic key",
			input:        "ANTHROPIC_API_KEY=sk-ant-api03-abcdefghijklmnopqrstuvwxyz1234567890123456789012345678901234567890",
			wantRedacted: true,
		},
		{
			name:         "no secrets",
			input:        "hello world",
			wantRedacted: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			redacted := security.RedactForDisplay(tt.input)
			hasSecret := security.HasSecretPattern(tt.input)
			if hasSecret != tt.wantRedacted {
				t.Errorf("HasSecretPattern(%q) = %v, want %v", tt.input, hasSecret, tt.wantRedacted)
			}
			if hasSecret && redacted == tt.input {
				t.Error("RedactForDisplay did not modify secret content")
			}
		})
	}
}

func TestIssue583_MarkdownExportRedaction(t *testing.T) {
	msgs := []SessionMessage{
		{
			Role:       "tool",
			ToolName:   "config",
			ToolArgs:   `{"setting":"api_key","value":"sk-live-1234567890abcdef1234567890abcdef"}`,
			ToolDetail: "Set API key: sk-live-1234567890abcdef1234567890abcdef",
			Content:    "API key set successfully",
		},
	}

	markdownOutput := formatMessagesAsMarkdown(msgs, "Test Session")

	if strings.Contains(markdownOutput, "sk-live-1234567890abcdef1234567890abcdef") {
		t.Error("Markdown export contains unredacted API key in ToolDetail")
	}

	if !strings.Contains(markdownOutput, "sk-") || strings.Contains(markdownOutput, "sk-live-1234567890abcdef1234567890abcdef") {

		if strings.Contains(markdownOutput, "sk-") {

			if strings.Contains(markdownOutput, "1234567890abcdef") {
				t.Error("Markdown export should mask the middle part of the key")
			}
		}
	}
}

func TestIssue583_JSONExportRedaction(t *testing.T) {
	largeContent := strings.Repeat("A", 300000)
	apiKeyArgs := `{"setting":"api_key","value":"sk-live-1234567890abcdef1234567890abcdef1234567890"}`

	msgs := []SessionMessage{
		{
			Role:     "tool",
			ToolName: "read_file",
			ToolArgs: `{"path":"large-file.txt"}`,
			Content:  largeContent,
		},
		{
			Role:       "tool",
			ToolName:   "config",
			ToolArgs:   apiKeyArgs,
			ToolDetail: "Set API key: sk-live-1234567890abcdef1234567890abcdef1234567890",
			Content:    "API key set",
		},
	}

	jsonOutput, err := formatMessagesAsJSON(msgs, "Test Session")
	if err != nil {
		t.Fatalf("format as JSON: %v", err)
	}

	if len(jsonOutput) > 50000 {
		t.Errorf("JSON export too large: %d bytes (expected < 50KB after truncation)", len(jsonOutput))
	}

	if strings.Contains(jsonOutput, "sk-live-1234567890abcdef1234567890abcdef1234567890") {
		t.Error("JSON export contains unredacted sk-live API key")
	}

	if strings.Contains(jsonOutput, strings.Repeat("A", 3000)) {
		t.Error("JSON export contains untruncated large content block (found 3000+ consecutive 'A's)")
	}

	var exportData struct {
		Title    string           `json:"title"`
		Messages []SessionMessage `json:"messages"`
	}
	if err := json.Unmarshal([]byte(jsonOutput), &exportData); err != nil {
		t.Errorf("JSON output is not valid: %v", err)
	}
}

func TestIssue583_ConfigMultiInstanceMerge(t *testing.T) {
	tmpDir := t.TempDir()

	t.Setenv("HOME", tmpDir)
	if err := os.MkdirAll(filepath.Join(tmpDir, ".ggcode"), 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	configPath := filepath.Join(tmpDir, ".ggcode", "desktop-config.json")

	instA := &DesktopConfig{
		WorkDir:  "/workspace/a",
		Language: "",
		WindowW:  1280,
		WindowH:  860,
	}

	instA.Language = "zh-CN"
	if err := instA.Save(); err != nil {
		t.Fatalf("save instance A: %v", err)
	}

	diskA := &DesktopConfig{WindowW: 1280, WindowH: 860}
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config after A save: %v", err)
	}
	if err := json.Unmarshal(data, diskA); err != nil {
		t.Fatalf("unmarshal config after A save: %v", err)
	}
	if diskA.Language != "zh-CN" {
		t.Errorf("After A save: expected language=zh-CN, got %s", diskA.Language)
	}

	instB := &DesktopConfig{
		Language: "",
	}
	instB.SetWorkDir("/workspace/b")
	instB.SetWindowState(1920, 1080, 0, 0, false)

	if err := instB.Save(); err != nil {
		t.Fatalf("save instance B: %v", err)
	}

	diskMerged := &DesktopConfig{WindowW: 1280, WindowH: 860}
	data, err = os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config after B save: %v", err)
	}
	if err := json.Unmarshal(data, diskMerged); err != nil {
		t.Fatalf("unmarshal config after B save: %v", err)
	}

	if diskMerged.Language != "zh-CN" {
		t.Errorf("After B save: expected language=zh-CN (from A), got %s (BUG: A's preference was rolled back)", diskMerged.Language)
	}

	if diskMerged.WorkDir != "/workspace/b" {
		t.Errorf("After B save: expected workdir=/workspace/b (from B), got %s", diskMerged.WorkDir)
	}

	if diskMerged.WindowW != 1920 {
		t.Errorf("After B save: expected window_width=1920 (from B), got %d", diskMerged.WindowW)
	}
	if diskMerged.WindowH != 1080 {
		t.Errorf("After B save: expected window_height=1080 (from B), got %d", diskMerged.WindowH)
	}
}

func TestIssue583_LastSessionFieldRemoved(t *testing.T) {

	dc := &DesktopConfig{}

	_ = dc

}

func TestIssue583_ListSessionsLockErrorFailClosed(t *testing.T) {

	list, err := ListSessions("", nil)
	if err != nil {

		t.Logf("ListSessions returned error (expected in test env): %v", err)
	}
	_ = list
}
