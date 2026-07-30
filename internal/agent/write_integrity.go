package agent

import (
	"fmt"
	"go/parser"
	"go/scanner"
	"go/token"
	"path/filepath"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
)

// Post-write file integrity validation.
//
// Research basis: Claude Code uses LSP for immediate post-edit syntax feedback;
// Cursor runs in-process diagnostics; OpenHands/Cline rely on post-edit build
// verification. Aider shows a live diff and validates structure before commit.
//
// ggcode already has LSP diagnostics integration and verify hints, but LSP
// requires a running language server (not always available, e.g. for generated
// code or exotic languages) and verify hints only suggest running the build
// command — they don't catch the error inline.
//
// This module provides a lightweight, always-available structural validation
// that runs synchronously after successful file writes and catches the most
// common post-edit issues with zero external dependencies:
//
//  1. Go syntax errors — uses go/parser from the standard library to catch
//     syntax issues immediately (<1ms for typical files). This is the most
//     impactful check since this is a Go project and syntax errors are the
//     #1 cause of failed builds after agent edits.
//  2. Binary corruption — null bytes in what should be a text file indicate
//     encoding issues or accidental binary writes.
//  3. Content loss — a non-empty file becoming empty/whitespace-only after an
//     edit signals a catastrophic edit failure (e.g., old_text consumed the
//     entire file).
//
// When issues are found, a concise warning is injected into the tool result so
// the agent can fix the problem in the same turn, avoiding a wasted build/test
// cycle iteration. The check is non-blocking and cannot hang (go/parser is a
// pure in-memory operation).

const (
	// maxIntegrityWarnings caps the number of warnings per write to avoid
	// flooding the tool result with excessive output.
	maxIntegrityWarnings = 3

	// maxGoSyntaxErrors limits how many Go syntax errors we report. Go files
	// with many errors produce a cascade; the first 2 are usually the root cause.
	maxGoSyntaxErrors = 2
)

// checkWriteIntegrity validates the content of a file after a write/edit.
// Returns a non-empty guidance string if issues are detected.
//
// Parameters:
//   - filePath: the path of the written file (used for language detection)
//   - oldContent: the file content before the write ("" for new files)
//   - newContent: the file content after the write
func checkWriteIntegrity(filePath, oldContent, newContent string) string {
	var warnings []string

	// 1. Binary corruption: null bytes in what should be a text file.
	if strings.ContainsRune(newContent, 0) {
		count := strings.Count(newContent, "\x00")
		warnings = append(warnings,
			fmt.Sprintf("File contains %d null byte(s) (\\x00) — content may be corrupted or incorrectly encoded. Check encoding and re-write if needed.", count))
	}

	// 2. Content loss: non-empty source file became empty/whitespace-only.
	//    This catches the common failure where edit_file's old_text matches
	//    and removes the entire file content.
	if strings.TrimSpace(oldContent) != "" && strings.TrimSpace(newContent) == "" {
		warnings = append(warnings,
			fmt.Sprintf("This edit resulted in an EMPTY file (was %d bytes before). "+
				"Verify this was intended — the old_text match may have consumed the entire file content.", len(oldContent)))
	}

	// 3. Go syntax check for .go files — catches syntax errors immediately.
	if filepath.Ext(filePath) == ".go" && strings.TrimSpace(newContent) != "" {
		if syntaxWarnings := checkGoSyntax(filePath, newContent); len(syntaxWarnings) > 0 {
			warnings = append(warnings, syntaxWarnings...)
		}
	}

	// 4. Debug statement detection — flags leftover debug prints/logs that
	//    agents commonly introduce (console.log, debugger, dd(), etc.).
	if debugWarnings := checkDebugStatements(filePath, oldContent, newContent); len(debugWarnings) > 0 {
		warnings = append(warnings, debugWarnings...)
	}

	if len(warnings) == 0 {
		return ""
	}

	// Cap warnings to avoid excessive output.
	if len(warnings) > maxIntegrityWarnings {
		warnings = warnings[:maxIntegrityWarnings]
	}

	debug.Log("integrity", "post-write check found %d issue(s) in %s", len(warnings), filePath)

	var b strings.Builder
	b.WriteString("[Post-write integrity check]\n")
	for i, w := range warnings {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(w)
	}
	return b.String()
}

// checkGoSyntax parses Go source and returns syntax error descriptions.
// Uses go/parser from the standard library — fast (<1ms for typical files)
// and cannot hang (pure in-memory operation).
func checkGoSyntax(filename, src string) []string {
	fset := token.NewFileSet()
	_, err := parser.ParseFile(fset, filename, src, 0)
	if err == nil {
		return nil
	}

	var warnings []string

	// go/parser wraps errors in scanner.ErrorList (a []*scanner.Error).
	// Each error has a position and message, e.g. "main.go:5:2: expected declaration, found 'if'".
	if el, ok := err.(scanner.ErrorList); ok {
		for i, e := range el {
			if i >= maxGoSyntaxErrors {
				remaining := len(el) - maxGoSyntaxErrors
				warnings = append(warnings,
					fmt.Sprintf("...and %d more syntax error(s) in %s", remaining, filename))
				break
			}
			warnings = append(warnings, e.Error())
		}
		return warnings
	}

	// Fallback for non-ErrorList errors.
	warnings = append(warnings, err.Error())
	return warnings
}
