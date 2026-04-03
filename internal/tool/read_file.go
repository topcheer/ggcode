package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

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
	return "Read the contents of a file. Returns the file content as text."
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

	return Result{Content: string(data)}, nil
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
