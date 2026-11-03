package tool

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/lsp"
)

var postEditDiagEnabled = true

const postEditDiagTimeout = 800 * time.Millisecond

func postEditDiagnostics(workingDir, filePath string) string {
	if !postEditDiagEnabled {
		return ""
	}
	if workingDir == "" || filePath == "" {
		return ""
	}

	if _, ok := lsp.ResolveServerForWorkspace(workingDir); !ok {
		return ""
	}

	if !isSourceFile(filePath) {
		return ""
	}

	ctx, cancel := context.WithTimeout(context.Background(), postEditDiagTimeout)
	defer cancel()

	diagnostics, err := lsp.Diagnostics(ctx, workingDir, filePath)
	if err != nil {

		if errors.Is(err, lsp.ErrDiagnosticsNotReady) {
			debug.Log("post-edit-diag", "diagnostics not ready for %s (server analysis late); skipping", filePath)
			return ""
		}
		debug.Log("post-edit-diag", "LSP diagnostics failed for %s: %v", filePath, err)

		ClearDiagnosticBaseline(filePath)
		return ""
	}

	newDiags, resolvedCount, hasBaseline := diffAgainstBaseline(filePath, diagnostics)

	var result string
	if hasBaseline {
		result = formatNewDiagnostics(newDiags, resolvedCount)
	} else {
		result = formatDiagnostics(diagnostics)
	}

	if siblings := checkSiblingDiagnostics(ctx, workingDir, filePath); siblings != "" {
		result += siblings
	}

	if crossPkg := checkCrossPackageDiagnostics(ctx, workingDir, filePath); crossPkg != "" {
		result += crossPkg
	}

	return result
}

func checkSiblingDiagnostics(ctx context.Context, workingDir, filePath string) string {
	siblings, err := lsp.SiblingDiagnostics(ctx, workingDir, filePath)
	if err != nil || len(siblings) == 0 {
		return ""
	}

	var errors []string
	var warnings []string
	var infos []string
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
		} else {

			infos = append(infos, formatted)
		}
	}

	if len(errors) == 0 && len(warnings) == 0 && len(infos) == 0 {
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
	if len(infos) > 0 {
		if len(errors) > 0 || len(warnings) > 0 {
			b.WriteString("\n")
		}

		b.WriteString(fmt.Sprintf("Info (%d) in other files (may block compilation, e.g. unused imports):\n", len(infos)))
		shown := infos
		if len(shown) > 5 {
			shown = shown[:5]
			b.WriteString(fmt.Sprintf("  ... and %d more\n", len(infos)-5))
		}
		for _, in := range shown {
			b.WriteString(in + "\n")
		}
	}

	b.WriteString("Sibling files in this package report errors. These may pre-date your edit — check whether they reference symbols you changed before fixing.")
	return b.String()
}

func checkCrossPackageDiagnostics(ctx context.Context, workingDir, filePath string) string {
	cross, err := lsp.CrossPackageDiagnostics(ctx, workingDir, filePath)
	if err != nil || len(cross) == 0 {
		return ""
	}

	var errors []string
	var warnings []string
	var infos []string
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
		} else {

			infos = append(infos, formatted)
		}
	}

	if len(errors) == 0 && len(warnings) == 0 && len(infos) == 0 {
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

	b.WriteString("Files importing this package report errors. These may pre-date your edit — check whether they call symbols you changed before fixing.")
	return b.String()
}

func shortenForDisplay(absPath, workingDir string) string {
	if workingDir != "" {
		if rel, err := filepath.Rel(workingDir, absPath); err == nil && !strings.HasPrefix(rel, "..") {
			return rel
		}
	}
	return absPath
}

func formatDiagnostics(diagnostics []lsp.Diagnostic) string {
	if len(diagnostics) == 0 {
		return ""
	}

	var errors []string
	var warnings []string
	var infos []string
	for _, d := range diagnostics {

		msg := strings.TrimSpace(d.Message)
		if msg == "" {
			continue
		}
		line := d.Range.Start.Line + 1
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
