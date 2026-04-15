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
	return "Read the contents of a file. Returns text content or image data (for png/jpg/jpeg/gif/webp files)."
}

func (t ReadFile) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"path": {
				"type": "string",
				"description": "Path to the file to read"
			}
		},
		"required": ["path"]
	}`)
}

func (t ReadFile) Execute(ctx context.Context, input json.RawMessage) (Result, error) {
	var args struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("invalid input: %v", err)}, nil
	}

	if t.SandboxCheck != nil && !t.SandboxCheck(args.Path) {
		return Result{IsError: true, Content: "Error: path not allowed by sandbox policy"}, nil
	}

	data, err := os.ReadFile(args.Path)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("error reading file: %v", err)}, nil
	}

	// Image file handling: decode and return as multimodal result.
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

	content := string(data)
	content = truncateFileContent(content)

	return Result{Content: content}, nil
}

const (
	maxFileLines = 500
	maxFileChars = 50000
)

func truncateFileContent(content string) string {
	lines := strings.Split(content, "\n")
	if len(lines) > maxFileLines {
		truncated := strings.Join(lines[:maxFileLines], "\n")
		return truncated + "\n\n[File truncated: showing first 500 of " + fmt.Sprintf("%d", len(lines)) + " lines. Use line_range or glob to read specific sections.]"
	}
	if len(content) > maxFileChars {
		// UTF-8 safe truncation: find the last valid rune boundary before maxFileChars
		truncated := content[:maxFileChars]
		for i := maxFileChars - 1; i >= 0; i-- {
			if truncated[i]&0xC0 != 0x80 {
				truncated = content[:i+1]
				break
			}
		}
		return truncated + "\n\n[File truncated: showing first " + fmt.Sprintf("%d", maxFileChars) + " of " + fmt.Sprintf("%d", len(content)) + " characters. Use line_range or glob to read specific sections.]"
	}
	return content
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
