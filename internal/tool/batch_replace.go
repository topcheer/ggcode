package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"
	"sync"

	"github.com/topcheer/ggcode/internal/safego"
)

const (
	maxBatchReplaceFiles        = 20
	maxBatchReplaceMatches      = 500
	maxBatchReplacePatternLen   = 10 * 1024
	maxBatchReplaceReplaceLen   = 100 * 1024
	maxBatchReplacePayloadBytes = 5 * 1024 * 1024 // 5MB combined file content
)

// BatchReplace applies a literal or regex find-and-replace across multiple files
// in a single tool call. This is the "codemod" use case: renaming a symbol,
// replacing a deprecated API call, or standardizing a pattern across a codebase
// without reading and editing each file individually.
type BatchReplace struct {
	SandboxCheck AllowedPathChecker
	WorkingDir   string
}

func (t BatchReplace) Name() string { return "batch_replace" }

func (t BatchReplace) Description() string {
	return "Find-and-replace a pattern across multiple files in one call (codemod/refactoring). " +
		"Supports literal strings and regex. Use this instead of reading+editing each file individually " +
		"when you need the same substitution in many files (e.g. rename a symbol, fix a deprecated API). " +
		"Returns a per-file summary. Files with zero matches are skipped. " +
		"Use dry_run=true to preview without writing."
}

func (t BatchReplace) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"pattern": {
				"type": "string",
				"description": "Text to find. Literal string by default, or a regex pattern when is_regex=true."
			},
			"replacement": {
				"type": "string",
				"description": "Replacement text. For regex mode, use $1, $2, etc. for capture group references. Use $0 for the full match."
			},
			"files": {
				"type": "array",
				"description": "Absolute paths of files to process. Files that don't contain a match are silently skipped. Max 20 files.",
				"items": { "type": "string" }
			},
			"is_regex": {
				"type": "boolean",
				"description": "If true, treat 'pattern' as a Go regexp pattern. Default false (literal string match).",
				"default": false
			},
			"dry_run": {
				"type": "boolean",
				"description": "If true, preview changes without writing to disk. Returns match counts and sample diffs. Default false.",
				"default": false
			},
			"description": {
				"type": "string",
				"description": "Optional. Brief activity label shown in the UI in the user's language."
			}
		},
		"required": ["pattern", "replacement", "files"]
	}`)
}

// batchReplaceFileResult is the per-file outcome.
type batchReplaceFileResult struct {
	Path       string `json:"path"`
	Status     string `json:"status"`                // "changed", "skipped", "error"
	Matches    int    `json:"matches,omitempty"`     // number of replacements
	SampleDiff string `json:"sample_diff,omitempty"` // truncated first diff line (dry_run only)
	Error      string `json:"error,omitempty"`
}

// batchReplaceContent is the full structured output.
type batchReplaceContent struct {
	Summary      string                   `json:"summary"`
	FilesChanged int                      `json:"files_changed"`
	FilesSkipped int                      `json:"files_skipped"`
	FilesError   int                      `json:"files_error"`
	TotalMatches int                      `json:"total_matches"`
	DryRun       bool                     `json:"dry_run"`
	Results      []batchReplaceFileResult `json:"results"`
}

func (t BatchReplace) Execute(ctx context.Context, input json.RawMessage) (Result, error) {
	_ = ctx
	var args struct {
		Pattern     string   `json:"pattern"`
		Replacement string   `json:"replacement"`
		Files       []string `json:"files"`
		IsRegex     bool     `json:"is_regex"`
		DryRun      bool     `json:"dry_run"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("invalid input: %v", err)}, nil
	}

	if args.Pattern == "" {
		return Result{IsError: true, Content: "pattern must not be empty"}, nil
	}
	if len(args.Pattern) > maxBatchReplacePatternLen {
		return Result{IsError: true, Content: fmt.Sprintf("pattern too long: %d bytes, max %d", len(args.Pattern), maxBatchReplacePatternLen)}, nil
	}
	if len(args.Replacement) > maxBatchReplaceReplaceLen {
		return Result{IsError: true, Content: fmt.Sprintf("replacement too long: %d bytes, max %d", len(args.Replacement), maxBatchReplaceReplaceLen)}, nil
	}
	if len(args.Files) == 0 {
		return Result{IsError: true, Content: "files array must not be empty"}, nil
	}
	if len(args.Files) > maxBatchReplaceFiles {
		return Result{IsError: true, Content: fmt.Sprintf("too many files: got %d, max %d", len(args.Files), maxBatchReplaceFiles)}, nil
	}

	// Compile regex if needed.
	var re *regexp.Regexp
	if args.IsRegex {
		var err error
		re, err = regexp.Compile(args.Pattern)
		if err != nil {
			return Result{IsError: true, Content: fmt.Sprintf("invalid regex pattern: %v", err)}, nil
		}
	}

	// Deduplicate file paths preserving order.
	seen := map[string]struct{}{}
	var paths []string
	for _, f := range args.Files {
		path, err := cleanAbsolutePath(f)
		if err != nil {
			return Result{IsError: true, Content: fmt.Sprintf("invalid file path %q: %v", f, err)}, nil
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		paths = append(paths, path)
	}

	// Process files concurrently.
	type procResult struct {
		path       string
		status     string
		matches    int
		newContent string
		sampleDiff string
		errMsg     string
	}
	procResults := make([]procResult, len(paths))

	concurrency := 5
	if len(paths) < concurrency {
		concurrency = len(paths)
	}
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup
	totalPayloadBytes := 0
	var payloadMu sync.Mutex

	for i, path := range paths {
		wg.Add(1)
		go func(idx int, p string) {
			defer wg.Done()
			defer safego.Recover("tool.batch_replace")
			sem <- struct{}{}
			defer func() { <-sem }()

			if t.SandboxCheck != nil && !t.SandboxCheck(p) {
				procResults[idx] = procResult{path: p, status: "error", errMsg: "path not allowed by sandbox policy"}
				return
			}
			data, err := os.ReadFile(p)
			if err != nil {
				procResults[idx] = procResult{path: p, status: "error", errMsg: fmt.Sprintf("error reading file: %v", err)}
				return
			}

			payloadMu.Lock()
			totalPayloadBytes += len(data)
			payloadMu.Unlock()

			content := string(data)
			var newContent string
			var matchCount int

			if args.IsRegex {
				matches := re.FindAllStringSubmatchIndex(content, -1)
				matchCount = len(matches)
				if matchCount > 0 {
					newContent = re.ReplaceAllString(content, args.Replacement)
				} else {
					newContent = content // no change
				}
			} else {
				matchCount = strings.Count(content, args.Pattern)
				if matchCount > 0 {
					newContent = strings.ReplaceAll(content, args.Pattern, args.Replacement)
				} else {
					newContent = content // no change
				}
			}

			if matchCount == 0 {
				procResults[idx] = procResult{path: p, status: "skipped", matches: 0}
				return
			}

			// Build a sample diff for dry_run mode (first changed line).
			var sampleDiff string
			if args.DryRun {
				sampleDiff = extractSampleDiff(content, newContent)
			}

			procResults[idx] = procResult{
				path:       p,
				status:     "changed",
				matches:    matchCount,
				newContent: newContent,
				sampleDiff: sampleDiff,
			}
		}(i, path)
	}
	wg.Wait()

	if totalPayloadBytes > maxBatchReplacePayloadBytes {
		return Result{IsError: true, Content: fmt.Sprintf("combined file content too large: %d bytes, max %d. Reduce the number of files.", totalPayloadBytes, maxBatchReplacePayloadBytes)}, nil
	}

	// Assemble results and enforce global match cap.
	out := batchReplaceContent{DryRun: args.DryRun}
	out.Results = make([]batchReplaceFileResult, 0, len(procResults))
	totalMatches := 0
	for _, pr := range procResults {
		r := batchReplaceFileResult{
			Path:       pr.path,
			Status:     pr.status,
			Matches:    pr.matches,
			SampleDiff: pr.sampleDiff,
			Error:      pr.errMsg,
		}
		out.Results = append(out.Results, r)
		if pr.status == "changed" {
			totalMatches += pr.matches
			if totalMatches > maxBatchReplaceMatches {
				return Result{IsError: true, Content: fmt.Sprintf("too many total matches: %d, max %d. Narrow the file set or use a more specific pattern.", totalMatches, maxBatchReplaceMatches)}, nil
			}
			out.FilesChanged++
		} else if pr.status == "skipped" {
			out.FilesSkipped++
		} else if pr.status == "error" {
			out.FilesError++
		}
	}
	out.TotalMatches = totalMatches

	if args.DryRun {
		out.Summary = fmt.Sprintf("[dry_run] %d files: %d would change (%d total matches), %d skipped, %d errors",
			len(paths), out.FilesChanged, out.TotalMatches, out.FilesSkipped, out.FilesError)
		content, err := json.MarshalIndent(out, "", "  ")
		if err != nil {
			return Result{IsError: true, Content: fmt.Sprintf("error marshaling result: %v", err)}, nil
		}
		return Result{Content: string(content)}, nil
	}

	// Write changed files (non-dry-run mode).
	for i := range procResults {
		pr := procResults[i]
		if pr.status != "changed" {
			continue
		}
		if ctx.Err() != nil {
			out.Results[i].Status = "error"
			out.Results[i].Error = "cancelled"
			out.FilesError++
			out.FilesChanged--
			continue
		}
		CaptureDiagnosticBaseline(t.WorkingDir, pr.path)
		writeData, _ := formatGoBytes(pr.path, []byte(pr.newContent))
		if err := atomicWriteFile(pr.path, writeData, 0644); err != nil {
			out.Results[i].Status = "error"
			out.Results[i].Error = fmt.Sprintf("error writing file: %v", err)
			out.FilesError++
			out.FilesChanged--
			continue
		}
		// Post-write checks (reuse existing infrastructure).
		out.Results[i].Error += syntaxCheck(pr.path, []byte(pr.newContent))
		defaultFileTracker.RecordWrite(pr.path)
	}

	out.Summary = fmt.Sprintf("%d files: %d changed (%d total matches), %d skipped, %d errors",
		len(paths), out.FilesChanged, out.TotalMatches, out.FilesSkipped, out.FilesError)

	content, err := json.Marshal(out)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("error marshaling result: %v", err)}, nil
	}
	return Result{Content: string(content), IsError: out.FilesError > 0}, nil
}

// extractSampleDiff finds the first differing line between old and new content
// and returns a truncated diff preview.
func extractSampleDiff(oldContent, newContent string) string {
	oldLines := strings.Split(oldContent, "\n")
	newLines := strings.Split(newContent, "\n")
	maxLines := len(oldLines)
	if len(newLines) > maxLines {
		maxLines = len(newLines)
	}
	for i := 0; i < maxLines; i++ {
		var oldLine, newLine string
		if i < len(oldLines) {
			oldLine = oldLines[i]
		}
		if i < len(newLines) {
			newLine = newLines[i]
		}
		if oldLine != newLine {
			result := fmt.Sprintf("L%d: -%s\n     +%s", i+1, truncateForDiff(oldLine), truncateForDiff(newLine))
			return result
		}
	}
	return ""
}

func truncateForDiff(s string) string {
	const maxLineLen = 120
	if len(s) > maxLineLen {
		return s[:maxLineLen] + "..."
	}
	return s
}
