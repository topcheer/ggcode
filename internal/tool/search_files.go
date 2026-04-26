package tool

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/topcheer/ggcode/internal/safego"
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

	// Try git grep fast path when inside a git repo
	if results, totalMatches, ok := t.gitGrepSearch(ctx, args, re); ok {
		return formatResults(results, totalMatches), nil
	}

	// Fallback: parallel Go search with gitignore + binary detection
	return t.parallelSearch(ctx, args, re)
}

// gitGrepSearch attempts to use git grep for fast searching.
// Returns results, totalMatches, true on success; nil, 0, false if not in git repo.
func (t SearchFiles) gitGrepSearch(ctx context.Context, args struct {
	Pattern        string `json:"pattern"`
	Directory      string `json:"directory"`
	IncludePattern string `json:"include_pattern"`
	MaxResults     int    `json:"max_results"`
}, re *regexp.Regexp) ([]string, int, bool) {
	// Check if we're in a git repo
	if gitTrackedFiles(ctx, args.Directory) == nil {
		return nil, 0, false
	}

	gitArgs := []string{"grep", "--no-index", "-n", "-E", "--"}
	if args.IncludePattern != "" {
		gitArgs = append(gitArgs, args.Pattern, "--", args.IncludePattern)
	} else {
		gitArgs = append(gitArgs, args.Pattern)
	}

	cmd := gitCommand(ctx, gitArgs...)
	cmd.Dir = args.Directory
	out, err := cmd.Output()
	if err != nil {
		// git grep returns exit code 1 when no matches found
		if len(out) == 0 {
			return nil, 0, true // in git repo, just no results
		}
	}

	lines := strings.Split(string(out), "\n")
	var results []string
	totalMatches := 0
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		totalMatches++
		if len(results) < args.MaxResults {
			results = append(results, line)
		}
	}
	return results, totalMatches, true
}

// parallelSearch performs parallel file search with gitignore filtering.
func (t SearchFiles) parallelSearch(ctx context.Context, args struct {
	Pattern        string `json:"pattern"`
	Directory      string `json:"directory"`
	IncludePattern string `json:"include_pattern"`
	MaxResults     int    `json:"max_results"`
}, re *regexp.Regexp) (Result, error) {
	// Get git-tracked files if available
	tracked := gitTrackedFiles(ctx, args.Directory)

	// Phase 1: collect file paths
	var files []string
	filepath.WalkDir(args.Directory, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			name := d.Name()
			if skipDirs[name] {
				return filepath.SkipDir
			}
			return nil
		}

		relPath, _ := filepath.Rel(args.Directory, path)

		// Filter by gitignore if available
		if tracked != nil {
			if _, ok := tracked[filepath.ToSlash(relPath)]; !ok {
				return nil
			}
		}

		// Apply include glob filter
		if args.IncludePattern != "" {
			matched, err := filepath.Match(args.IncludePattern, d.Name())
			if err != nil || !matched {
				return nil
			}
		}

		// Skip binary files
		if isBinaryFile(path) {
			return nil
		}

		files = append(files, path)
		return nil
	})

	if len(files) == 0 {
		return Result{Content: "No matches found."}, nil
	}

	// Phase 2: parallel search
	numWorkers := runtime.NumCPU()
	if numWorkers > 8 {
		numWorkers = 8
	}
	if numWorkers > len(files) {
		numWorkers = len(files)
	}

	type match struct {
		line string
	}
	var (
		results      []string
		totalMatches int64
		mu           sync.Mutex
		fileQueue    = make(chan string, len(files))
		maxReached   atomic.Bool
	)

	for _, f := range files {
		fileQueue <- f
	}
	close(fileQueue)

	var wg sync.WaitGroup
	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		safego.Go("tool.search.worker", func() {
			defer wg.Done()
			for path := range fileQueue {
				if maxReached.Load() {
					return
				}
				localMatches := searchFile(path, args.Directory, re)
				if len(localMatches) == 0 {
					continue
				}
				atomic.AddInt64(&totalMatches, int64(len(localMatches)))

				mu.Lock()
				for _, m := range localMatches {
					if len(results) >= args.MaxResults {
						maxReached.Store(true)
						break
					}
					results = append(results, m)
				}
				mu.Unlock()

				if maxReached.Load() {
					return
				}
			}
		})
	}
	wg.Wait()

	if len(results) == 0 {
		return Result{Content: "No matches found."}, nil
	}

	total := int(atomic.LoadInt64(&totalMatches))
	return formatResults(results, total), nil
}

func searchFile(path, baseDir string, re *regexp.Regexp) []string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	var matches []string
	scanner := bufio.NewScanner(f)
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		line := scanner.Text()
		if re.MatchString(line) {
			relPath, _ := filepath.Rel(baseDir, path)
			matches = append(matches, fmt.Sprintf("%s:%d: %s", relPath, lineNum, line))
		}
	}
	return matches
}

func formatResults(results []string, totalMatches int) Result {
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
	return Result{Content: sb.String()}
}
