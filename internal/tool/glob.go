package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type Glob struct {
	SandboxCheck AllowedPathChecker
}

func (t Glob) Name() string { return "glob" }

func (t Glob) Description() string {
	return "Find files matching a glob pattern in a directory. Supports ** for recursive matching. Use this when you only need file paths; use search_files or grep when you need to search file contents."
}

func (t Glob) Parameters() json.RawMessage {
	return json.RawMessage(`{
	"type": "object",
	"properties": {
		"pattern": {
			"type": "string",
			"description": "Glob pattern for path matching only (e.g., '**/*.go', 'src/**/*.js'). Use search_files or grep for content search."
		},
		"directory": {
			"type": "string",
			"description": "Base directory to search in (default: current directory)"
		},
		"description": {
			"type": "string",
			"description": "REQUIRED. Brief activity label shown in the UI, in the user's language."
		}
	},
	"required": [
		"pattern",
		"description"
	]
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

	tracked := gitTrackedFiles(ctx, args.Directory)

	pattern := filepath.Join(args.Directory, args.Pattern)

	if t.SandboxCheck != nil {
		joined := filepath.Clean(pattern)
		if !t.SandboxCheck(joined) {
			return Result{IsError: true, Content: fmt.Sprintf("Error: glob pattern %q escapes the allowed directory", args.Pattern)}, nil
		}
	}
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("glob error: %v", err)}, nil
	}

	if strings.Contains(args.Pattern, "**") {
		matches = walkGlob(args.Directory, args.Pattern, tracked)
	} else if tracked != nil {

		filtered := filterByTracked(matches, args.Directory, tracked)
		matches = filtered
	}

	if len(matches) == 0 {
		var hints []string
		hints = append(hints, fmt.Sprintf("No files matched pattern %q in %q.", args.Pattern, args.Directory))
		if strings.Contains(args.Pattern, "**") {
			hints = append(hints, "The ** wildcard requires recursive matching — verify the base directory has subdirectories.")
		}
		if !strings.ContainsAny(args.Pattern, "*?[") {
			hints = append(hints, "Your pattern has no wildcards — try adding * (e.g., '*.go') or ** for recursive search.")
		}

		hints = append(hints, fmt.Sprintf("Try list_directory on %q to see available files, or broaden the pattern.", args.Directory))
		return Result{Content: strings.Join(hints, "\n")}, nil
	}

	for i, m := range matches {
		rel, err := filepath.Rel(args.Directory, m)
		if err == nil {
			matches[i] = rel
		}
	}

	sort.Strings(matches)

	const maxGlobResults = 500
	totalCount := len(matches)
	if totalCount > maxGlobResults {
		matches = matches[:maxGlobResults]
	}

	result := strings.Join(matches, "\n")
	if totalCount > maxGlobResults {
		result += fmt.Sprintf("\n... [%d more files matched — refine your pattern to see them]", totalCount-maxGlobResults)
	}

	return Result{Content: result}, nil
}

func filterByTracked(matches []string, baseDir string, tracked map[string]struct{}) []string {
	var filtered []string
	for _, m := range matches {
		rel, err := filepath.Rel(baseDir, m)
		if err != nil {
			continue
		}
		if _, ok := tracked[filepath.ToSlash(rel)]; ok {
			filtered = append(filtered, m)
		}
	}
	return filtered
}

func walkGlob(baseDir, pattern string, tracked map[string]struct{}) []string {

	parts := strings.SplitN(pattern, "**", 2)
	if len(parts) < 2 {
		return nil
	}
	prefix, suffix := parts[0], parts[1]

	var results []string

	startDirs := []string{filepath.Join(baseDir, prefix)}
	if strings.ContainsAny(prefix, "*?[") {
		if expanded, err := filepath.Glob(filepath.Join(baseDir, prefix)); err == nil && len(expanded) > 0 {
			startDirs = expanded
		}
	}
	for _, startDir := range startDirs {
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

			if tracked != nil {
				rel, _ := filepath.Rel(baseDir, path)
				if _, ok := tracked[filepath.ToSlash(rel)]; !ok {
					return nil
				}
			}

			rel, _ := filepath.Rel(startDir, path)
			relSlash := filepath.ToSlash(rel)
			suffixSlash := strings.TrimPrefix(filepath.ToSlash(suffix), "/")
			matched := matchStarStarSuffix(suffixSlash, relSlash)

			if matched {
				results = append(results, path)
			}
			return nil
		})
	}

	return results
}

func matchStarStarSuffix(suffix, rel string) bool {
	if suffix == "" {
		return true
	}
	if !strings.Contains(suffix, "/") {

		if strings.ContainsAny(suffix, "*?[") {
			ok, _ := filepath.Match(suffix, filepath.Base(rel))
			return ok
		}
		return rel == suffix || strings.HasSuffix(rel, "/"+suffix)
	}

	sSegs := strings.Split(suffix, "/")
	rSegs := strings.Split(rel, "/")
	for i := 0; i < len(rSegs); i++ {
		if matchSegs(sSegs, rSegs[i:]) {
			return true
		}
	}
	return false
}

func matchSegs(sSegs, rSegs []string) bool {
	if len(sSegs) == 0 {
		return true
	}
	if sSegs[0] == "**" {
		for i := 0; i <= len(rSegs); i++ {
			if matchSegs(sSegs[1:], rSegs[i:]) {
				return true
			}
		}
		return false
	}
	if len(rSegs) == 0 {
		return false
	}
	ok, err := filepath.Match(sSegs[0], rSegs[0])
	if err != nil || !ok {
		return false
	}
	return matchSegs(sSegs[1:], rSegs[1:])
}
