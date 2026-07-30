package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/topcheer/ggcode/internal/safego"
)

// CodeSearch implements a semantic code search tool using BM25 ranking.
// Unlike grep (exact regex matching), it ranks files by relevance to a
// natural-language query — finding "where authentication is handled" even
// when the exact keywords don't appear in the code.
//
// The index is maintained by CodeIndexManager in the background. If the
// index is not yet ready, the tool returns an error directing the LLM
// to retry shortly — it never blocks the agent loop to build the index.
type CodeSearch struct {
	SandboxCheck AllowedPathChecker
	Index        *CodeIndexManager
}

func (t CodeSearch) Name() string { return "code_search" }

func (t CodeSearch) Description() string {
	return "Semantic code search using BM25 relevance ranking. Finds files relevant to a natural-language query (e.g., 'authentication error handling', 'cron job scheduling'). Ranks files by relevance, not exact keyword match. Use grep for exact regex patterns; use code_search when you need to find conceptually related code."
}

func (t CodeSearch) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"query": {
				"type": "string",
				"description": "Natural-language description of what you're looking for (e.g., 'OAuth token refresh logic', 'database connection pooling')."
			},
			"path": {
				"type": "string",
				"description": "Directory to search in. Defaults to current working directory. Only used in legacy mode (no background index)."
			},
			"type": {
				"type": "string",
				"description": "File type filter (e.g., 'go', 'ts', 'py'). More efficient than searching all files. Only used in legacy mode."
			},
			"max_results": {
				"type": "integer",
				"description": "Maximum number of files to return (default: 10).",
				"minimum": 1,
				"maximum": 50
			},
			"description": {
				"type": "string",
				"description": "REQUIRED. Brief activity label shown in the UI."
			}
		},
		"required": ["query", "description"]
	}`)
}

// searchArgs holds the parsed tool arguments.
type searchArgs struct {
	Query      string `json:"query"`
	Path       string `json:"path"`
	Type       string `json:"type"`
	MaxResults int    `json:"max_results"`
}

func (t CodeSearch) Execute(ctx context.Context, input json.RawMessage) (Result, error) {
	var args searchArgs
	if err := json.Unmarshal(input, &args); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("invalid input: %v", err)}, nil
	}

	if args.Query == "" {
		return Result{IsError: true, Content: "query is required"}, nil
	}

	if t.SandboxCheck != nil && args.Path != "" && !t.SandboxCheck(args.Path) {
		return Result{IsError: true, Content: "Error: path not allowed by sandbox policy"}, nil
	}

	if args.Path == "" {
		args.Path = "."
	}
	if args.MaxResults <= 0 {
		args.MaxResults = 10
	}
	if args.MaxResults > 50 {
		args.MaxResults = 50
	}

	// If no index manager is configured, fall back to the legacy
	// ephemeral index path (used in tests and non-agent contexts).
	if t.Index == nil {
		return t.executeLegacy(ctx, args)
	}

	// Lazy start: trigger background index build on first code_search call
	// rather than at tool registration time. This avoids loading ~60MB of
	// index data for instances that never use code_search.
	t.Index.StartBackgroundIndex()

	// Query the persistent background index.
	results, err := t.Index.Search(args.Query, args.MaxResults)
	if err != nil {
		return Result{IsError: true, Content: err.Error()}, nil
	}
	if len(results) == 0 {
		return Result{Content: fmt.Sprintf("No files matched query %q. Try different keywords or broader search terms.", args.Query)}, nil
	}

	queryTerms := tokenizeForSearch(args.Query)
	stats := t.Index.Stats()
	return formatSearchResults(results, queryTerms, stats.TotalFiles, args), nil
}

// executeLegacy is the fallback path that builds an ephemeral index
// from the specified directory. Used when no CodeIndexManager is wired
// (e.g., in unit tests).
func (t CodeSearch) executeLegacy(ctx context.Context, args searchArgs) (Result, error) {
	searchCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	files := collectSourceFiles(searchCtx, args.Path, args.Type, t.SandboxCheck)
	if len(files) == 0 {
		return Result{Content: "No source files found in the specified directory."}, nil
	}

	contents := readFilesParallel(searchCtx, files, args.Path)
	if len(contents) == 0 {
		return Result{Content: "No readable source files found."}, nil
	}

	idx := buildBM25Index(contents)
	queryTerms := tokenizeForSearch(args.Query)
	if len(queryTerms) == 0 {
		return Result{Content: "Query contains only common words. Try more specific terms."}, nil
	}

	results := idx.score(queryTerms, args.MaxResults)
	if len(results) == 0 {
		return Result{Content: fmt.Sprintf("No files matched query %q. Try different keywords or broader search terms.", args.Query)}, nil
	}

	return formatSearchResults(results, queryTerms, len(files), args), nil
}

// collectSourceFiles walks the directory and returns a list of source files.
func collectSourceFiles(ctx context.Context, baseDir, fileType string, sandboxCheck AllowedPathChecker) []string {
	tracked := gitTrackedFiles(ctx, baseDir)

	var globs []string
	if fileType != "" {
		if tg, ok := typeGlobs[fileType]; ok {
			globs = append(globs, tg...)
		}
	}

	var files []string
	_ = filepath.WalkDir(baseDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if skipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}

		relPath, _ := filepath.Rel(baseDir, path)

		if tracked != nil {
			if _, ok := tracked[filepath.ToSlash(relPath)]; !ok {
				return nil
			}
		}

		if len(globs) > 0 {
			matched := false
			for _, g := range globs {
				if m, _ := filepath.Match(g, d.Name()); m {
					matched = true
					break
				}
			}
			if !matched {
				return nil
			}
		}

		if isBinaryFile(path) {
			return nil
		}

		if info, err := d.Info(); err == nil && info.Size() > 256*1024 {
			return nil
		}

		if sandboxCheck != nil && !sandboxCheck(path) {
			return nil
		}

		files = append(files, path)
		return nil
	})
	return files
}

// readFilesParallel reads file contents concurrently with bounded parallelism.
func readFilesParallel(ctx context.Context, files []string, baseDir string) map[string]string {
	maxWorkers := 8
	if len(files) < maxWorkers {
		maxWorkers = len(files)
	}

	contents := make(map[string]string)
	var mu sync.Mutex

	fileQueue := make(chan string, len(files))
	for _, f := range files {
		fileQueue <- f
	}
	close(fileQueue)

	var wg sync.WaitGroup
	for i := 0; i < maxWorkers; i++ {
		wg.Add(1)
		safego.Go("tool.code_search.reader", func() {
			defer wg.Done()
			for path := range fileQueue {
				select {
				case <-ctx.Done():
					return
				default:
				}
				data, err := os.ReadFile(path)
				if err != nil {
					continue
				}
				relPath, _ := filepath.Rel(baseDir, path)
				mu.Lock()
				contents[relPath] = string(data)
				mu.Unlock()
			}
		})
	}
	wg.Wait()

	return contents
}

// formatSearchResults formats BM25 results for the LLM.
func formatSearchResults(results []bm25Result, queryTerms []string, totalFiles int, args searchArgs) Result {
	var sb strings.Builder

	fmt.Fprintf(&sb, "Semantic search: %q\n", args.Query)
	fmt.Fprintf(&sb, "Ranked %d file(s) by relevance (of %d searched):\n\n", len(results), totalFiles)

	for i, r := range results {
		// Convert score to a 0-100 "relevance" for readability
		relevance := int(r.score * 10)
		if relevance > 100 {
			relevance = 100
		}
		if relevance < 1 {
			relevance = 1
		}

		fmt.Fprintf(&sb, "%d. %s (relevance: %d%%)\n", i+1, r.path, relevance)
	}

	sb.WriteString("\nUse read_file or grep to inspect these files in detail.\n")
	return Result{Content: sb.String()}
}
