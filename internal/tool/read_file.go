package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/topcheer/ggcode/internal/extract"
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
	return "Read file contents. Supports text files, images (png/jpg/gif/webp), PDF, Office documents (docx/xlsx/pptx), OpenDocument (odt/ods/odp), EPUB, and RTF. Use offset and limit for range reading large files."
}

func (t ReadFile) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"path": {
				"type": "string",
				"description": "Absolute path to the file to read"
			},
			"offset": {
				"type": "integer",
				"description": "Line number to start reading from (1-based). Only applies to text files and extracted documents."
			},
			"limit": {
				"type": "integer",
				"description": "Maximum number of lines to read."
			}
		},
		"required": ["path"]
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

	// Pre-check file size
	info, err := os.Stat(args.Path)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("error accessing file: %v", err)}, nil
	}
	if info.Size() > maxFileSize {
		return Result{IsError: true, Content: fmt.Sprintf("file too large (%d MB). Use read_file with offset/limit for range reading.", info.Size()/(1024*1024))}, nil
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

	// Document file handling: extract text, then apply range reading.
	if extract.IsDocumentFile(args.Path) {
		result, err := extract.Extract(args.Path, data)
		if err != nil {
			return Result{IsError: true, Content: fmt.Sprintf("error extracting document text: %v", err)}, nil
		}

		var header string
		if result.Pages > 0 {
			header = fmt.Sprintf("[Extracted from %s, %d pages]\n", result.Format, result.Pages)
		} else {
			header = fmt.Sprintf("[Extracted from %s]\n", result.Format)
		}

		text := readFileRange(result.Text, args.Offset, args.Limit, 0)
		return Result{Content: header + text}, nil
	}

	// Plain text file: apply range reading.
	content := string(data)
	text := readFileRange(content, args.Offset, args.Limit, 0)
	return Result{Content: text}, nil
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
