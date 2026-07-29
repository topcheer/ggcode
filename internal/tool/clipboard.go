package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// ClipboardTool lets the agent read from and write to the system clipboard.
// This bridges the terminal and other applications — the agent can retrieve
// text the user copied (error messages, URLs, code snippets) or place output
// (generated code, commit messages) on the clipboard for the user to paste.
type ClipboardTool struct{}

func (ClipboardTool) Name() string { return "clipboard" }

func (ClipboardTool) Description() string {
	return "Read from or write to the system clipboard. " +
		"Use action='read' to get clipboard contents (e.g., error messages or code the user copied). " +
		"Use action='write' with a 'text' parameter to copy text to the clipboard. " +
		"Max 50,000 characters per operation."
}

func (ClipboardTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"action": {
				"type": "string",
				"enum": ["read", "write"],
				"description": "read = return clipboard contents; write = set clipboard to text"
			},
			"text": {
				"type": "string",
				"description": "Text to write to the clipboard (required for action='write', ignored for read)"
			},
			"description": {
				"type": "string",
				"description": "REQUIRED. Brief activity label shown in the UI."
			}
		},
		"required": ["action", "description"]
	}`)
}

const clipboardMaxChars = 50000

func (t ClipboardTool) Execute(ctx context.Context, input json.RawMessage) (Result, error) {
	var args struct {
		Action string `json:"action"`
		Text   string `json:"text"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("invalid input: %v", err)}, nil
	}

	switch args.Action {
	case "read":
		return t.readClipboard(ctx)
	case "write":
		if strings.TrimSpace(args.Text) == "" {
			return Result{IsError: true, Content: "action='write' requires non-empty 'text' parameter"}, nil
		}
		if len(args.Text) > clipboardMaxChars {
			return Result{IsError: true, Content: fmt.Sprintf("text exceeds maximum of %d characters", clipboardMaxChars)}, nil
		}
		return t.writeClipboard(ctx, args.Text)
	default:
		return Result{IsError: true, Content: fmt.Sprintf("unknown action %q: use 'read' or 'write'", args.Action)}, nil
	}
}

func (ClipboardTool) readClipboard(ctx context.Context) (Result, error) {
	cmd, err := clipboardReadCmd(ctx)
	if err != nil {
		return Result{IsError: true, Content: err.Error()}, nil
	}
	out, err := cmd.Output()
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("clipboard read failed: %v", err)}, nil
	}
	text := string(out)
	if text == "" {
		return Result{Content: "(clipboard is empty)"}, nil
	}
	if len(text) > clipboardMaxChars {
		text = text[:clipboardMaxChars] + "\n... (truncated)"
	}
	return Result{Content: text}, nil
}

func (ClipboardTool) writeClipboard(ctx context.Context, text string) (Result, error) {
	cmd, err := clipboardWriteCmd(ctx)
	if err != nil {
		return Result{IsError: true, Content: err.Error()}, nil
	}
	cmd.Stdin = strings.NewReader(text)
	if err := cmd.Run(); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("clipboard write failed: %v", err)}, nil
	}
	preview := text
	if len(preview) > 200 {
		preview = preview[:200] + "..."
	}
	return Result{Content: fmt.Sprintf("Copied %d characters to clipboard: %s", len(text), preview)}, nil
}

// clipboardReadCmd returns an exec.Cmd that writes clipboard contents to stdout.
func clipboardReadCmd(ctx context.Context) (*exec.Cmd, error) {
	switch runtime.GOOS {
	case "darwin":
		return exec.CommandContext(ctx, "pbpaste"), nil
	case "windows":
		return exec.CommandContext(ctx, "powershell", "-NoProfile", "-Command", "Get-Clipboard"), nil
	default: // linux, freebsd, etc.
		if path, _ := exec.LookPath("wl-paste"); path != "" {
			return exec.CommandContext(ctx, path), nil
		}
		if path, _ := exec.LookPath("xclip"); path != "" {
			return exec.CommandContext(ctx, path, "-selection", "clipboard", "-o"), nil
		}
		if path, _ := exec.LookPath("xsel"); path != "" {
			return exec.CommandContext(ctx, path, "--clipboard", "--output"), nil
		}
		return nil, fmt.Errorf("no clipboard utility found (install xclip, xsel, or wl-clipboard)")
	}
}

// clipboardWriteCmd returns an exec.Cmd that reads from stdin and writes to the clipboard.
func clipboardWriteCmd(ctx context.Context) (*exec.Cmd, error) {
	switch runtime.GOOS {
	case "darwin":
		return exec.CommandContext(ctx, "pbcopy"), nil
	case "windows":
		return exec.CommandContext(ctx, "clip"), nil
	default:
		if path, _ := exec.LookPath("wl-copy"); path != "" {
			return exec.CommandContext(ctx, path), nil
		}
		if path, _ := exec.LookPath("xclip"); path != "" {
			return exec.CommandContext(ctx, path, "-selection", "clipboard"), nil
		}
		if path, _ := exec.LookPath("xsel"); path != "" {
			return exec.CommandContext(ctx, path, "--clipboard", "--input"), nil
		}
		return nil, fmt.Errorf("no clipboard utility found (install xclip, xsel, or wl-clipboard)")
	}
}

// ClipboardAvailable reports whether a clipboard utility is installed.
// Exported so callers (e.g. registration guards) can check before registering.
func ClipboardAvailable() bool {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err := clipboardReadCmd(ctx)
	return err == nil
}
