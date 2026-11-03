package tool

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
)

type TodoMarker struct {
	File     string `json:"file"`
	Line     int    `json:"line"`
	Category string `json:"category"`
	Severity string `json:"severity"`
	Text     string `json:"text"`
	Author   string `json:"author"`
	Age      string `json:"age"`
}

var todoCategorySeverity = map[string]string{
	"FIXME":      "critical",
	"BUG":        "critical",
	"TODO":       "high",
	"HACK":       "medium",
	"XXX":        "medium",
	"WORKAROUND": "medium",
	"NOTE":       "low",
}

var todoMarkerOrder = []string{
	"WORKAROUND",
	"FIXME",
	"TODO",
	"HACK",
	"NOTE",
	"BUG",
	"XXX",
}

var todoSeverityOrder = map[string]int{
	"critical": 0,
	"high":     1,
	"medium":   2,
	"low":      3,
}

var scanTodoSourceExtensions = map[string]bool{
	".go": true, ".rs": true, ".py": true, ".js": true, ".ts": true,
	".jsx": true, ".tsx": true, ".java": true, ".kt": true, ".scala": true,
	".c": true, ".cpp": true, ".cc": true, ".cxx": true, ".h": true,
	".hpp": true, ".cs": true, ".rb": true, ".php": true, ".swift": true,
	".dart": true, ".lua": true, ".sh": true, ".bash": true, ".zsh": true,
	".sql": true, ".proto": true, ".yaml": true, ".yml": true,
	".toml": true, ".json": true, ".tf": true, ".elm": true,
	".ex": true, ".exs": true, ".clj": true, ".cljs": true, ".hs": true,
	".ml": true, ".fs": true, ".vim": true, ".r": true, ".pl": true,
}

var scanTodoSkipDirs = map[string]bool{
	".git": true, "node_modules": true, "vendor": true, ".vendor": true,
	"dist": true, "build": true, "target": true, "__pycache__": true,
	".next": true, ".nuxt": true, ".output": true, ".svelte-kit": true,
	"coverage": true, ".coverage": true, ".tox": true, ".mypy_cache": true,
	".pytest_cache": true, ".idea": true, ".vscode": true,
}

type ScanTodosTool struct {
	WorkingDir string
}

func (t ScanTodosTool) Name() string { return "scan_todos" }

func (t ScanTodosTool) Description() string {
	return "Scan the codebase for technical-debt markers (TODO, FIXME, HACK, XXX, BUG, NOTE, WORKAROUND) " +
		"across all source files. Returns a categorized, severity-ranked report with optional git-blame " +
		"staleness detection. Use to survey accumulated technical debt, find stale TODOs, or identify " +
		"areas needing attention before a release. Different from code_health (complexity metrics) and " +
		"diff_scan (diff-only checks)."
}

func (t ScanTodosTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"path": {
				"type": "string",
				"description": "Directory to scan (default: current working directory)"
			},
			"include_blame": {
				"type": "boolean",
				"description": "Include git blame info (author, age) for each marker. Slower but reveals stale markers. Default: false"
			},
			"stale_days": {
				"type": "integer",
				"description": "Markers older than this many days are flagged as stale (default: 90, requires include_blame)"
			},
			"max_results": {
				"type": "integer",
				"description": "Maximum number of markers to return (default: 50)"
			},
			"categories": {
				"type": "string",
				"description": "Comma-separated list of categories to include (e.g. 'TODO,FIXME'). Default: all categories"
			},
			"description": {
				"type": "string",
				"description": "REQUIRED. Brief activity label shown in the UI."
			}
		},
		"required": ["description"]
	}`)
}

func (t ScanTodosTool) Execute(ctx context.Context, input json.RawMessage) (Result, error) {
	var args struct {
		Path         string `json:"path"`
		IncludeBlame bool   `json:"include_blame"`
		StaleDays    int    `json:"stale_days"`
		MaxResults   int    `json:"max_results"`
		Categories   string `json:"categories"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("invalid input: %v", err)}, nil
	}

	dir := resolveDir(args.Path, t.WorkingDir)
	if dir == "" {
		dir = "."
	}
	absDir, err := filepath.Abs(dir)
	if err != nil {
		absDir = dir
	}

	if args.StaleDays <= 0 {
		args.StaleDays = 90
	}
	if args.MaxResults <= 0 {
		args.MaxResults = 50
	}

	var categoryFilter map[string]bool
	if args.Categories != "" {
		valid := make(map[string]bool, len(todoMarkerOrder))
		for _, m := range todoMarkerOrder {
			valid[m] = true
		}
		categoryFilter = make(map[string]bool)
		for _, c := range strings.Split(args.Categories, ",") {
			c = strings.ToUpper(strings.TrimSpace(c))
			if c == "" {
				continue
			}

			if !valid[c] {
				return Result{IsError: true, Content: fmt.Sprintf("unknown category %q; valid: TODO, FIXME, HACK, XXX, BUG, NOTE, WORKAROUND", c)}, nil
			}
			categoryFilter[c] = true
		}
	}

	debug.Log("scan-todos", "scanning %s blame=%v stale_days=%d max=%d", absDir, args.IncludeBlame, args.StaleDays, args.MaxResults)

	markers, filesScanned, err := scanDirectoryForTodos(absDir, categoryFilter)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("scan failed: %v", err)}, nil
	}

	if args.IncludeBlame && len(markers) > 0 {
		enrichWithBlame(absDir, markers, args.StaleDays)
	}

	sort.Slice(markers, func(i, j int) bool {
		si := todoSeverityOrder[markers[i].Severity]
		sj := todoSeverityOrder[markers[j].Severity]
		if si != sj {
			return si < sj
		}
		if markers[i].File != markers[j].File {
			return markers[i].File < markers[j].File
		}
		return markers[i].Line < markers[j].Line
	})

	total := len(markers)
	truncated := false
	if len(markers) > args.MaxResults {
		markers = markers[:args.MaxResults]
		truncated = true
	}

	content := formatTodoReport(markers, absDir, total, filesScanned, truncated, args.IncludeBlame)

	return Result{Content: content}, nil
}

type limitedBuf struct {
	b []byte
}

func (w *limitedBuf) Write(p []byte) (int, error) {
	const max = 8 << 20
	if len(w.b) < max {
		room := max - len(w.b)
		if room > len(p) {
			room = len(p)
		}
		w.b = append(w.b, p[:room]...)
	}

	return len(p), nil
}

func (w *limitedBuf) bytes() []byte { return w.b }

func scanDirectoryForTodos(rootDir string, categoryFilter map[string]bool) ([]TodoMarker, int, error) {
	var markers []TodoMarker
	filesScanned := 0

	err := filepath.WalkDir(rootDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {

			if path == rootDir {
				return err
			}
			return nil
		}
		if d.IsDir() {
			name := d.Name()
			if scanTodoSkipDirs[name] {
				return filepath.SkipDir
			}
			return nil
		}

		ext := filepath.Ext(path)
		if !scanTodoSourceExtensions[ext] {
			return nil
		}

		filesScanned++
		fileMarkers, err := scanFileForTodos(path, categoryFilter)
		if err != nil {
			return nil
		}
		markers = append(markers, fileMarkers...)
		return nil
	})

	return markers, filesScanned, err
}

func scanFileForTodos(path string, categoryFilter map[string]bool) ([]TodoMarker, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var markers []TodoMarker
	scanner := bufio.NewScanner(f)

	scanner.Buffer(make([]byte, 0, 65536), 65536)
	lineNum := 0

	for scanner.Scan() {
		lineNum++
		line := scanner.Text()
		marker := extractTodoMarker(path, lineNum, line, categoryFilter)
		if marker != nil {
			markers = append(markers, *marker)
		}
	}

	return markers, scanner.Err()
}

func extractTodoMarker(file string, lineNum int, line string, categoryFilter map[string]bool) *TodoMarker {
	upper := strings.ToUpper(line)
	for _, cat := range todoMarkerOrder {

		idx := findMarkerIndex(upper, cat)
		if idx < 0 {
			continue
		}
		if categoryFilter != nil && !categoryFilter[cat] {
			continue
		}

		textStart := idx + len(cat)
		if textStart >= len(line) {
			textStart = len(line)
		}
		text := strings.TrimSpace(line[textStart:])

		text = strings.TrimLeft(text, ":- \t")

		if len(text) > 120 {
			text = text[:120] + "..."
		}
		return &TodoMarker{
			File:     file,
			Line:     lineNum,
			Category: cat,
			Severity: todoCategorySeverity[cat],
			Text:     text,
		}
	}
	return nil
}

func findMarkerIndex(upperLine, marker string) int {
	searchStart := 0
	for {
		idx := strings.Index(upperLine[searchStart:], marker)
		if idx < 0 {
			return -1
		}
		absIdx := searchStart + idx

		if absIdx > 0 {
			before := upperLine[absIdx-1]
			if (before >= 'A' && before <= 'Z') || (before >= '0' && before <= '9') || before == '_' {
				searchStart = absIdx + 1
				continue
			}
		}

		afterIdx := absIdx + len(marker)
		if afterIdx < len(upperLine) {
			after := upperLine[afterIdx]
			if (after >= 'A' && after <= 'Z') || (after >= '0' && after <= '9') || after == '_' {
				searchStart = absIdx + 1
				continue
			}
		}
		return absIdx
	}
}

func enrichWithBlame(rootDir string, markers []TodoMarker, staleDays int) {

	fileMarkers := make(map[string][]int)
	for i := range markers {
		relPath, err := filepath.Rel(rootDir, markers[i].File)
		if err != nil {
			continue
		}
		fileMarkers[relPath] = append(fileMarkers[relPath], i)
	}

	staleThreshold := time.Now().AddDate(0, 0, -staleDays)

	for relPath, indices := range fileMarkers {

		blameCtx, cancelBlame := context.WithTimeout(context.Background(), 30*time.Second)
		cmd := exec.CommandContext(blameCtx, "git", "blame", "--line-porcelain", "--date=iso", relPath)
		cmd.Dir = rootDir
		var lb limitedBuf
		cmd.Stdout = &lb
		err := cmd.Run()
		cancelBlame()
		if err != nil {
			continue
		}
		output := lb.bytes()

		blameInfo := parseGitBlame(output)

		for _, idx := range indices {
			if idx >= len(markers) {
				continue
			}
			lineNum := markers[idx].Line
			if info, ok := blameInfo[lineNum]; ok {
				markers[idx].Author = info.author
				if !info.date.IsZero() {
					markers[idx].Age = formatAge(info.date)
					if info.date.Before(staleThreshold) {

						if todoSeverityOrder[markers[idx].Severity] > todoSeverityOrder["high"] {
							markers[idx].Severity = "high"
						}
					}
				}
			}
		}
	}
}

type blameLineInfo struct {
	author string
	date   time.Time
}

func parseGitBlame(output []byte) map[int]blameLineInfo {
	result := make(map[int]blameLineInfo)
	lines := strings.Split(string(output), "\n")

	var currentLine int
	var author string
	var date time.Time

	for _, line := range lines {

		if len(line) > 0 && ((line[0] >= '0' && line[0] <= '9') || (line[0] >= 'a' && line[0] <= 'f')) {
			parts := strings.Fields(line)
			if len(parts) >= 3 {
				fmt.Sscanf(parts[2], "%d", &currentLine)
				author = ""
				date = time.Time{}
			}
		}
		if strings.HasPrefix(line, "author ") {
			author = strings.TrimPrefix(line, "author ")
		}
		if strings.HasPrefix(line, "author-time ") {
			timeStr := strings.TrimPrefix(line, "author-time ")
			var unixTime int64
			fmt.Sscanf(timeStr, "%d", &unixTime)
			if unixTime > 0 {
				date = time.Unix(unixTime, 0)
			}
		}
		if strings.HasPrefix(line, "author-tz ") {

			if currentLine > 0 {
				result[currentLine] = blameLineInfo{author: author, date: date}
			}
		}
	}
	return result
}

func formatAge(t time.Time) string {
	days := int(time.Since(t).Hours() / 24)
	if days < 1 {
		return "today"
	}
	if days < 30 {
		return fmt.Sprintf("%dd", days)
	}
	if days < 365 {
		return fmt.Sprintf("%dmo", days/30)
	}
	return fmt.Sprintf("%dy%dmo", days/365, (days%365)/30)
}

func formatTodoReport(markers []TodoMarker, basePath string, total, filesScanned int, truncated, includeBlame bool) string {
	var b strings.Builder

	b.WriteString(fmt.Sprintf("## Technical Debt Scan: %s\n\n", basePath))
	b.WriteString(fmt.Sprintf("**Total Markers: %d** | Files Scanned: %d", total, filesScanned))
	if truncated {
		b.WriteString(fmt.Sprintf(" (showing top %d)", len(markers)))
	}
	b.WriteString("\n\n")

	if total == 0 {
		b.WriteString("No TODO/FIXME/HACK/XXX/BUG/NOTE markers found. Codebase is clean.\n")
		return b.String()
	}

	catCounts := make(map[string]int)
	sevCounts := make(map[string]int)
	fileCounts := make(map[string]int)
	for _, m := range markers {
		catCounts[m.Category]++
		sevCounts[m.Severity]++
		fileCounts[m.File]++
	}

	b.WriteString("**By Category:** ")
	cats := make([]string, 0, len(catCounts))
	for c := range catCounts {
		cats = append(cats, c)
	}
	sort.Strings(cats)
	parts := make([]string, 0, len(cats))
	for _, c := range cats {
		parts = append(parts, fmt.Sprintf("%s: %d", c, catCounts[c]))
	}
	b.WriteString(strings.Join(parts, ", "))
	b.WriteString("\n\n")

	if sevCounts["critical"] > 0 || sevCounts["high"] > 0 {
		b.WriteString("**Severity:** ")
		sevParts := []string{}
		if sevCounts["critical"] > 0 {
			sevParts = append(sevParts, fmt.Sprintf("%d critical (FIXME/BUG)", sevCounts["critical"]))
		}
		if sevCounts["high"] > 0 {
			sevParts = append(sevParts, fmt.Sprintf("%d high (TODO/stale)", sevCounts["high"]))
		}
		if sevCounts["medium"] > 0 {
			sevParts = append(sevParts, fmt.Sprintf("%d medium (HACK/XXX)", sevCounts["medium"]))
		}
		if sevCounts["low"] > 0 {
			sevParts = append(sevParts, fmt.Sprintf("%d low (NOTE)", sevCounts["low"]))
		}
		b.WriteString(strings.Join(sevParts, ", "))
		b.WriteString("\n\n")
	}

	if len(fileCounts) > 1 {
		type fc struct {
			file  string
			count int
		}
		var sorted []fc
		for f, c := range fileCounts {
			sorted = append(sorted, fc{file: f, count: c})
		}
		sort.Slice(sorted, func(i, j int) bool { return sorted[i].count > sorted[j].count })
		topN := 5
		if len(sorted) < topN {
			topN = len(sorted)
		}
		b.WriteString("### Top Files by Marker Count\n\n| File | Count |\n|------|-------|\n")
		for i := 0; i < topN; i++ {
			relFile, _ := filepath.Rel(basePath, sorted[i].file)
			b.WriteString(fmt.Sprintf("| %s | %d |\n", relFile, sorted[i].count))
		}
		b.WriteString("\n")
	}

	b.WriteString("### Markers\n\n")
	if includeBlame {
		b.WriteString("| Severity | Category | File:Line | Text | Author | Age |\n")
		b.WriteString("|----------|----------|-----------|------|--------|-----|\n")
	} else {
		b.WriteString("| Severity | Category | File:Line | Text |\n")
		b.WriteString("|----------|----------|-----------|------|\n")
	}
	for _, m := range markers {
		relFile, err := filepath.Rel(basePath, m.File)
		if err != nil {
			relFile = m.File
		}
		text := m.Text
		if text == "" {
			text = "(no description)"
		}

		text = strings.ReplaceAll(text, "|", "\\|")

		if includeBlame {
			author := m.Author
			if author == "" {
				author = "?"
			}
			age := m.Age
			if age == "" {
				age = "?"
			}
			b.WriteString(fmt.Sprintf("| %s | %s | %s:%d | %s | %s | %s |\n",
				m.Severity, m.Category, relFile, m.Line, text, author, age))
		} else {
			b.WriteString(fmt.Sprintf("| %s | %s | %s:%d | %s |\n",
				m.Severity, m.Category, relFile, m.Line, text))
		}
	}

	return b.String()
}

func (t ScanTodosTool) Clone() Tool {
	return &ScanTodosTool{WorkingDir: t.WorkingDir}
}
