package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// OpenEditorTool lets the agent open a file (optionally at a specific line) in
// the user's preferred external editor or IDE. This bridges the terminal-based
// agent loop and GUI-based code review workflows.
//
// The tool auto-detects the editor from (in priority order):
//  1. The $GID_EDITOR / $GGCODE_EDITOR env var (ggcode-specific override)
//  2. The $VISUAL env var
//  3. The $EDITOR env var
//  4. Well-known CLI launchers detected via exec.LookPath: VS Code (`code`),
//     Cursor (`cursor`), Zed (`zed`), Sublime Text (`subl`), IntelliJ IDEA
//     (`idea`), WebStorm (`webstorm`), GoLand (`goland`), Neovim (`nvim`),
//     Vim (`vim`), Emacs (`emacs`).
//
// On macOS, if no CLI launcher is found, the tool falls back to `open` which
// uses the system default application. On Linux it falls back to `xdg-open`.
// On Windows it falls back to `start`.
//
// The command is launched in detached mode (non-blocking): the tool returns
// immediately after starting the editor process, so the agent loop is not
// stalled waiting for the user to close the editor.
type OpenEditorTool struct {
	WorkingDir string
}

func (OpenEditorTool) Name() string { return "open_editor" }

func (OpenEditorTool) Description() string {
	return "Open a file in the user's external editor or IDE. " +
		"Supports optional line and column for jump-to-location. " +
		"Auto-detects the editor from $GGCODE_EDITOR, $VISUAL, $EDITOR, or installed IDEs (VS Code, Cursor, Zed, IntelliJ, etc.). " +
		"Non-blocking: returns immediately after launching. " +
		"Use when the user asks to open a file, or after completing edits the user should review in their IDE."
}

func (OpenEditorTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"path": {
				"type": "string",
				"description": "Absolute path to the file to open."
			},
			"line": {
				"type": "integer",
				"description": "Optional 1-based line number to jump to."
			},
			"column": {
				"type": "integer",
				"description": "Optional 1-based column number to jump to (requires line)."
			},
			"editor": {
				"type": "string",
				"description": "Optional editor override (e.g. 'code', 'cursor', 'vim', 'subl'). If omitted, auto-detects."
			},
			"description": {
				"type": "string",
				"description": "REQUIRED. Brief activity label shown in the UI."
			}
		},
		"required": ["path", "description"]
	}`)
}

func (t OpenEditorTool) Execute(ctx context.Context, input json.RawMessage) (Result, error) {
	var args struct {
		Path   string `json:"path"`
		Line   int    `json:"line"`
		Column int    `json:"column"`
		Editor string `json:"editor"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("invalid input: %v", err)}, nil
	}

	if msg := CheckRequired("path", args.Path); msg != "" {
		return Result{IsError: true, Content: "Error: " + msg}, nil
	}

	absPath, err := filepath.Abs(args.Path)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("error resolving path: %v", err)}, nil
	}
	if _, err := os.Stat(absPath); os.IsNotExist(err) {
		suggestion := suggestFilePath(absPath)
		msg := fmt.Sprintf("file not found: %s", absPath)
		if suggestion != "" {
			msg += suggestion
		}
		return Result{IsError: true, Content: msg}, nil
	}

	editor := args.Editor
	if editor == "" {
		editor = detectEditor()
	}
	if editor == "" {
		return Result{IsError: true, Content: "no editor detected. Set $EDITOR, $VISUAL, or $GGCODE_EDITOR, or pass the 'editor' parameter."}, nil
	}

	cmd := buildEditorCommand(editor, absPath, args.Line, args.Column)
	if cmd == nil {
		return Result{IsError: true, Content: fmt.Sprintf("could not build launch command for editor %q on %s", editor, runtime.GOOS)}, nil
	}

	// Detach the editor process so it doesn't block the agent loop.
	// Set process group / creation flags so the child survives independent of ggcode.
	if err := startDetached(cmd); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("failed to launch editor %q: %v", editor, err)}, nil
	}

	loc := ""
	if args.Line > 0 {
		loc = fmt.Sprintf(" at line %d", args.Line)
		if args.Column > 0 {
			loc += fmt.Sprintf(", column %d", args.Column)
		}
	}
	return Result{Content: fmt.Sprintf("Opened %s in %s%s", filepath.Base(absPath), editorName(editor), loc)}, nil
}

// detectEditor returns the best available editor command.
func detectEditor() string {
	// 1. ggcode-specific override
	for _, key := range []string{"GGCODE_EDITOR", "GID_EDITOR"} {
		if v := os.Getenv(key); v != "" {
			return v
		}
	}
	// 2. Standard env vars
	for _, key := range []string{"VISUAL", "EDITOR"} {
		if v := os.Getenv(key); v != "" {
			// Strip common arguments like "code --wait" — take just the binary
			return strings.Fields(v)[0]
		}
	}
	// 3. Well-known IDE launchers
	for _, name := range ideLaunchers() {
		if path, _ := exec.LookPath(name); path != "" {
			return name
		}
	}
	return ""
}

// ideLaunchers returns IDE/editor CLI binaries in priority order.
func ideLaunchers() []string {
	return []string{"code", "cursor", "zed", "subl", "idea", "webstorm", "goland", "pycharm", "nvim", "vim", "emacs"}
}

// editorName returns a human-friendly name for the editor binary.
func editorName(editor string) string {
	switch strings.ToLower(filepath.Base(editor)) {
	case "code":
		return "VS Code"
	case "cursor":
		return "Cursor"
	case "zed":
		return "Zed"
	case "subl":
		return "Sublime Text"
	case "idea":
		return "IntelliJ IDEA"
	case "webstorm":
		return "WebStorm"
	case "goland":
		return "GoLand"
	case "pycharm":
		return "PyCharm"
	case "nvim":
		return "Neovim"
	case "vim":
		return "Vim"
	case "emacs":
		return "Emacs"
	case "open", "xdg-open", "start":
		return "system default"
	default:
		return filepath.Base(editor)
	}
}

// buildEditorCommand constructs an exec.Cmd for the given editor, file, and
// optional line/column. Returns nil if the editor/platform combo is unsupported.
func buildEditorCommand(editor, file string, line, col int) *exec.Cmd {
	base := strings.ToLower(filepath.Base(editor))
	hasLine := line > 0

	// Build the argument list based on the editor type.
	switch base {
	case "code", "cursor", "zed", "code-insiders":
		// VS Code family: editor --goto file:line:column
		args := []string{}
		if hasLine {
			loc := fmt.Sprintf("%s:%d", file, line)
			if col > 0 {
				loc += fmt.Sprintf(":%d", col)
			}
			args = append(args, "--goto", loc)
		} else {
			args = append(args, file)
		}
		return exec.Command(editor, args...)

	case "subl":
		// Sublime Text: subl file:line:column
		if hasLine {
			loc := fmt.Sprintf("%s:%d", file, line)
			if col > 0 {
				loc += fmt.Sprintf(":%d", col)
			}
			return exec.Command(editor, loc)
		}
		return exec.Command(editor, file)

	case "idea", "webstorm", "goland", "pycharm", "phpstorm", "rubymine", "clion":
		// JetBrains IDEs: editor --line N file
		if hasLine {
			return exec.Command(editor, "--line", fmt.Sprintf("%d", line), file)
		}
		return exec.Command(editor, file)

	case "nvim", "vim":
		// Vim/Neovim: editor +line file  (or +line,column for nvim)
		if hasLine {
			flag := fmt.Sprintf("+%d", line)
			if col > 0 && base == "nvim" {
				flag = fmt.Sprintf("+call cursor(%d,%d)", line, col)
			}
			return exec.Command(editor, flag, file)
		}
		return exec.Command(editor, file)

	case "emacs":
		// Emacs: editor +N file
		if hasLine {
			return exec.Command(editor, fmt.Sprintf("+%d", line), file)
		}
		return exec.Command(editor, file)

	case "nano", "micro", "jed", "joe":
		// Terminal editors that support +line
		if hasLine {
			return exec.Command(editor, fmt.Sprintf("+%d", line), file)
		}
		return exec.Command(editor, file)
	}

	// Fallback: platform default openers
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", file)
	case "windows":
		return exec.Command("cmd", "/c", "start", "", file)
	default:
		if path, _ := exec.LookPath("xdg-open"); path != "" {
			return exec.Command(path, file)
		}
		return exec.Command(editor, file)
	}
}

// startDetached launches the command in a detached process so it doesn't
// block the calling goroutine or get killed when ggcode exits.
func startDetached(cmd *exec.Cmd) error {
	// Don't inherit stdin/stdout/stderr — the editor manages its own I/O.
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil

	cmd.SysProcAttr = detachSysProcAttr()

	return cmd.Start()
}
