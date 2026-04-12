package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/topcheer/ggcode/internal/lsp"
)

type lspPathTool struct {
	name         string
	description  string
	workingDir   string
	sandboxCheck AllowedPathChecker
	exec         func(context.Context, string, string) (string, error)
}

type lspPositionTool struct {
	name         string
	description  string
	workingDir   string
	sandboxCheck AllowedPathChecker
	exec         func(context.Context, string, string, lsp.Position) (string, error)
}

type lspRangeTool struct {
	name         string
	description  string
	workingDir   string
	sandboxCheck AllowedPathChecker
	exec         func(context.Context, string, string, lsp.Range) (string, error)
}

type lspWorkspaceQueryTool struct {
	name        string
	description string
	workingDir  string
	exec        func(context.Context, string, string) (string, error)
}

type lspRenameTool struct {
	name         string
	description  string
	workingDir   string
	readSandbox  AllowedPathChecker
	writeSandbox AllowedPathChecker
	applyEdits   func(context.Context, string, string, lsp.Position, string) (string, error)
}

func (t lspPathTool) Name() string                  { return t.name }
func (t lspPathTool) Description() string           { return t.description }
func (t lspPositionTool) Name() string              { return t.name }
func (t lspPositionTool) Description() string       { return t.description }
func (t lspRangeTool) Name() string                 { return t.name }
func (t lspRangeTool) Description() string          { return t.description }
func (t lspWorkspaceQueryTool) Name() string        { return t.name }
func (t lspWorkspaceQueryTool) Description() string { return t.description }
func (t lspRenameTool) Name() string                { return t.name }
func (t lspRenameTool) Description() string         { return t.description }

func (t lspPathTool) Parameters() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","description":"Path to the source file"}},"required":["path"]}`)
}

func (t lspPositionTool) Parameters() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","description":"Path to the source file"},"line":{"type":"integer","description":"1-based line number"},"character":{"type":"integer","description":"1-based character number"}},"required":["path","line","character"]}`)
}

func (t lspRangeTool) Parameters() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","description":"Path to the source file"},"start_line":{"type":"integer","description":"1-based start line"},"start_character":{"type":"integer","description":"1-based start character"},"end_line":{"type":"integer","description":"1-based end line"},"end_character":{"type":"integer","description":"1-based end character"}},"required":["path","start_line","start_character","end_line","end_character"]}`)
}

func (t lspWorkspaceQueryTool) Parameters() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"query":{"type":"string","description":"Workspace symbol query"}},"required":["query"]}`)
}

func (t lspRenameTool) Parameters() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","description":"Path to the source file"},"line":{"type":"integer","description":"1-based line number"},"character":{"type":"integer","description":"1-based character number"},"new_name":{"type":"string","description":"Replacement symbol name"}},"required":["path","line","character","new_name"]}`)
}

func (t lspPathTool) Execute(ctx context.Context, input json.RawMessage) (Result, error) {
	var args struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("invalid input: %v", err)}, nil
	}
	path, err := t.resolvePath(args.Path)
	if err != nil {
		return Result{IsError: true, Content: err.Error()}, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := t.exec(ctx, t.workingDir, path)
	if err != nil {
		return Result{IsError: true, Content: err.Error()}, nil
	}
	return Result{Content: out}, nil
}

func (t lspPositionTool) Execute(ctx context.Context, input json.RawMessage) (Result, error) {
	var args struct {
		Path      string `json:"path"`
		Line      int    `json:"line"`
		Character int    `json:"character"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("invalid input: %v", err)}, nil
	}
	path, err := t.resolvePath(args.Path)
	if err != nil {
		return Result{IsError: true, Content: err.Error()}, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := t.exec(ctx, t.workingDir, path, lsp.Position{Line: args.Line, Character: args.Character})
	if err != nil {
		return Result{IsError: true, Content: err.Error()}, nil
	}
	return Result{Content: out}, nil
}

func (t lspRangeTool) Execute(ctx context.Context, input json.RawMessage) (Result, error) {
	var args struct {
		Path           string `json:"path"`
		StartLine      int    `json:"start_line"`
		StartCharacter int    `json:"start_character"`
		EndLine        int    `json:"end_line"`
		EndCharacter   int    `json:"end_character"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("invalid input: %v", err)}, nil
	}
	path, err := t.resolvePath(args.Path)
	if err != nil {
		return Result{IsError: true, Content: err.Error()}, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := t.exec(ctx, t.workingDir, path, lsp.Range{
		Start: lsp.Position{Line: args.StartLine, Character: args.StartCharacter},
		End:   lsp.Position{Line: args.EndLine, Character: args.EndCharacter},
	})
	if err != nil {
		return Result{IsError: true, Content: err.Error()}, nil
	}
	return Result{Content: out}, nil
}

func (t lspWorkspaceQueryTool) Execute(ctx context.Context, input json.RawMessage) (Result, error) {
	var args struct {
		Query string `json:"query"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("invalid input: %v", err)}, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := t.exec(ctx, t.workingDir, args.Query)
	if err != nil {
		return Result{IsError: true, Content: err.Error()}, nil
	}
	return Result{Content: out}, nil
}

func (t lspRenameTool) Execute(ctx context.Context, input json.RawMessage) (Result, error) {
	var args struct {
		Path      string `json:"path"`
		Line      int    `json:"line"`
		Character int    `json:"character"`
		NewName   string `json:"new_name"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("invalid input: %v", err)}, nil
	}
	path, err := resolveLSPToolPath(args.Path, t.workingDir, t.readSandbox)
	if err != nil {
		return Result{IsError: true, Content: err.Error()}, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := t.applyEdits(ctx, t.workingDir, path, lsp.Position{Line: args.Line, Character: args.Character}, args.NewName)
	if err != nil {
		return Result{IsError: true, Content: err.Error()}, nil
	}
	return Result{Content: out}, nil
}

func (t lspPathTool) resolvePath(path string) (string, error) {
	return resolveLSPToolPath(path, t.workingDir, t.sandboxCheck)
}

func (t lspPositionTool) resolvePath(path string) (string, error) {
	return resolveLSPToolPath(path, t.workingDir, t.sandboxCheck)
}

func (t lspRangeTool) resolvePath(path string) (string, error) {
	return resolveLSPToolPath(path, t.workingDir, t.sandboxCheck)
}

func resolveLSPToolPath(path, workingDir string, sandboxCheck AllowedPathChecker) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", fmt.Errorf("path is required")
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(workingDir, path)
	}
	if sandboxCheck != nil && !sandboxCheck(path) {
		return "", fmt.Errorf("error: path not allowed by sandbox policy")
	}
	return path, nil
}

func NewLSPTools(workingDir string, readSandbox, writeSandbox AllowedPathChecker) []Tool {
	return []Tool{
		lspPositionTool{
			name:         "lsp_hover",
			description:  "Get hover/type information for the symbol at a specific file position using a locally installed LSP server when available. Prefer this over text search for semantic questions.",
			workingDir:   workingDir,
			sandboxCheck: readSandbox,
			exec: func(ctx context.Context, workspace, path string, pos lsp.Position) (string, error) {
				text, err := lsp.Hover(ctx, workspace, path, pos)
				if err != nil {
					return "", err
				}
				if strings.TrimSpace(text) == "" {
					return "No hover information returned.", nil
				}
				return text, nil
			},
		},
		lspPositionTool{
			name:         "lsp_definition",
			description:  "Go to definition for the symbol at a specific file position using a locally installed LSP server. Prefer this over text search for supported languages.",
			workingDir:   workingDir,
			sandboxCheck: readSandbox,
			exec: func(ctx context.Context, workspace, path string, pos lsp.Position) (string, error) {
				locations, err := lsp.Definition(ctx, workspace, path, pos)
				if err != nil {
					return "", err
				}
				if len(locations) == 0 {
					return "No definition found.", nil
				}
				lines := make([]string, 0, len(locations))
				for _, loc := range locations {
					lines = append(lines, fmt.Sprintf("%s:%d:%d", loc.Path, loc.Range.Start.Line, loc.Range.Start.Character))
				}
				return strings.Join(lines, "\n"), nil
			},
		},
		lspPositionTool{
			name:         "lsp_references",
			description:  "Find references for the symbol at a specific file position using a locally installed LSP server.",
			workingDir:   workingDir,
			sandboxCheck: readSandbox,
			exec: func(ctx context.Context, workspace, path string, pos lsp.Position) (string, error) {
				locations, err := lsp.References(ctx, workspace, path, pos)
				if err != nil {
					return "", err
				}
				if len(locations) == 0 {
					return "No references found.", nil
				}
				lines := make([]string, 0, len(locations))
				for _, loc := range locations {
					lines = append(lines, fmt.Sprintf("%s:%d:%d", loc.Path, loc.Range.Start.Line, loc.Range.Start.Character))
				}
				return strings.Join(lines, "\n"), nil
			},
		},
		lspPathTool{
			name:         "lsp_symbols",
			description:  "List document symbols for a source file using a locally installed LSP server.",
			workingDir:   workingDir,
			sandboxCheck: readSandbox,
			exec: func(ctx context.Context, workspace, path string) (string, error) {
				symbols, err := lsp.DocumentSymbols(ctx, workspace, path)
				if err != nil {
					return "", err
				}
				if len(symbols) == 0 {
					return "No symbols returned.", nil
				}
				lines := make([]string, 0, len(symbols))
				for _, sym := range symbols {
					lines = append(lines, fmt.Sprintf("%s (%d) %d:%d-%d:%d", sym.Name, sym.Kind, sym.Range.Start.Line, sym.Range.Start.Character, sym.Range.End.Line, sym.Range.End.Character))
				}
				return strings.Join(lines, "\n"), nil
			},
		},
		lspWorkspaceQueryTool{
			name:        "lsp_workspace_symbols",
			description: "Search workspace symbols using a locally installed LSP server instead of broad text search when semantic symbol lookup is available.",
			workingDir:  workingDir,
			exec: func(ctx context.Context, workspace, query string) (string, error) {
				symbols, err := lsp.WorkspaceSymbols(ctx, workspace, query)
				if err != nil {
					return "", err
				}
				if len(symbols) == 0 {
					return "No workspace symbols returned.", nil
				}
				lines := make([]string, 0, len(symbols))
				for _, sym := range symbols {
					lines = append(lines, fmt.Sprintf("%s:%d:%d %s (%d)", sym.Path, sym.Range.Start.Line, sym.Range.Start.Character, sym.Name, sym.Kind))
				}
				return strings.Join(lines, "\n"), nil
			},
		},
		lspPathTool{
			name:         "lsp_diagnostics",
			description:  "Get diagnostics for a source file using a locally installed LSP server, including publishDiagnostics when the server pushes them.",
			workingDir:   workingDir,
			sandboxCheck: readSandbox,
			exec: func(ctx context.Context, workspace, path string) (string, error) {
				diagnostics, err := lsp.Diagnostics(ctx, workspace, path)
				if err != nil {
					return "", err
				}
				if len(diagnostics) == 0 {
					return "No diagnostics returned.", nil
				}
				lines := make([]string, 0, len(diagnostics))
				for _, diag := range diagnostics {
					line := fmt.Sprintf("L%d:%d [%d] %s", diag.Range.Start.Line, diag.Range.Start.Character, diag.Severity, diag.Message)
					if strings.TrimSpace(diag.Source) != "" {
						line += " (" + diag.Source + ")"
					}
					lines = append(lines, line)
				}
				return strings.Join(lines, "\n"), nil
			},
		},
		lspRangeTool{
			name:         "lsp_code_actions",
			description:  "List available code actions for a source range using a locally installed LSP server.",
			workingDir:   workingDir,
			sandboxCheck: readSandbox,
			exec: func(ctx context.Context, workspace, path string, rng lsp.Range) (string, error) {
				actions, err := lsp.CodeActions(ctx, workspace, path, rng)
				if err != nil {
					return "", err
				}
				if len(actions) == 0 {
					return "No code actions returned.", nil
				}
				lines := make([]string, 0, len(actions))
				for _, action := range actions {
					line := action.Title
					if strings.TrimSpace(action.Kind) != "" {
						line += " [" + action.Kind + "]"
					}
					if len(action.Edits) > 0 {
						line += fmt.Sprintf(" edits=%d", len(action.Edits))
					}
					if strings.TrimSpace(action.Command) != "" {
						line += " command=" + action.Command
					}
					lines = append(lines, line)
				}
				return strings.Join(lines, "\n"), nil
			},
		},
		lspRenameTool{
			name:         "lsp_rename",
			description:  "Rename a symbol using a locally installed LSP server and apply the returned workspace edits to allowed files.",
			workingDir:   workingDir,
			readSandbox:  readSandbox,
			writeSandbox: writeSandbox,
			applyEdits: func(ctx context.Context, workspace, path string, pos lsp.Position, newName string) (string, error) {
				edits, err := lsp.RenameEdits(ctx, workspace, path, pos, newName)
				if err != nil {
					return "", err
				}
				if len(edits) == 0 {
					return "No rename edits returned.", nil
				}
				return applyLSPFileEdits(edits, writeSandbox)
			},
		},
	}
}

func applyLSPFileEdits(edits []lsp.FileEdit, sandboxCheck AllowedPathChecker) (string, error) {
	summaries := make([]string, 0, len(edits))
	for _, fileEdit := range edits {
		if strings.TrimSpace(fileEdit.Path) == "" {
			continue
		}
		if sandboxCheck != nil && !sandboxCheck(fileEdit.Path) {
			return "", fmt.Errorf("error: path not allowed by sandbox policy: %s", fileEdit.Path)
		}
		content, err := os.ReadFile(fileEdit.Path)
		if err != nil {
			return "", err
		}
		next, count, err := applyTextEdits(string(content), fileEdit.Edits)
		if err != nil {
			return "", fmt.Errorf("%s: %w", fileEdit.Path, err)
		}
		if err := os.WriteFile(fileEdit.Path, []byte(next), 0o644); err != nil {
			return "", err
		}
		summaries = append(summaries, fmt.Sprintf("%s (%d edits)", fileEdit.Path, count))
	}
	if len(summaries) == 0 {
		return "No editable workspace changes returned.", nil
	}
	sort.Strings(summaries)
	return "Applied workspace edits:\n" + strings.Join(summaries, "\n"), nil
}

func applyTextEdits(content string, edits []lsp.TextEdit) (string, int, error) {
	type replacement struct {
		start   int
		end     int
		newText string
	}
	repls := make([]replacement, 0, len(edits))
	for _, edit := range edits {
		start, err := offsetForPosition(content, edit.Range.Start)
		if err != nil {
			return "", 0, err
		}
		end, err := offsetForPosition(content, edit.Range.End)
		if err != nil {
			return "", 0, err
		}
		if end < start {
			return "", 0, fmt.Errorf("invalid edit range %v", edit.Range)
		}
		repls = append(repls, replacement{start: start, end: end, newText: edit.NewText})
	}
	sort.Slice(repls, func(i, j int) bool {
		if repls[i].start == repls[j].start {
			return repls[i].end > repls[j].end
		}
		return repls[i].start > repls[j].start
	})
	next := content
	for _, repl := range repls {
		next = next[:repl.start] + repl.newText + next[repl.end:]
	}
	return next, len(repls), nil
}

func offsetForPosition(content string, pos lsp.Position) (int, error) {
	if pos.Line < 1 || pos.Character < 1 {
		return 0, fmt.Errorf("invalid position %d:%d", pos.Line, pos.Character)
	}
	offset := 0
	line := 1
	for offset < len(content) && line < pos.Line {
		r, size := utf8.DecodeRuneInString(content[offset:])
		offset += size
		if r == '\n' {
			line++
		}
	}
	if line != pos.Line {
		if line == pos.Line-1 && offset == len(content) {
			return len(content), nil
		}
		return 0, fmt.Errorf("line %d out of range", pos.Line)
	}
	targetUnits := pos.Character - 1
	units := 0
	for idx := offset; idx < len(content); {
		r, size := utf8.DecodeRuneInString(content[idx:])
		if r == '\n' {
			break
		}
		if units == targetUnits {
			return idx, nil
		}
		width := len(utf16.Encode([]rune{r}))
		if units+width > targetUnits {
			return idx, nil
		}
		units += width
		idx += size
		if units == targetUnits {
			return idx, nil
		}
	}
	if units == targetUnits {
		return offset + len(strings.SplitN(content[offset:], "\n", 2)[0]), nil
	}
	return 0, fmt.Errorf("character %d out of range on line %d", pos.Character, pos.Line)
}
