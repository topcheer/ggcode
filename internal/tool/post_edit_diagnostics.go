package tool

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/lsp"
)

// postEditDiagEnabled controls whether post-edit LSP diagnostics are shown.
// Defaults to true. Can be disabled via configuration.
var postEditDiagEnabled = true

// SetPostEditDiagEnabled enables or disables post-edit LSP diagnostics globally.
func SetPostEditDiagEnabled(enabled bool) {
	postEditDiagEnabled = enabled
}

// postEditDiagTimeout is the maximum time to wait for LSP diagnostics after
// an edit. Kept under 1s to avoid blocking the agent loop — if the LSP
// server is slow to respond, we skip diagnostics rather than stalling the
// edit tool result. The previous 3s timeout caused multi-second perceived
// latency on every edit_file/write_file call.
const postEditDiagTimeout = 800 * time.Millisecond

// postEditDiagnostics fetches LSP diagnostics for a file after an edit and
// returns a formatted warning string if any errors or warnings are found.
//
// If a diagnostic baseline was captured before the edit (via
// CaptureDiagnosticBaseline), only NEWLY INTRODUCED diagnostics are shown —
// pre-existing issues are suppressed to reduce noise and focus the agent on
// problems caused by its own change.
//
// Returns empty string if:
//   - post-edit diagnostics are disabled
//   - no LSP server is available for the file's language
//   - the LSP call fails or times out
//   - no errors/warnings are found (only info/hints are suppressed)
//   - baseline diff shows zero new issues (clean edit)
//
// This gives the agent immediate, actionable feedback after code edits,
// without requiring a separate lsp_diagnostics tool call or waiting for the
// async verify loop at the end of the agent turn.
func postEditDiagnostics(workingDir, filePath string) string {
	if !postEditDiagEnabled {
		return ""
	}
	if workingDir == "" || filePath == "" {
		return ""
	}

	// Quick check: is an LSP server available for this workspace?
	if _, ok := lsp.ResolveServerForWorkspace(workingDir); !ok {
		return ""
	}

	// Only check source files — skip markdown, images, configs, etc.
	if !isSourceFile(filePath) {
		return ""
	}

	ctx, cancel := context.WithTimeout(context.Background(), postEditDiagTimeout)
	defer cancel()

	diagnostics, err := lsp.Diagnostics(ctx, workingDir, filePath)
	if err != nil {
		debug.Log("post-edit-diag", "LSP diagnostics failed for %s: %v", filePath, err)
		// Clear any stale baseline since we couldn't get post-edit diagnostics.
		ClearDiagnosticBaseline(filePath)
		return ""
	}

	// Diff against pre-edit baseline if available. When a baseline exists,
	// only show NEWLY INTRODUCED diagnostics — pre-existing issues are
	// suppressed to keep the signal focused on what the agent's edit caused.
	newDiags, resolvedCount, hasBaseline := diffAgainstBaseline(filePath, diagnostics)

	var result string
	if hasBaseline {
		result = formatNewDiagnostics(newDiags, resolvedCount)
	} else {
		result = formatDiagnostics(diagnostics)
	}

	// Check sibling files in the same directory for cross-file errors.
	// gopls pushes diagnostics for all files in a Go package; this catches
	// errors in sibling files (e.g., callers of a renamed function) that
	// the per-file Diagnostics call above misses.
	if siblings := checkSiblingDiagnostics(ctx, workingDir, filePath); siblings != "" {
		result += siblings
	}

	// Check files in OTHER packages that import the edited file's package.
	// This catches cross-package breakage: editing a function signature in
	// package A can break callers in package B that import A.
	if crossPkg := checkCrossPackageDiagnostics(ctx, workingDir, filePath); crossPkg != "" {
		result += crossPkg
	}

	return result
}

// checkSiblingDiagnostics queries cached LSP diagnostics for Go files in the
// same directory as the edited file, returning formatted warnings for any
// errors or warnings found in sibling files. This is read-only (no LSP
// requests) and very fast.
func checkSiblingDiagnostics(ctx context.Context, workingDir, filePath string) string {
	siblings, err := lsp.SiblingDiagnostics(ctx, workingDir, filePath)
	if err != nil || len(siblings) == 0 {
		return ""
	}

	var errors []string
	var warnings []string
	for _, sd := range siblings {
		msg := strings.TrimSpace(sd.Diagnostic.Message)
		if msg == "" {
			continue
		}
		base := filepath.Base(sd.File)
		line := sd.Diagnostic.Range.Start.Line + 1
		formatted := fmt.Sprintf("  %s:%d: %s", base, line, msg)
		if sd.Diagnostic.Severity <= 1 {
			errors = append(errors, formatted)
		} else if sd.Diagnostic.Severity == 2 {
			warnings = append(warnings, formatted)
		}
	}

	if len(errors) == 0 && len(warnings) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("\n[Cross-file Diagnostics — sibling files in same package]\n")
	if len(errors) > 0 {
		b.WriteString(fmt.Sprintf("Errors (%d) in other files:\n", len(errors)))
		shown := errors
		if len(shown) > 5 {
			shown = shown[:5]
		}
		for _, e := range shown {
			b.WriteString(e + "\n")
		}
		if len(errors) > 5 {
			b.WriteString(fmt.Sprintf("  ... and %d more\n", len(errors)-5))
		}
	}
	if len(warnings) > 0 {
		if len(errors) > 0 {
			b.WriteString("\n")
		}
		b.WriteString(fmt.Sprintf("Warnings (%d) in other files:\n", len(warnings)))
		shown := warnings
		if len(shown) > 3 {
			shown = shown[:3]
		}
		for _, w := range shown {
			b.WriteString(w + "\n")
		}
		if len(warnings) > 3 {
			b.WriteString(fmt.Sprintf("  ... and %d more\n", len(warnings)-3))
		}
	}
	// #856: cached sibling diagnostics may predate this edit (no baseline
	// diff available for other files), so don't assert causality — point the
	// agent at verification instead of chasing edit-caused errors.
	b.WriteString("Sibling files in this package report errors. These may pre-date your edit — check whether they reference symbols you changed before fixing.")
	return b.String()
}

// checkCrossPackageDiagnostics queries cached LSP diagnostics for Go files in
// OTHER packages (different directories) that import the edited file's package.
// This catches cross-package breakage that checkSiblingDiagnostics misses: when
// editing a function in package A breaks callers in package B.
//
// Uses lsp.CrossPackageDiagnostics which reads the cached workspace diagnostics
// maintained by gopls and filters to files that import the edited package.
func checkCrossPackageDiagnostics(ctx context.Context, workingDir, filePath string) string {
	cross, err := lsp.CrossPackageDiagnostics(ctx, workingDir, filePath)
	if err != nil || len(cross) == 0 {
		return ""
	}

	var errors []string
	var warnings []string
	for _, sd := range cross {
		msg := strings.TrimSpace(sd.Diagnostic.Message)
		if msg == "" {
			continue
		}
		display := shortenForDisplay(sd.File, workingDir)
		line := sd.Diagnostic.Range.Start.Line + 1
		formatted := fmt.Sprintf("  %s:%d: %s", display, line, msg)
		if sd.Diagnostic.Severity <= 1 {
			errors = append(errors, formatted)
		} else if sd.Diagnostic.Severity == 2 {
			warnings = append(warnings, formatted)
		}
	}

	if len(errors) == 0 && len(warnings) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("\n[Cross-package Diagnostics — files in other packages that import this package]\n")
	if len(errors) > 0 {
		b.WriteString(fmt.Sprintf("Errors (%d) in dependent packages:\n", len(errors)))
		shown := errors
		if len(shown) > 5 {
			shown = shown[:5]
		}
		for _, e := range shown {
			b.WriteString(e + "\n")
		}
		if len(errors) > 5 {
			b.WriteString(fmt.Sprintf("  ... and %d more\n", len(errors)-5))
		}
	}
	if len(warnings) > 0 {
		if len(errors) > 0 {
			b.WriteString("\n")
		}
		b.WriteString(fmt.Sprintf("Warnings (%d) in dependent packages:\n", len(warnings)))
		shown := warnings
		if len(shown) > 3 {
			shown = shown[:3]
		}
		for _, w := range shown {
			b.WriteString(w + "\n")
		}
		if len(warnings) > 3 {
			b.WriteString(fmt.Sprintf("  ... and %d more\n", len(warnings)-3))
		}
	}
	// #856: same as sibling diagnostics — cross-package errors come from a
	// gopls cache with no baseline; avoid the false 'caused by your edit'.
	b.WriteString("Files importing this package report errors. These may pre-date your edit — check whether they call symbols you changed before fixing.")
	return b.String()
}

// shortenForDisplay converts an absolute path to a project-relative one for display.
func shortenForDisplay(absPath, workingDir string) string {
	if workingDir != "" {
		if rel, err := filepath.Rel(workingDir, absPath); err == nil && !strings.HasPrefix(rel, "..") {
			return rel
		}
	}
	return absPath
}

// formatDiagnostics formats LSP diagnostics into a concise warning string.
// Only errors (severity 1) and warnings (severity 2) are shown — info (3)
// and hints (4) are suppressed to keep the output focused on actionable issues.
// Returns empty string if there are no errors or warnings.
func formatDiagnostics(diagnostics []lsp.Diagnostic) string {
	if len(diagnostics) == 0 {
		return ""
	}

	var errors []string
	var warnings []string
	var infos []string
	for _, d := range diagnostics {
		// LSP severity: 1=Error, 2=Warning, 3=Information, 4=Hint.
		// #1332: mirror the #820 baseline-path classification (0 as Error,
		// 3/4 as Info) - the fallback path (new .go files never capture a
		// baseline) dropped Info diagnostics, hiding gopls "imported and not
		// used" (an actual compile blocker reported at Info severity).
		msg := strings.TrimSpace(d.Message)
		if msg == "" {
			continue
		}
		line := d.Range.Start.Line + 1 // 0-based to 1-based
		formatted := fmt.Sprintf("  L%d: %s", line, msg)
		switch d.Severity {
		case 0, 1:
			errors = append(errors, formatted)
		case 2:
			warnings = append(warnings, formatted)
		case 3, 4:
			infos = append(infos, formatted)
		}
	}

	if len(errors) == 0 && len(warnings) == 0 && len(infos) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("\n\n[LSP Diagnostics — post-edit check]\n")
	if len(errors) > 0 {
		b.WriteString(fmt.Sprintf("Errors (%d):\n", len(errors)))
		// Cap at 10 items to keep output manageable
		shown := errors
		if len(shown) > 10 {
			shown = shown[:10]
		}
		for _, e := range shown {
			b.WriteString(e + "\n")
		}
		if len(errors) > 10 {
			b.WriteString(fmt.Sprintf("  ... and %d more\n", len(errors)-10))
		}
	}
	if len(warnings) > 0 {
		if len(errors) > 0 {
			b.WriteString("\n")
		}
		b.WriteString(fmt.Sprintf("Warnings (%d):\n", len(warnings)))
		shown := warnings
		if len(shown) > 5 {
			shown = shown[:5]
		}
		for _, w := range shown {
			b.WriteString(w + "\n")
		}
		if len(warnings) > 5 {
			b.WriteString(fmt.Sprintf("  ... and %d more\n", len(warnings)-5))
		}
	}
	if len(infos) > 0 {
		if len(errors) > 0 || len(warnings) > 0 {
			b.WriteString("\n")
		}
		b.WriteString(fmt.Sprintf("Info (%d):\n", len(infos)))
		shown := infos
		if len(shown) > 5 {
			shown = shown[:5]
		}
		for _, w := range shown {
			b.WriteString(w + "\n")
		}
		if len(infos) > 5 {
			b.WriteString(fmt.Sprintf("  ... and %d more\n", len(infos)-5))
		}
	}
	b.WriteString("Fix these issues to ensure the code compiles correctly.")
	return b.String()
}

// isSourceFile returns true for file extensions commonly supported by LSP servers.
// This avoids wasting time calling Diagnostics on files like .md, .json, .yaml
// that don't have language servers attached (in most setups).
var sourceExtensions = map[string]bool{
	".go":    true,
	".ts":    true,
	".tsx":   true,
	".js":    true,
	".jsx":   true,
	".py":    true,
	".rs":    true,
	".java":  true,
	".kt":    true,
	".c":     true,
	".cpp":   true,
	".cc":    true,
	".h":     true,
	".hpp":   true,
	".cs":    true,
	".rb":    true,
	".swift": true,
	".dart":  true,
	".lua":   true,
	".zig":   true,
	".php":   true,
	".scala": true,
	".ex":    true,
	".exs":   true,
	".clj":   true,
	".cljs":  true,
	".vim":   true,
}

func isSourceFile(path string) bool {
	return sourceExtensions[filepath.Ext(path)]
}
