package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/topcheer/ggcode/internal/image"
	"github.com/topcheer/ggcode/internal/provider"
)

// AllowedPathChecker is a function that checks if a path is allowed by sandbox policy.
type AllowedPathChecker func(path string) bool

// ReadFile implements the read_file tool.
type ReadFile struct {
	SandboxCheck AllowedPathChecker
}

func (t ReadFile) Name() string { return "read_file" }

func (t ReadFile) Description() string {
	return "Read file contents into the agent's context. ALWAYS read a file with this tool before editing it. " +
		"Output is line-numbered using `cat -n` format (\"   42\\t<line content>\"). Recommended workflow: copy those numbered lines directly into edit_file/multi_edit_file old_text, because the edit tools understand the prefixes and use them as anchors for single-line edits and duplicate text. " +
		"Use offset/limit for files larger than ~2000 lines. " +
		"Supports text, images (png/jpg/gif/webp), PDF, Office (docx/xlsx/pptx), OpenDocument (odt/ods/odp), EPUB, RTF, archives (zip/tar/tar.gz/tar.bz2), iWork, and SVG."
}

func (t ReadFile) Parameters() json.RawMessage {
	return json.RawMessage(`{
	"type": "object",
	"properties": {
		"path": {
			"type": "string",
			"description": "Absolute path to the file to read."
		},
		"offset": {
			"type": "integer",
			"description": "1-based line number to start reading from. Only applies to text files and extracted documents."
		},
		"limit": {
			"type": "integer",
			"description": "Maximum number of lines to read. Output is capped at 2000 lines when no limit is supplied."
		},
		"description": {
			"type": "string",
			"description": "Optional. Brief activity label shown in the UI in the user's language."
		}
	},
	"required": [
		"path"
	]
}`)
}

const maxFileSize = 10 * 1024 * 1024 // 10MB pre-check limit

func (t ReadFile) Execute(ctx context.Context, input json.RawMessage) (Result, error) {
	var args struct {
		Path   string `json:"path"`
		Offset int    `json:"offset"`
		Limit  int    `json:"limit"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("invalid input: %v", err)}, nil
	}

	if t.SandboxCheck != nil && !t.SandboxCheck(args.Path) {
		return Result{IsError: true, Content: "Error: path not allowed by sandbox policy"}, nil
	}

	// Pre-check file size — but allow range reads of large files via offset/limit
	info, err := os.Stat(args.Path)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("error accessing file: %v", err)}, nil
	}
	if info.Size() > maxFileSize {
		if args.Offset > 0 || args.Limit > 0 {
			// Streaming range read: only read the requested lines
			text, err := readFileRangeStreaming(args.Path, args.Offset, args.Limit, readFileRangeOptions{
				defaultLimit: maxOutputLines,
				moreHint:     "Use read_file with offset/limit for more.",
			})
			if err != nil {
				return Result{IsError: true, Content: err.Error()}, nil
			}
			return Result{Content: text}, nil
		}
		// Count lines so the agent knows the range to use
		lineCount := countFileLines(args.Path)
		return Result{IsError: true, Content: fmt.Sprintf(
			"file too large (%d MB, ~%d lines). Use read_file with offset and limit for range reading (e.g. offset=1 limit=500 to read the first 500 lines).",
			info.Size()/(1024*1024), lineCount,
		)}, nil
	}

	data, err := os.ReadFile(args.Path)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("error reading file: %v", err)}, nil
	}

	// Image file handling: decode and return as multimodal result (ignores offset/limit).
	if image.IsImageFile(args.Path) {
		img, err := image.Decode(data)
		if err != nil {
			return Result{IsError: true, Content: fmt.Sprintf("error decoding image: %v", err)}, nil
		}
		b64 := image.EncodeBase64(img)
		return Result{
			Content: image.Placeholder(filepath.Base(args.Path), img),
			Images: []ResultImage{{
				MIME:       img.MIME,
				Base64:     b64,
				Width:      img.Width,
				Height:     img.Height,
				SourcePath: args.Path,
			}},
		}, nil
	}

	text, err := readTextContentFromBytes(args.Path, data, args.Offset, args.Limit, readFileRangeOptions{
		defaultLimit: maxOutputLines,
		moreHint:     "Use read_file with offset/limit for more.",
	})
	if err != nil {
		return Result{IsError: true, Content: err.Error()}, nil
	}
	return Result{Content: text}, nil
}

// detectIndentStyle analyzes the first ~200 lines to determine tab vs space indentation.
func detectIndentStyle(content string) string {
	tabs, spaces := 0, 0
	lines := strings.Split(content, "\n")
	limit := len(lines)
	if limit > 200 {
		limit = 200
	}
	for _, line := range lines[:limit] {
		if len(line) == 0 || (line[0] != ' ' && line[0] != '\t') {
			continue
		}
		if line[0] == '\t' {
			tabs++
		} else {
			i := 0
			for i < len(line) && line[i] == ' ' {
				i++
			}
			if i >= 2 {
				spaces++
			}
		}
	}
	total := tabs + spaces
	if total == 0 {
		return ""
	}
	if tabs > spaces {
		return "indent: tab"
	}
	// Detect base indent unit by finding the greatest common divisor
	// of all leading-space counts. This handles 2-space YAML vs 4-space Python/etc.
	widths := map[int]int{}
	for _, line := range lines[:limit] {
		if len(line) == 0 || line[0] != ' ' {
			continue
		}
		i := 0
		for i < len(line) && line[i] == ' ' {
			i++
		}
		if i >= 2 {
			widths[i]++
		}
	}
	if len(widths) == 0 {
		return "indent: spaces"
	}
	// Find GCD of all observed widths
	allWidths := make([]int, 0, len(widths))
	for w := range widths {
		allWidths = append(allWidths, w)
	}
	g := allWidths[0]
	for _, w := range allWidths[1:] {
		g = gcd(g, w)
	}
	if g < 2 {
		g = 2
	}
	return fmt.Sprintf("indent: %d spaces", g)
}

func gcd(a, b int) int {
	for b != 0 {
		a, b = b, a%b
	}
	return a
}

// detectEncoding checks for BOM or non-UTF-8 content.
func detectEncoding(data []byte) string {
	if len(data) >= 3 && data[0] == 0xEF && data[1] == 0xBB && data[2] == 0xBF {
		return "encoding: UTF-8 BOM"
	}
	for _, b := range data[:min(len(data), 512)] {
		if b == 0 {
			return "encoding: binary/UTF-16"
		}
	}
	return ""
}

// --- provider.ToolDefinition helper ---

// ToolDef creates a provider.ToolDefinition from a Tool.
func ToolDef(t Tool) provider.ToolDefinition {
	return provider.ToolDefinition{
		Name:        t.Name(),
		Description: t.Description(),
		Parameters:  t.Parameters(),
	}
}
