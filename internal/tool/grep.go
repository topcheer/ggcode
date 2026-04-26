package tool

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/topcheer/ggcode/internal/safego"
)

// Grep implements a powerful file content search tool (ripgrep wrapper with Go fallback).
type Grep struct {
	SandboxCheck AllowedPathChecker
}

func (t Grep) Name() string { return "grep" }

func (t Grep) Description() string {
	return "A powerful search tool built on ripgrep (with Go fallback). " +
		"Supports regex, glob filtering, file type filtering, context lines, " +
		"multiple output modes, multiline matching, and pagination."
}

func (t Grep) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"pattern": {
				"type": "string",
				"description": "The regular expression pattern to search for in file contents."
			},
			"path": {
				"type": "string",
				"description": "File or directory to search in. Defaults to current working directory."
			},
			"glob": {
				"type": "string",
				"description": "Glob pattern to filter files (e.g. \"*.js\", \"*.{ts,tsx}\")"
			},
			"type": {
				"type": "string",
				"description": "File type to search. Common types: js, ts, py, go, rust, java, rb, c, cpp, css, html, sql. More efficient than glob for type filtering."
			},
			"output_mode": {
				"type": "string",
				"enum": ["content", "files_with_matches", "count"],
				"description": "Output mode. 'content' shows matching lines with line numbers. 'files_with_matches' shows only file paths. 'count' shows match counts per file. Default: files_with_matches."
			},
			"-A": {
				"type": "integer",
				"description": "Number of lines to show after each match.",
				"minimum": 0
			},
			"-B": {
				"type": "integer",
				"description": "Number of lines to show before each match.",
				"minimum": 0
			},
			"-C": {
				"type": "integer",
				"description": "Alias for 'context'. Number of lines to show before and after each match.",
				"minimum": 0
			},
			"context": {
				"type": "integer",
				"description": "Number of lines to show before and after each match. -C is an alias for this.",
				"minimum": 0
			},
			"head_limit": {
				"type": "integer",
				"description": "Limit output to first N entries. Default 250. Use 0 for unlimited.",
				"minimum": 0
			},
			"offset": {
				"type": "integer",
				"description": "Skip first N entries for pagination. Default 0.",
				"minimum": 0
			},
			"multiline": {
				"type": "boolean",
				"description": "Enable multiline mode where . matches newlines. Default false."
			},
			"-i": {
				"type": "boolean",
				"description": "Case insensitive search. Default false."
			}
		},
		"required": ["pattern"]
	}`)
}

// grepArgs holds all parsed arguments.
type grepArgs struct {
	Pattern    string `json:"pattern"`
	Path       string `json:"path"`
	Glob       string `json:"glob"`
	Type       string `json:"type"`
	OutputMode string `json:"output_mode"`
	After      int    `json:"-A"`
	Before     int    `json:"-B"`
	Context    int    `json:"context"`
	ContextC   int    `json:"-C"`
	HeadLimit  int    `json:"head_limit"`
	Offset     int    `json:"offset"`
	Multiline  bool   `json:"multiline"`
	IgnoreCase bool   `json:"-i"`
}

func (a *grepArgs) contextLines() int {
	if a.ContextC > 0 {
		return a.ContextC
	}
	return a.Context
}

func (t Grep) Execute(ctx context.Context, input json.RawMessage) (Result, error) {
	var args grepArgs
	if err := json.Unmarshal(input, &args); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("invalid input: %v", err)}, nil
	}

	if args.Pattern == "" {
		return Result{IsError: true, Content: "pattern is required"}, nil
	}

	if t.SandboxCheck != nil && args.Path != "" && !t.SandboxCheck(args.Path) {
		return Result{IsError: true, Content: "Error: path not allowed by sandbox policy"}, nil
	}

	if args.Path == "" {
		args.Path = "."
	}
	if args.OutputMode == "" {
		args.OutputMode = "files_with_matches"
	}
	if args.HeadLimit == 0 && args.OutputMode == "content" {
		args.HeadLimit = 250
	}

	// Validate regex
	flags := ""
	if args.IgnoreCase {
		flags = "(?i)"
	}
	if args.Multiline {
		flags += "(?s)"
	}
	re, err := regexp.Compile(flags + args.Pattern)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("invalid regex: %v", err)}, nil
	}

	// Try rg first, then Go fallback
	if rgAvailable() {
		return t.rgSearch(ctx, args, re)
	}
	return t.goSearch(ctx, args, re)
}

// ── rg (ripgrep) path ────────────────────────────────────────────

var (
	rgPath    string
	rgOnce    sync.Once
	rgInstall sync.Once
	rgTrying  atomic.Bool // prevent concurrent install attempts
)

func rgAvailable() bool {
	rgOnce.Do(func() {
		p, err := exec.LookPath("rg")
		if err == nil {
			rgPath = p
			return
		}
		// Try silent async install (fire and forget)
		if rgTrying.CompareAndSwap(false, true) {
			go installRG()
		}
	})
	return rgPath != ""
}

// installRG attempts to install ripgrep silently. On success, sets rgPath
// so subsequent calls to rgAvailable will find it.
func installRG() {
	defer rgTrying.Store(false)

	var cmd *exec.Cmd
	switch {
	case hasCommand("brew"):
		cmd = exec.Command("brew", "install", "ripgrep")
	case hasCommand("cargo"):
		cmd = exec.Command("cargo", "install", "ripgrep")
	case hasCommand("pip"):
		cmd = exec.Command("pip", "install", "ripgrep")
	case hasCommand("pip3"):
		cmd = exec.Command("pip3", "install", "ripgrep")
	default:
		return
	}
	cmd.Stdout = nil
	cmd.Stderr = nil
	_ = cmd.Run()

	// Check if it worked
	p, err := exec.LookPath("rg")
	if err == nil {
		rgPath = p
	}
}

func hasCommand(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

func (t Grep) rgSearch(ctx context.Context, args grepArgs, re *regexp.Regexp) (Result, error) {
	rgArgs := []string{"--no-heading", "--color", "never", "--with-filename", "--line-number"}

	switch args.OutputMode {
	case "files_with_matches":
		rgArgs = append(rgArgs, "-l")
	case "count":
		rgArgs = append(rgArgs, "-c")
	}

	if args.IgnoreCase {
		rgArgs = append(rgArgs, "-i")
	}
	if args.Multiline {
		rgArgs = append(rgArgs, "-U", "--multiline-dotall")
	}

	ctxLines := args.contextLines()
	if ctxLines > 0 {
		rgArgs = append(rgArgs, "-C", fmt.Sprintf("%d", ctxLines))
	} else {
		if args.After > 0 {
			rgArgs = append(rgArgs, "-A", fmt.Sprintf("%d", args.After))
		}
		if args.Before > 0 {
			rgArgs = append(rgArgs, "-B", fmt.Sprintf("%d", args.Before))
		}
	}

	if args.Glob != "" {
		rgArgs = append(rgArgs, "--glob", args.Glob)
	}
	if args.Type != "" {
		rgArgs = append(rgArgs, "--type", args.Type)
	}

	if args.HeadLimit > 0 && args.OutputMode == "content" {
		rgArgs = append(rgArgs, "--max-count", fmt.Sprintf("%d", args.HeadLimit+args.Offset))
	}

	rgArgs = append(rgArgs, "--", args.Pattern, args.Path)

	cmd := exec.CommandContext(ctx, rgPath, rgArgs...)
	cmd.Env = append(os.Environ(), "GIT_PAGER=cat")
	out, err := cmd.Output()
	if err != nil {
		if len(out) == 0 {
			return Result{Content: "No matches found."}, nil
		}
		// rg returns exit code 1 for no matches, 2 for errors
		if bytes.Contains(out, []byte("Error")) || bytes.Contains(out, []byte("error")) {
			// Fall through and try to use what we got
			if len(out) == 0 {
				return Result{IsError: true, Content: fmt.Sprintf("ripgrep error: %s", string(out))}, nil
			}
		}
	}

	return formatGrepOutput(string(out), args)
}

func formatGrepOutput(output string, args grepArgs) (Result, error) {
	if output == "" {
		return Result{Content: "No matches found."}, nil
	}

	lines := strings.Split(strings.TrimRight(output, "\n"), "\n")

	total := len(lines)

	// Apply offset + head_limit for content mode
	if args.OutputMode == "content" {
		start := args.Offset
		if start > total {
			start = total
		}
		end := total
		if args.HeadLimit > 0 && start+args.HeadLimit < end {
			end = start + args.HeadLimit
		}
		lines = lines[start:end]

		var sb strings.Builder
		for _, l := range lines {
			sb.WriteString(l)
			sb.WriteByte('\n')
		}
		if total > end {
			fmt.Fprintf(&sb, "\n(showing %d-%d of %d results; use offset to see more)", start+1, end, total)
		}
		return Result{Content: sb.String()}, nil
	}

	// files_with_matches or count — just return the output
	var sb strings.Builder
	totalCount := 0
	for _, l := range lines {
		sb.WriteString(l)
		sb.WriteByte('\n')
		if args.OutputMode == "count" {
			// rg count format: "path:N" — extract N
			if idx := strings.LastIndex(l, ":"); idx >= 0 {
				var n int
				if _, err := fmt.Sscanf(l[idx+1:], "%d", &n); err == nil {
					totalCount += n
				}
			}
		}
	}
	if args.OutputMode == "files_with_matches" {
		fmt.Fprintf(&sb, "\n%d file(s) matched", len(lines))
	} else if args.OutputMode == "count" {
		fmt.Fprintf(&sb, "\n%d file(s), %d match(es) total", len(lines), totalCount)
	}
	return Result{Content: sb.String()}, nil
}

// ── Go fallback path ─────────────────────────────────────────────

// typeGlobs maps file type names to glob patterns.
var typeGlobs = map[string][]string{
	"js":   {"*.js", "*.jsx", "*.mjs", "*.cjs"},
	"ts":   {"*.ts", "*.tsx", "*.mts", "*.cts"},
	"py":   {"*.py", "*.pyi", "*.pyw"},
	"go":   {"*.go"},
	"rust": {"*.rs"},
	"java": {"*.java"},
	"rb":   {"*.rb", "*.erb"},
	"c":    {"*.c", "*.h"},
	"cpp":  {"*.cpp", "*.cc", "*.cxx", "*.hpp", "*.hxx", "*.h"},
	"css":  {"*.css", "*.scss", "*.sass", "*.less"},
	"html": {"*.html", "*.htm"},
	"sql":  {"*.sql"},
}

type fileMatch struct {
	path    string
	lineNum int
	line    string
}

type fileCount struct {
	path  string
	count int
}

func (t Grep) goSearch(ctx context.Context, args grepArgs, re *regexp.Regexp) (Result, error) {
	// Collect files to search
	tracked := gitTrackedFiles(ctx, args.Path)

	var globs []string
	if args.Glob != "" {
		globs = []string{args.Glob}
	}
	if args.Type != "" {
		if tg, ok := typeGlobs[args.Type]; ok {
			globs = append(globs, tg...)
		}
	}

	var files []string
	filepath.WalkDir(args.Path, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if skipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}

		relPath, _ := filepath.Rel(args.Path, path)

		// gitignore filter
		if tracked != nil {
			if _, ok := tracked[filepath.ToSlash(relPath)]; !ok {
				return nil
			}
		}

		// glob filter
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

		// Skip binary
		if isBinaryFile(path) {
			return nil
		}

		files = append(files, path)
		return nil
	})

	if len(files) == 0 {
		return Result{Content: "No matches found."}, nil
	}

	// Parallel search
	numWorkers := runtime.NumCPU()
	if numWorkers > 8 {
		numWorkers = 8
	}
	if numWorkers > len(files) {
		numWorkers = len(files)
	}

	var (
		allMatches   []fileMatch
		fileCounts   = make(map[string]int)
		matchedFiles = make(map[string]bool)
		mu           sync.Mutex
		fileQueue    = make(chan string, len(files))
	)

	for _, f := range files {
		fileQueue <- f
	}
	close(fileQueue)

	var wg sync.WaitGroup
	for i := 0; i < numWorkers; i++ {
		wg.Add(1)
		safego.Go("tool.grep.worker", func() {
			defer wg.Done()
			for path := range fileQueue {
				relPath, _ := filepath.Rel(args.Path, path)

				switch args.OutputMode {
				case "files_with_matches":
					if grepFileHasMatch(path, re) {
						mu.Lock()
						matchedFiles[relPath] = true
						mu.Unlock()
					}
				case "count":
					cnt := grepFileCount(path, re)
					if cnt > 0 {
						mu.Lock()
						fileCounts[relPath] = cnt
						mu.Unlock()
					}
				default: // content
					matches := grepFileContent(path, re, args)
					if len(matches) > 0 {
						mu.Lock()
						allMatches = append(allMatches, matches...)
						mu.Unlock()
					}
				}
			}
		})
	}
	wg.Wait()

	// Format output
	switch args.OutputMode {
	case "files_with_matches":
		return formatFilesWithMatches(matchedFiles, args), nil
	case "count":
		return formatCount(fileCounts, args), nil
	default:
		return formatContentMatches(allMatches, args), nil
	}
}

func grepFileHasMatch(path string, re *regexp.Regexp) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		if re.MatchString(scanner.Text()) {
			return true
		}
	}
	return false
}

func grepFileCount(path string, re *regexp.Regexp) int {
	f, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer f.Close()

	count := 0
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		if re.MatchString(scanner.Text()) {
			count++
		}
	}
	return count
}

func grepFileContent(path string, re *regexp.Regexp, args grepArgs) []fileMatch {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	// Read all lines for context support
	var lines []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}

	ctxLines := args.contextLines()
	before := args.Before
	after := args.After
	if ctxLines > 0 {
		before = ctxLines
		after = ctxLines
	}

	var matches []fileMatch
	// Track which lines have been included (to avoid duplicates with context)
	included := make(map[int]bool)

	for i, line := range lines {
		if !re.MatchString(line) {
			continue
		}
		start := i - before
		if start < 0 {
			start = 0
		}
		end := i + after + 1
		if end > len(lines) {
			end = len(lines)
		}

		for j := start; j < end; j++ {
			if included[j] {
				continue
			}
			included[j] = true
			matches = append(matches, fileMatch{
				path:    path,
				lineNum: j + 1,
				line:    lines[j],
			})
		}
	}
	return matches
}

func formatFilesWithMatches(matchedFiles map[string]bool, args grepArgs) Result {
	var paths []string
	for p := range matchedFiles {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	total := len(paths)
	start := args.Offset
	if start > total {
		start = total
	}
	end := total
	if args.HeadLimit > 0 && start+args.HeadLimit < end {
		end = start + args.HeadLimit
	}
	paths = paths[start:end]

	var sb strings.Builder
	for _, p := range paths {
		sb.WriteString(p)
		sb.WriteByte('\n')
	}
	fmt.Fprintf(&sb, "\n%d file(s) matched", total)
	return Result{Content: sb.String()}
}

func formatCount(fileCounts map[string]int, args grepArgs) Result {
	var paths []string
	for p := range fileCounts {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	total := len(paths)
	start := args.Offset
	if start > total {
		start = total
	}
	end := total
	if args.HeadLimit > 0 && start+args.HeadLimit < end {
		end = start + args.HeadLimit
	}
	paths = paths[start:end]

	var sb strings.Builder
	totalMatches := 0
	for _, p := range paths {
		c := fileCounts[p]
		totalMatches += c
		fmt.Fprintf(&sb, "%s: %d\n", p, c)
	}
	fmt.Fprintf(&sb, "\n%d file(s), %d match(es) total", len(paths), totalMatches)
	return Result{Content: sb.String()}
}

func formatContentMatches(matches []fileMatch, args grepArgs) Result {
	// Sort matches by path then line number
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].path != matches[j].path {
			return matches[i].path < matches[j].path
		}
		return matches[i].lineNum < matches[j].lineNum
	})

	total := len(matches)
	start := args.Offset
	if start > total {
		start = total
	}
	end := total
	if args.HeadLimit > 0 && start+args.HeadLimit < end {
		end = start + args.HeadLimit
	}
	matches = matches[start:end]

	var sb strings.Builder
	prevPath := ""
	for _, m := range matches {
		relPath := m.path
		if prevPath != "" && prevPath != relPath {
			sb.WriteByte('\n')
		}
		prevPath = relPath
		fmt.Fprintf(&sb, "%s:%d: %s\n", relPath, m.lineNum, m.line)
	}

	if total > end {
		fmt.Fprintf(&sb, "\n(showing %d-%d of %d results; use offset to see more)", start+1, end, total)
	} else if total > 0 {
		fmt.Fprintf(&sb, "\n%d match(es)", total)
	}

	return Result{Content: sb.String()}
}
