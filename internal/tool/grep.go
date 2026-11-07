package tool

import (
	"bufio"
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
	"time"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/safego"
)

type Grep struct {
	SandboxCheck AllowedPathChecker
}

func (t Grep) Name() string { return "grep" }

func (t Grep) Description() string {
	return "Search file contents using ripgrep (with Go fallback). Prefer over search_files for context lines, pagination, file-type filtering, or large repos. Use glob for file paths only. Defaults to files_with_matches; set output_mode=content for matching lines."
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
			"enum": [
				"content",
				"files_with_matches",
				"count"
			],
			"description": "Output mode. 'content' shows matching lines with line numbers. 'files_with_matches' shows only file paths. 'count' shows match counts per file. Default: files_with_matches."
		},
			"after": {
				"type": "integer",
				"description": "Number of lines to show after each match. Prefer this over '-A'.",
				"minimum": 0
			},
			"before": {
				"type": "integer",
				"description": "Number of lines to show before each match. Prefer this over '-B'.",
				"minimum": 0
			},
			"ignore_case": {
				"type": "boolean",
				"description": "Case insensitive search. Prefer this over '-i'. Default false."
			},
			"-A": {
				"type": "integer",
				"description": "Backward-compatible alias for 'after'. Number of lines to show after each match.",
				"minimum": 0
			},
			"-B": {
				"type": "integer",
				"description": "Backward-compatible alias for 'before'. Number of lines to show before each match.",
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
			"description": "Limit output to first N entries. Defaults: 250 in content mode, 500 in files_with_matches/count modes (a trailing summary shows how many were withheld). Use a large value deliberately if you truly need more. With ripgrep installed, entries are capped per file at offset+N matches (deep pagination into one hot file reveals more as the window grows).",
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

const maxFilesWithMatches = 500

const maxContentHeadLimit = 250

type grepArgs struct {
	Pattern        string `json:"pattern"`
	Path           string `json:"path"`
	Glob           string `json:"glob"`
	Type           string `json:"type"`
	OutputMode     string `json:"output_mode"`
	After          int    `json:"-A"`
	Before         int    `json:"-B"`
	AfterLong      int    `json:"after"`
	BeforeLong     int    `json:"before"`
	Context        int    `json:"context"`
	ContextC       int    `json:"-C"`
	HeadLimit      int    `json:"head_limit"`
	Offset         int    `json:"offset"`
	Multiline      bool   `json:"multiline"`
	IgnoreCase     bool   `json:"-i"`
	IgnoreCaseLong bool   `json:"ignore_case"`
}

func (a *grepArgs) normalizeAliases() {
	if a.After == 0 && a.AfterLong > 0 {
		a.After = a.AfterLong
	}
	if a.Before == 0 && a.BeforeLong > 0 {
		a.Before = a.BeforeLong
	}
	if a.IgnoreCaseLong {
		a.IgnoreCase = true
	}
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

	args.normalizeAliases()

	if args.Offset < 0 {
		args.Offset = 0
	}
	if args.HeadLimit < 0 {
		args.HeadLimit = 0
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

	if args.HeadLimit == 0 && (args.OutputMode == "files_with_matches" || args.OutputMode == "count") {
		args.HeadLimit = maxFilesWithMatches
	}

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

	if rgAvailable() {
		return t.rgSearch(ctx, args, re)
	}
	return t.goSearch(ctx, args, re)
}

var (
	rgPath      atomic.Value
	rgLastCheck atomic.Int64
	rgTrying    atomic.Bool
)

var rgRecheckWindow = 5 * time.Minute

func rgAvailable() bool {

	if ptr := rgPath.Load(); ptr != nil {
		return true
	}

	now := time.Now().Unix()
	last := rgLastCheck.Load()
	if last > 0 && now-last < int64(rgRecheckWindow.Seconds()) {

		return false
	}

	rgLastCheck.Store(now)
	p, err := exec.LookPath("rg")
	if err == nil {
		rgPath.Store(&p)
		return true
	}

	if rgTrying.CompareAndSwap(false, true) {
		safego.Go("tool.grep.installRG", installRG)
	}
	return false
}

func installRG() {
	defer rgTrying.Store(false)

	var cmd *exec.Cmd
	var mgr string
	switch {
	case hasCommand("brew"):
		cmd = exec.Command("brew", "install", "ripgrep")
		mgr = "brew"
	case hasCommand("cargo"):
		cmd = exec.Command("cargo", "install", "ripgrep")
		mgr = "cargo"
	default:
		debug.Log("grep", "ripgrep not installed and no official package manager found; install the official BurntSushi ripgrep yourself, e.g. brew install ripgrep / cargo install ripgrep / apt install ripgrep, or download from https://github.com/BurntSushi/ripgrep/releases (the PyPI 'ripgrep' package is a third-party wrapper and is deliberately not auto-installed); grep falls back to the built-in Go engine until then")
		return
	}

	if err := cmd.Run(); err != nil {
		debug.Log("grep", "ripgrep install via %s failed: %v", mgr, err)
		return
	}

	p, err := exec.LookPath("rg")
	if err != nil {
		debug.Log("grep", "ripgrep install via %s succeeded but 'rg' not found in PATH (may need PATH update)", mgr)
		return
	}

	rgPath.Store(&p)
	debug.Log("grep", "ripgrep installed successfully via %s at %s", mgr, p)
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
	} else if args.OutputMode == "content" {

		rgArgs = append(rgArgs, "--max-count", fmt.Sprintf("%d", maxContentHeadLimit+args.Offset))
	}

	rgArgs = append(rgArgs, "--", args.Pattern, args.Path)

	ptr := rgPath.Load()
	if ptr == nil {

		return Result{IsError: true, Content: "ripgrep path not available"}, nil
	}
	path := ptr.(*string)
	cmd := exec.CommandContext(ctx, *path, rgArgs...)
	cmd.Env = append(os.Environ(), "GIT_PAGER=cat")
	out, err := cmd.Output()
	if err != nil {
		if res, handled := rgExitResult(err); handled {
			return res, nil
		}

	}

	return formatGrepOutput(string(out), args)
}

func rgExitResult(err error) (Result, bool) {
	if exitErr, ok := err.(*exec.ExitError); ok {
		if exitErr.ExitCode() >= 2 {
			msg := strings.TrimSpace(string(exitErr.Stderr))
			if msg == "" {
				msg = err.Error()
			}
			return Result{IsError: true, Content: "ripgrep error: " + msg}, true
		}
		return Result{}, false
	}
	return Result{IsError: true, Content: fmt.Sprintf("ripgrep failed to run: %v", err)}, true
}

func formatGrepOutput(output string, args grepArgs) (Result, error) {
	if output == "" {
		var hints []string
		if args.IgnoreCase {
			hints = append(hints, fmt.Sprintf("Case-insensitive search for %q found nothing. Try without -i or check spelling.", args.Pattern))
		} else {
			hints = append(hints, fmt.Sprintf("Try case-insensitive search (-i) or check if the pattern %q is correct.", args.Pattern))
		}
		if args.Type != "" {
			hints = append(hints, fmt.Sprintf("You restricted to type %q — try widening to more file types.", args.Type))
		}
		if args.Path != "" && args.Path != "." {
			hints = append(hints, "Try searching from a broader directory (e.g., the project root).")
		}
		return Result{Content: "No matches found.\nSuggestions:\n  " + strings.Join(hints, "\n  ")}, nil
	}

	lines := strings.Split(strings.TrimRight(output, "\n"), "\n")

	total := len(lines)

	if args.OutputMode == "content" {

		sortRgContentLines(lines)
		start := args.Offset
		if start > total {
			start = total
		}
		end := total
		if args.HeadLimit > 0 && start+args.HeadLimit < end {
			end = start + args.HeadLimit
		} else if args.HeadLimit <= 0 && start+maxContentHeadLimit < end {
			end = start + maxContentHeadLimit
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

	var sb strings.Builder
	totalCount := 0
	if args.OutputMode == "count" {

		for _, l := range lines {
			if idx := strings.LastIndex(l, ":"); idx >= 0 {
				var n int
				if _, err := fmt.Sscanf(l[idx+1:], "%d", &n); err == nil {
					totalCount += n
				}
			}
		}
		start := args.Offset
		if start > total {
			start = total
		}
		limit := args.HeadLimit
		if limit <= 0 {
			limit = maxFilesWithMatches
		}
		end := total
		if start+limit < end {
			end = start + limit
		}
		for _, l := range lines[start:end] {
			sb.WriteString(l)
			sb.WriteByte('\n')
		}
		if total > end || start > 0 {
			fmt.Fprintf(&sb, "\n(showing %d-%d of %d files; use offset/head_limit to page or narrow the pattern)", start+1, end, total)
		}
	} else {

		sortedLines := make([]string, len(lines))
		copy(sortedLines, lines)

		sortPathsDepthThenLex(sortedLines)

		start := args.Offset
		if start > total {
			start = total
		}
		limit := args.HeadLimit
		if limit <= 0 {
			limit = maxFilesWithMatches
		}
		end := total
		if start+limit < end {
			end = start + limit
		}
		shown := sortedLines[start:end]
		for _, l := range shown {
			sb.WriteString(l)
			sb.WriteByte('\n')
		}
		if total > end || start > 0 {
			fmt.Fprintf(&sb, "\n(showing %d-%d of %d files; use offset/head_limit to page or narrow the pattern/glob)", start+1, end, total)
		}
	}
	if args.OutputMode == "files_with_matches" {
		fmt.Fprintf(&sb, "\n%d file(s) matched", len(lines))
	} else if args.OutputMode == "count" {
		fmt.Fprintf(&sb, "\n%d file(s), %d match(es) total", len(lines), totalCount)
	}
	return Result{Content: sb.String()}, nil
}

func sortPathsDepthThenLex(paths []string) {
	sort.Slice(paths, func(i, j int) bool {
		di, dj := pathDepth(paths[i]), pathDepth(paths[j])
		if di != dj {
			return di < dj
		}
		return paths[i] < paths[j]
	})
}

func pathDepth(path string) int {
	return strings.Count(path, string(filepath.Separator))
}

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

func (t Grep) goSearch(ctx context.Context, args grepArgs, re *regexp.Regexp) (Result, error) {

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

		files = append(files, path)
		return nil
	})

	if len(files) == 0 {

		return formatGrepOutput("", args)
	}

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
				default:
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

	switch args.OutputMode {
	case "files_with_matches":
		return formatFilesWithMatches(matchedFiles, args), nil
	case "count":
		return formatCount(fileCounts, args), nil
	default:
		return formatContentMatches(allMatches, args), nil
	}
}

const grepMaxLineLen = 10 * 1024 * 1024

func grepFileHasMatch(path string, re *regexp.Regexp) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)

	scanner.Buffer(make([]byte, 0, 64*1024), grepMaxLineLen)
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
	scanner.Buffer(make([]byte, 0, 64*1024), grepMaxLineLen)
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

	var lines []string
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), grepMaxLineLen)
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
	sortPathsDepthThenLex(paths)

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
	if total > end {
		fmt.Fprintf(&sb, "\n(showing %d-%d of %d files; pass head_limit to raise the cap or narrow the pattern/glob)", start+1, end, total)
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

	totalMatches := 0
	for _, c := range fileCounts {
		totalMatches += c
	}

	var sb strings.Builder
	for _, p := range paths {
		c := fileCounts[p]
		fmt.Fprintf(&sb, "%s: %d\n", p, c)
	}
	if total > end {
		fmt.Fprintf(&sb, "\n(showing %d-%d of %d files; pass head_limit to raise the cap or narrow the pattern/glob)", start+1, end, total)
	}
	fmt.Fprintf(&sb, "\n%d file(s), %d match(es) total", total, totalMatches)
	return Result{Content: sb.String()}
}

func sortRgContentLines(lines []string) {
	type rgLinePair struct {
		k rgLineKey
		s string
	}
	pairs := make([]rgLinePair, len(lines))
	last := rgLineKey{}
	for i, l := range lines {
		k := rgContentKey(l)
		if k.ok {
			last = k
		} else {
			k = rgLineKey{path: last.path, line: last.line, ok: true, cont: true}
		}
		pairs[i] = rgLinePair{k: k, s: l}
	}

	sort.SliceStable(pairs, func(i, j int) bool {
		ki, kj := pairs[i].k, pairs[j].k
		if ki.path != kj.path {
			return ki.path < kj.path
		}
		if ki.line != kj.line {
			return ki.line < kj.line
		}

		return !ki.cont && kj.cont
	})
	for i := range pairs {
		lines[i] = pairs[i].s
	}
}

type rgLineKey struct {
	path string
	line int
	ok   bool
	cont bool
}

func rgContentKey(l string) rgLineKey {

	for i := 0; i < len(l); i++ {
		if l[i] == ':' || l[i] == '-' {
			rest := l[i+1:]
			n := 0
			for n < len(rest) && rest[n] >= '0' && rest[n] <= '9' {
				n++
			}
			if n > 0 && n < len(rest) && (rest[n] == ':' || rest[n] == '-') {
				line := 0
				for _, c := range rest[:n] {
					line = line*10 + int(c-'0')
				}
				return rgLineKey{path: l[:i], line: line, ok: true}
			}
		}
	}
	return rgLineKey{}
}

func formatContentMatches(matches []fileMatch, args grepArgs) Result {

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
	} else if args.HeadLimit <= 0 && start+maxContentHeadLimit < end {
		end = start + maxContentHeadLimit
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
