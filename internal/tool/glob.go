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

// Glob implements the glob tool for file path matching.
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

	// Get git-tracked files for gitignore filtering
	tracked := gitTrackedFiles(ctx, args.Directory)

	// Support ** for recursive matching via filepath.Glob doublestar
	pattern := filepath.Join(args.Directory, args.Pattern)
	// #1690 case 3: the pattern itself was never sandbox-checked - "../../*"
	// joined its way out of the allowed directory and Glob happily walked
	// it. Clean the joined path and require it to stay inside.
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

	// #829: filepath.Glob treats ** as a single * (one segment). For
	// '**/*.go' Glob returns depth-1 files only, so the old
	// 'len(matches)==0 && Contains(**)' guard never fired and deep files
	// were silently missing. Any ** pattern goes to walkGlob unconditionally.
	if strings.Contains(args.Pattern, "**") {
		matches = walkGlob(args.Directory, args.Pattern, tracked)
	} else if tracked != nil {
		// Filter by gitignore
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
		// Suggest listing the directory to see what's there
		hints = append(hints, fmt.Sprintf("Try list_directory on %q to see available files, or broaden the pattern.", args.Directory))
		return Result{Content: strings.Join(hints, "\n")}, nil
	}

	// Make paths relative
	for i, m := range matches {
		rel, err := filepath.Rel(args.Directory, m)
		if err == nil {
			matches[i] = rel
		}
	}

	// Sort matches alphabetically for predictable output
	sort.Strings(matches)

	// Cap results to avoid flooding context with large file lists
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

// filterByTracked removes files not in the git-tracked set.
// tracked keys are slash-separated paths relative to baseDir (git ls-files
// output), so membership must be an exact relative-path lookup, not a
// suffix match. HasSuffix(".../build/main.go", "main.go") is true even when
// only the root main.go is tracked, which let ignored/untracked files slip
// through the gitignore filter.
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

// walkGlob handles ** patterns by walking the directory tree.
func walkGlob(baseDir, pattern string, tracked map[string]struct{}) []string {
	// Split pattern on the FIRST ** : prefix anchors the walk root, and the
	// (possibly still **-containing) remainder is matched segment-wise below.
	// #4549: only the first split is taken on purpose — matchStarStarSuffix
	// now handles any number of additional ** segments in the remainder
	// recursively, so multi-** patterns no longer silently mis-match.
	parts := strings.SplitN(pattern, "**", 2)
	if len(parts) < 2 {
		return nil
	}
	prefix, suffix := parts[0], parts[1]

	var results []string

	// #4705: a prefix carrying its own wildcards (*/**/*.go, pkg-*/**/x.go)
	// used to be Joined literally into a nonexistent startDir - WalkDir then
	// delivered one root error the callback swallowed, producing a silent
	// zero-match with a misleading "no subdirectories" hint. Expand the
	// prefix into concrete directories first and walk each.
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

			// Filter by gitignore if available
			if tracked != nil {
				rel, _ := filepath.Rel(baseDir, path)
				if _, ok := tracked[filepath.ToSlash(rel)]; !ok {
					return nil
				}
			}

			// Remove prefix, try matching suffix against remainder.
			// #829: filepath.Match's * does not cross '/', so a suffix like
			// '/*.go' never matched 'deep/two.go'. Match segment-wise, allowing
			// '**' to absorb any number of leading segments.
			rel, _ := filepath.Rel(startDir, path)
			relSlash := filepath.ToSlash(rel)
			suffixSlash := strings.TrimPrefix(filepath.ToSlash(suffix), "/")
			matched := matchStarStarSuffix(suffixSlash, relSlash)
			// #4549: store the walk-relative path, NOT filepath.Abs(path).
			// The caller converts results via filepath.Rel(args.Directory, m);
			// with a default directory of "." Rel(".", "/abs/path") always
			// errors and the absolute path leaked through, breaking the
			// relative-path output contract shared with filepath.Glob.
			if matched {
				results = append(results, path)
			}
			return nil
		})
	}

	return results
}

// matchStarStarSuffix matches rel against a '**/'-style suffix where **
// absorbs any number of leading segments (#829). #4549: a suffix may itself
// contain further '**' segments (e.g. pattern "**/docs/**/*.md" leaves
// suffix "docs/**/*.md"); those absorb zero or more whole segments via
// matchSegs. The suffix still aligns at any position of rel, preserving the
// original "first ** absorbs the prefix" semantics.
func matchStarStarSuffix(suffix, rel string) bool {
	if suffix == "" {
		return true
	}
	if !strings.Contains(suffix, "/") {
		// Single-segment suffix with wildcards (e.g. '*.go'): * does not
		// cross '/', so match the BASE segment ('sub/nested.go' vs '*.go')
		// or, for literal names, any tail segment.
		if strings.ContainsAny(suffix, "*?[") {
			ok, _ := filepath.Match(suffix, filepath.Base(rel))
			return ok
		}
		return rel == suffix || strings.HasSuffix(rel, "/"+suffix)
	}
	// Multi-segment suffix: try aligning it at every position of rel (the
	// split-off leading '**' absorbs any number of leading segments), and let
	// literal '**' segments inside the suffix absorb any run of segments.
	sSegs := strings.Split(suffix, "/")
	rSegs := strings.Split(rel, "/")
	for i := 0; i < len(rSegs); i++ {
		if matchSegs(sSegs, rSegs[i:]) {
			return true
		}
	}
	return false
}

// matchSegs matches suffix segments against rel segments. A literal "**"
// segment absorbs zero or more whole segments; other segments match via
// filepath.Match (single-segment wildcards only). An empty suffix segment
// list matches any remaining tail.
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
