package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/topcheer/ggcode/internal/codehealth"
)

// CodeHealthTool implements the code_health tool that analyzes Go source
// code for cyclomatic complexity, function length, nesting depth, and other
// quality metrics to identify technical debt hotspots.
type CodeHealthTool struct{ WorkingDir string }

func (t CodeHealthTool) Name() string { return "code_health" }

func (t CodeHealthTool) Description() string {
	return "Analyze Go source code for code quality metrics including cyclomatic complexity, " +
		"function length, nesting depth, and statement count. Returns a ranked report of the most " +
		"complex functions (technical debt hotspots) and a health score. Use to proactively identify " +
		"refactoring targets before making changes, or to assess the quality of code after edits. " +
		"Supports analyzing a directory (recursive) or a single .go file."
}

func (t CodeHealthTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"path": {
				"type": "string",
				"description": "Directory or .go file to analyze (default: current working directory)"
			},
			"max_functions": {
				"type": "integer",
				"description": "Maximum number of functions to return in the report (default: 20)",
				"default": 20
			},
			"threshold": {
				"type": "integer",
				"description": "Minimum cyclomatic complexity to flag as a hotspot (default: 11)",
				"default": 11
			},
			"max_files": {
				"type": "integer",
				"description": "Maximum number of files to scan (0 = no limit, default: 500)"
			},
			"description": {
				"type": "string",
				"description": "REQUIRED. Brief activity label shown in the UI."
			}
		},
		"required": ["description"]
	}`)
}

func (t CodeHealthTool) Execute(ctx context.Context, input json.RawMessage) (Result, error) {
	var args struct {
		Path         string `json:"path"`
		MaxFunctions int    `json:"max_functions"`
		Threshold    int    `json:"threshold"`
		MaxFiles     int    `json:"max_files"`
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

	opts := codehealth.DefaultOptions()
	if args.MaxFunctions > 0 {
		opts.MaxFunctions = args.MaxFunctions
	}
	if args.Threshold > 0 {
		opts.ThresholdComplexity = args.Threshold
	}
	if args.MaxFiles > 0 {
		opts.MaxFiles = args.MaxFiles
	}

	report, err := codehealth.Analyze(absDir, opts)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("code health analysis failed: %v", err)}, nil
	}

	// Format the report for the LLM
	content := formatReport(report, absDir)

	return Result{Content: content}, nil
}

// formatReport renders the analysis as a human/LLM-readable text report.
func formatReport(r *codehealth.Report, basePath string) string {
	var b strings.Builder

	b.WriteString(fmt.Sprintf("## Code Health Report: %s\n\n", r.Path))
	b.WriteString(fmt.Sprintf("**Health Score: %d/100** | Files: %d | Functions: %d | Avg Complexity: %.1f | Max Complexity: %d\n\n",
		r.HealthScore, r.FilesScanned, r.Functions, r.AvgComplexity, r.MaxComplexity))

	if len(r.TopFunctions) == 0 {
		b.WriteString("No functions exceed the complexity threshold. Code health looks good.\n")
		return b.String()
	}

	// Severity summary
	var critical, high, medium int
	for _, f := range r.TopFunctions {
		switch f.Severity {
		case codehealth.SeverityCritical:
			critical++
		case codehealth.SeverityHigh:
			high++
		case codehealth.SeverityMedium:
			medium++
		}
	}
	if critical > 0 || high > 0 || medium > 0 {
		b.WriteString("**Severity Distribution:** ")
		parts := []string{}
		if critical > 0 {
			parts = append(parts, fmt.Sprintf("%d critical (>30)", critical))
		}
		if high > 0 {
			parts = append(parts, fmt.Sprintf("%d high (20-30)", high))
		}
		if medium > 0 {
			parts = append(parts, fmt.Sprintf("%d medium (11-19)", medium))
		}
		b.WriteString(strings.Join(parts, ", "))
		b.WriteString("\n\n")
	}

	// Top functions table
	b.WriteString("### Top Complex Functions (Refactoring Targets)\n\n")
	b.WriteString("| Severity | Complexity | Function | File:Line | Length | Nesting | Params |\n")
	b.WriteString("|----------|------------|----------|-----------|--------|---------|--------|\n")
	for _, f := range r.TopFunctions {
		relFile, err := filepath.Rel(basePath, f.File)
		if err != nil {
			relFile = f.File
		}
		b.WriteString(fmt.Sprintf("| %s | %d | `%s` | %s:%d | %d | %d | %d |\n",
			f.Severity, f.Complexity, f.Function, relFile, f.Line, f.Length, f.NestingDepth, f.Params))
	}

	// File summaries
	if len(r.FileSummaries) > 0 {
		b.WriteString("\n### Files by Max Complexity\n\n")
		b.WriteString("| File | Functions | Max Complexity | Avg Complexity | Statements |\n")
		b.WriteString("|------|-----------|----------------|----------------|------------|\n")
		for _, fs := range r.FileSummaries {
			relFile, err := filepath.Rel(basePath, fs.File)
			if err != nil {
				relFile = fs.File
			}
			b.WriteString(fmt.Sprintf("| %s | %d | %d | %.1f | %d |\n",
				relFile, fs.Functions, fs.MaxComplexity, fs.AvgComplexity, fs.TotalStatements))
		}
	}

	// Refactoring guidance
	if r.HealthScore < 70 {
		b.WriteString("\n> **Recommendation:** Multiple high-complexity functions detected. ")
		b.WriteString("Consider refactoring functions with complexity > 20 by extracting helper functions, ")
		b.WriteString("reducing nesting via early returns, or splitting large switch/if-else chains.\n")
	}

	return b.String()
}

// Clone returns an independent copy of this tool for use by a different agent.
func (t CodeHealthTool) Clone() Tool {
	return &CodeHealthTool{WorkingDir: t.WorkingDir}
}
