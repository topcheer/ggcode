package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Glob implements the glob tool for file path matching.
type Glob struct {
	SandboxCheck AllowedPathChecker
}

func (t Glob) Name() string { return "glob" }

func (t Glob) Description() string {
	return "Find files matching a glob pattern in a directory. Supports ** for recursive matching."
}

func (t Glob) Parameters() json.RawMessage {
	return json.RawMessage(`{
			"type": "object",
			"properties": {
				"pattern": {
					"type": "string",
					"description": "Glob pattern (e.g., '**/*.go', 'src/**/*.js')"
				},
				"directory": {
					"type": "string",
					"description": "Base directory to search in (default: current directory)"
				}
			},
			"required": ["pattern"]
		}`)
}

func (t Glob) Execute(ctx context.Context, input json.RawMessage) (Result, error) {
	var args struct {
		Pattern   string `json:"pattern"`
		Directory string `json:"directory"`
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

	// Get git-tracked files for gitignore filtering
	tracked := gitTrackedFiles(ctx, args.Directory)

	// Support ** for recursive matching via filepath.Glob doublestar
	pattern := filepath.Join(args.Directory, args.Pattern)
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("glob error: %v", err)}, nil
	}

	// filepath.Glob doesn't support ** natively in all Go versions,
	// so also do a manual recursive walk for patterns containing **
	if len(matches) == 0 && strings.Contains(args.Pattern, "**") {
		matches = walkGlob(args.Directory, args.Pattern, tracked)
	} else if tracked != nil {
		// Filter by gitignore
		filtered := filterByTracked(matches, tracked)
		matches = filtered
	}

	if len(matches) == 0 {
		return Result{Content: "No files matched the pattern."}, nil
	}

	// Make paths relative
	for i, m := range matches {
		rel, err := filepath.Rel(args.Directory, m)
		if err == nil {
			matches[i] = rel
		}
	}

	return Result{Content: strings.Join(matches, "\n")}, nil
}

// filterByTracked removes files not in the git-tracked set.
func filterByTracked(matches []string, tracked map[string]struct{}) []string {
	var filtered []string
	for _, m := range matches {
		// tracked keys are relative paths; m may be absolute
		for key := range tracked {
			if strings.HasSuffix(m, key) {
				filtered = append(filtered, m)
				break
			}
		}
	}
	return filtered
}

// walkGlob handles ** patterns by walking the directory tree.
func walkGlob(baseDir, pattern string, tracked map[string]struct{}) []string {
	// Split pattern on **
	parts := strings.SplitN(pattern, "**", 2)
	if len(parts) < 2 {
		return nil
	}
	prefix, suffix := parts[0], parts[1]

	var results []string

	// Walk the tree looking for matches
	startDir := filepath.Join(baseDir, prefix)
	filepath.WalkDir(startDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if skipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}

		// Filter by gitignore if available
		if tracked != nil {
			rel, _ := filepath.Rel(baseDir, path)
			if _, ok := tracked[filepath.ToSlash(rel)]; !ok {
				return nil
			}
		}

		// Remove prefix, try matching suffix against remainder
		rel, _ := filepath.Rel(startDir, path)
		matched, _ := filepath.Match(suffix, rel)
		if !matched {
			// Also try matching with any sub-path
			matched, _ = filepath.Match(strings.TrimPrefix(suffix, string(filepath.Separator)), rel)
		}
		if matched {
			absPath, _ := filepath.Abs(path)
			results = append(results, absPath)
		}
		return nil
	})

	return results
}
