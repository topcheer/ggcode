package tool

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

)

// SearchFiles implements the search_files tool (grep-like content search).
type SearchFiles struct {
	SandboxCheck AllowedPathChecker
}

func (t SearchFiles) Name() string { return "search_files" }

func (t SearchFiles) Description() string {
	return "Search for a regex pattern in files within a directory. Returns matching lines with file paths and line numbers."
}

func (t SearchFiles) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"pattern": {
				"type": "string",
				"description": "Regular expression to search for"
			},
			"directory": {
				"type": "string",
				"description": "Directory to search in (default: current directory)"
			},
			"include_pattern": {
				"type": "string",
				"description": "Glob pattern to filter files (e.g., '*.go')"
			},
			"max_results": {
				"type": "integer",
				"description": "Maximum number of matches to return (default: 50)"
			}
		},
		"required": ["pattern"]
	}`)
}

func (t SearchFiles) Execute(ctx context.Context, input json.RawMessage) (Result, error) {
	var args struct {
		Pattern        string `json:"pattern"`
		Directory      string `json:"directory"`
		IncludePattern string `json:"include_pattern"`
		MaxResults     int    `json:"max_results"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("invalid input: %v", err)}, nil
	}

	if t.SandboxCheck != nil && !t.SandboxCheck(args.Directory) {
		return Result{IsError: true, Content: "Error: path not allowed by sandbox policy"}, nil
	}

	if args.Directory == "" {
		args.Directory = "."
	}
	if args.MaxResults <= 0 {
		args.MaxResults = 50
	}

	re, err := regexp.Compile(args.Pattern)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("invalid regex: %v", err)}, nil
	}

	var results []string
	totalMatches := 0

	err = filepath.WalkDir(args.Directory, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // skip errors
		}
		if d.IsDir() {
			// Skip common ignore directories
			name := d.Name()
			if name == ".git" || name == "node_modules" || name == "vendor" || name == "__pycache__" {
				return filepath.SkipDir
			}
			return nil
		}

		// Apply include glob filter
		if args.IncludePattern != "" {
			matched, err := filepath.Match(args.IncludePattern, d.Name())
			if err != nil || !matched {
				return nil
			}
		}

		f, err := os.Open(path)
		if err != nil {
			return nil
		}
		defer f.Close()

		scanner := bufio.NewScanner(f)
		lineNum := 0
		for scanner.Scan() {
			lineNum++
			line := scanner.Text()
			if re.MatchString(line) {
				totalMatches++
				relPath, _ := filepath.Rel(args.Directory, path)
				results = append(results, fmt.Sprintf("%s:%d: %s", relPath, lineNum, line))
				if len(results) >= args.MaxResults {
					return fmt.Errorf("max results reached")
				}
			}
		}
		return nil
	})

	if err != nil && err.Error() != "max results reached" {
		return Result{IsError: true, Content: fmt.Sprintf("search error: %v", err)}, nil
	}

	if len(results) == 0 {
		return Result{Content: "No matches found."}, nil
	}

	var sb strings.Builder
	if totalMatches > len(results) {
		fmt.Fprintf(&sb, "Showing %d of %d matches:\n\n", len(results), totalMatches)
	} else {
		fmt.Fprintf(&sb, "Found %d matches:\n\n", totalMatches)
	}
	for _, r := range results {
		sb.WriteString(r)
		sb.WriteByte('\n')
	}

	return Result{Content: sb.String()}, nil
}
