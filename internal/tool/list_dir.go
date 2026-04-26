package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"sort"
	"strings"
)

// ListDir implements the list_directory tool.
type ListDir struct {
	SandboxCheck AllowedPathChecker
}

func (t ListDir) Name() string { return "list_directory" }

func (t ListDir) Description() string {
	return "List files and directories at a given path. Returns names, types, and sizes."
}

func (t ListDir) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"path": {
				"type": "string",
				"description": "Directory path to list (default: current directory)"
			}
		},
		"required": ["path"]
	}`)
}

func (t ListDir) Execute(ctx context.Context, input json.RawMessage) (Result, error) {
	var args struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("invalid input: %v", err)}, nil
	}

	if t.SandboxCheck != nil && !t.SandboxCheck(args.Path) {
		return Result{IsError: true, Content: "Error: path not allowed by sandbox policy"}, nil
	}

	if args.Path == "" {
		args.Path = "."
	}

	entries, err := os.ReadDir(args.Path)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("error listing directory: %v", err)}, nil
	}

	// Sort: directories first, then files
	sort.Slice(entries, func(i, j int) bool {
		fi, _ := entries[i].Info()
		fj, _ := entries[j].Info()
		di := fi != nil && fi.IsDir()
		dj := fj != nil && fj.IsDir()
		if di != dj {
			return di
		}
		return entries[i].Name() < entries[j].Name()
	})

	var sb strings.Builder
	for _, e := range entries {
		info, _ := e.Info()
		typ := "file"
		size := ""
		if info != nil {
			if info.IsDir() {
				typ = "dir"
			} else if info.Mode()&fs.ModeSymlink != 0 {
				typ = "symlink"
			}
			if !info.IsDir() {
				size = fmt.Sprintf(" (%d bytes)", info.Size())
			}
		}
		fmt.Fprintf(&sb, "%s  [%s]%s\n", e.Name(), typ, size)
	}

	return Result{Content: sb.String()}, nil
}
