package agent

// Commented-out Code Block Detection
//
// Research basis: AI coding agents (Claude Code, Cursor, Cline, Aider) frequently
// comment out code instead of deleting it — a habit inherited from human developers
// who fear losing code. For agents, this is purely wasteful:
//   - Dead code clutters the diff and confuses reviewers
//   - Commented blocks are never restored by the agent (unlike git history)
//   - Large commented blocks signal indecision: the agent wasn't sure if the
//     old code was needed, so it left it "just in case"
//   - Commented code accumulates across multiple agent runs, degrading code quality
//
// Competitor analysis:
//   - Claude Code: no commented-code detection; relies on the agent's judgment
//   - Cursor: lint-on-save catches some; diff review makes it visible
//   - Cline/OpenHands: no detection; relies on build/test cycle
//   - Aider: commit-per-edit makes commented blocks visible in diffs, but
//     doesn't actively warn
//   - Devin: SICA overseer doesn't detect commented code patterns
//
// Our approach: detect blocks of commented-out executable code that were
// INTRODUCED by this edit (delta-based, same as debug_sniffer and
// placeholder_check). Only flags NEW commented blocks, not pre-existing ones.
//
// What we detect:
//   1. Multi-line comment blocks containing actual code statements (not prose)
//   2. Language-specific patterns: // code, /* code */, # code, etc.
//   3. Heuristic: lines that look like code (contain =, (), {}, ;, return, if,
//      for, func, def, class, etc.) when commented out in blocks of 3+ lines
//
// What we DON'T flag:
//   - Single-line comments (legitimate documentation)
//   - Comments containing prose explanations
//   - License headers or file headers
//   - Configuration comments (e.g., # config: value)
//   - Commented-out test cases (common and sometimes intentional)

import (
	"fmt"
	"path/filepath"
	"strings"
)

const (
	// commentedBlockThreshold: minimum number of consecutive commented code-like
	// lines before triggering a warning. 3+ lines of commented code-like content
	// almost always indicates commented-out code, not documentation.
	commentedBlockThreshold = 3

	// commentedMaxWarnings caps warnings per write to avoid flooding.
	commentedMaxWarnings = 2
)

// commentCodeIndicators are substrings that strongly suggest a commented line
// contains executable code rather than prose documentation. We check for these
// to distinguish "// This function does X" (documentation) from "// x = foo()"
// (commented-out code).
var commentCodeIndicators = []string{
	"return ",
	"return\t",
	"if ",
	"if(",
	"for ",
	"for(",
	"while ",
	"while(",
	"switch ",
	"switch(",
	"case ",
	"break;",
	"continue;",
	"throw ",
	"raise ",
	"panic(",
	"defer ",
	"go ",
	"await ",
	"await(",
	"yield ",
	"new ",
	"new(",
	"delete ",
	"sizeof(",
	"typedef ",
	"struct{",
	"struct {",
	"interface{",
	"interface {",
	"package ",
	"import ",
	"#include",
	"#import",
	"#define",
	"#pragma",
	"func ",
	"func(",
	"def ",
	"func ",
	"fn ",
	"pub fn ",
	"pubfn ",
	"void ",
	"int ",
	"const ",
	"let ",
	"let ",
	"var ",
	"val ",
	"mut ",
	"type ",
	"enum ",
	"union ",
	"class ",
	"public:",
	"private:",
	"protected:",
	"static ",
	"virtual ",
	"override ",
	"template<",
	"template <",
}

// checkCommentedCodeBlocks detects blocks of commented-out executable code
// that were INTRODUCED by this edit. Returns warning strings (empty if none).
//
// Parameters:
//   - filePath: the file being written (used for language detection)
//   - oldContent: file content before the edit
//   - newContent: file content after the edit
func checkCommentedCodeBlocks(filePath, oldContent, newContent string) []string {
	ext := strings.ToLower(filepath.Ext(filePath))
	commentSyntax := commentSyntaxForExt(ext)
	if commentSyntax == nil {
		return nil // unsupported language
	}

	// Find commented blocks in new content that don't exist in old content.
	newBlocks := findCommentedCodeBlocks(newContent, commentSyntax)
	if len(newBlocks) == 0 {
		return nil
	}

	oldBlocks := findCommentedCodeBlocks(oldContent, commentSyntax)
	oldSet := make(map[string]bool, len(oldBlocks))
	for _, b := range oldBlocks {
		oldSet[b] = true
	}

	var warnings []string
	for _, block := range newBlocks {
		// Only flag NEW blocks (not pre-existing)
		if oldSet[block] {
			continue
		}
		lineCount := strings.Count(block, "\n") + 1
		warnings = append(warnings, fmt.Sprintf(
			"Commented-out code block (%d lines) detected in %s. "+
				"AI agents often leave old code commented out instead of deleting it. "+
				"If this code is no longer needed, remove it — git history preserves the original. "+
				"If you need to keep it temporarily, move it to a separate file or add a clear TODO.",
			lineCount, filepath.Base(filePath)))
		if len(warnings) >= commentedMaxWarnings {
			break
		}
	}

	return warnings
}

// commentSyntax describes the comment syntax for a language family.
type commentSyntaxInfo struct {
	lineComment  string // e.g., "//" for Go/C/JS, "#" for Python/Ruby
	blockOpen    string // e.g., "/*"
	blockClose   string // e.g., "*/"
	hasBlockComm bool
}

func commentSyntaxForExt(ext string) *commentSyntaxInfo {
	switch ext {
	case ".go", ".c", ".cpp", ".cc", ".cxx", ".h", ".hpp", ".java", ".kt",
		".scala", ".swift", ".rs", ".zig", ".dart", ".js", ".jsx", ".ts",
		".tsx", ".cs", ".php", ".css", ".scss", ".less":
		return &commentSyntaxInfo{
			lineComment:  "//",
			blockOpen:    "/*",
			blockClose:   "*/",
			hasBlockComm: true,
		}

	case ".py", ".rb", ".sh", ".bash", ".zsh", ".yaml", ".yml", ".toml",
		".r", ".pl", ".lua", ".tf", ".dockerfile", ".conf", ".cfg",
		".ini", ".makefile", ".cmake":
		return &commentSyntaxInfo{
			lineComment: "#",
		}

	default:
		return nil
	}
}

// findCommentedCodeBlocks scans content for blocks of consecutive comment lines
// that appear to contain executable code (not prose). Returns the raw block text
// for each match.
func findCommentedCodeBlocks(content string, cs *commentSyntaxInfo) []string {
	var blocks []string
	lines := strings.Split(content, "\n")

	var currentBlock []string
	// codeLike counts lines in currentBlock that look like code (neutral
	// empty-comment separators don't count toward the threshold — #152:
	// otherwise godoc example blocks separated by a bare // line get
	// flagged as dead code).
	codeLike := 0

	flush := func() {
		if len(currentBlock) >= commentedBlockThreshold && codeLike >= commentedBlockThreshold {
			blocks = append(blocks, strings.Join(currentBlock, "\n"))
		}
		currentBlock = nil
		codeLike = 0
	}

	// #152: track multi-line /* ... */ spans across lines. Previous
	// extractBlockCommentCode could never return non-empty (dead code).
	inBlockComment := false
	var blockLines []string
	blockCodeLike := 0
	flushBlockComment := func() {
		if len(blockLines) >= commentedBlockThreshold && blockCodeLike >= commentedBlockThreshold {
			blocks = append(blocks, strings.Join(blockLines, "\n"))
		}
		blockLines = nil
		blockCodeLike = 0
	}

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		if inBlockComment {
			// Strip a trailing */ if present; content between is candidate code.
			inner := trimmed
			if idx := strings.Index(inner, cs.blockClose); idx >= 0 {
				inner = inner[:idx]
				inBlockComment = false
			}
			inner = strings.TrimSpace(inner)
			blockLines = append(blockLines, line)
			if inner != "" && looksLikeCode(inner) {
				blockCodeLike++
			}
			if !inBlockComment {
				flushBlockComment()
			}
			continue
		}

		// Check if this line is a line-comment with code-like content
		if isCommentedCodeLine(trimmed, cs.lineComment) {
			currentBlock = append(currentBlock, line)
			if blockContent := strings.TrimSpace(strings.TrimPrefix(trimmed, cs.lineComment)); blockContent != "" && looksLikeCode(blockContent) {
				codeLike++
			}
			continue
		}

		// Non-comment line — flush any accumulated block
		flush()

		// #152: enter a multi-line /* ... */ span when the opener is not
		// closed on the same line.
		if cs.hasBlockComm && strings.Contains(trimmed, cs.blockOpen) {
			rest := trimmed[strings.Index(trimmed, cs.blockOpen)+len(cs.blockOpen):]
			if !strings.Contains(rest, cs.blockClose) {
				inBlockComment = true
				blockLines = append(blockLines, line)
				rest = strings.TrimSpace(rest)
				if rest != "" && looksLikeCode(rest) {
					blockCodeLike++
				}
				continue
			}
		}
	}
	flush()
	if inBlockComment {
		flushBlockComment()
	}

	return blocks
}

// isCommentedCodeLine checks if a trimmed line is a comment that contains
// executable code indicators (not just prose).
func isCommentedCodeLine(trimmed, commentPrefix string) bool {
	// Must start with the comment prefix
	if !strings.HasPrefix(trimmed, commentPrefix) {
		return false
	}

	// Strip the comment prefix to get the "content"
	content := strings.TrimSpace(trimmed[len(commentPrefix):])

	// Empty comment lines are part of blocks but not code indicators
	// — they're allowed as separators within a block.
	if content == "" {
		return true // neutral: extends the block without adding code signal
	}

	// #152: godoc/go-doc example convention — code in documentation is
	// indented after the comment marker ("//\tvals := ..."). Such lines
	// are documentation examples, not commented-out dead code. Content
	// that begins with a tab directly after the marker is doc formatting.
	if strings.HasPrefix(trimmed[len(commentPrefix):], "\t") {
		return false
	}

	// Skip lines that look like documentation, not code:
	// - License headers
	if strings.Contains(strings.ToLower(content), "license") ||
		strings.Contains(strings.ToLower(content), "copyright") {
		return false
	}

	// Check if the content looks like code
	return looksLikeCode(content)
}

// looksLikeCode applies heuristics to determine if comment content resembles
// executable code rather than prose documentation.
func looksLikeCode(content string) bool {
	// Strong indicators: assignment or function call patterns
	if strings.Contains(content, "=") && !strings.Contains(content, "==") {
		// assignment like x = 5 or x := 5
		return true
	}

	// Function call pattern: identifier followed by (
	if strings.Contains(content, "(") && strings.Contains(content, ")") {
		return true
	}

	// Semicolon termination (C-like statements)
	if strings.HasSuffix(content, ";") {
		return true
	}

	// Language-specific keywords
	for _, indicator := range commentCodeIndicators {
		if strings.Contains(content, indicator) {
			return true
		}
	}

	return false
}

// extractBlockCommentCode checks if a single-line or multi-line block comment
// contains code-like content. For simplicity, we check if a /* ... */ on one
// line contains code indicators. Multi-line block comments are handled by
// the line-scanner indirectly (lines within /* */ that also match line comments
// are rare and would be caught by the line comment path in languages that
// support both).
// extractBlockCommentCode retained for compatibility; multi-line block
// comments are now handled by the span tracker in findCommentedCodeBlocks
// (#152 — this function previously could never return non-empty).
func extractBlockCommentCode(line string, cs *commentSyntaxInfo) string {
	return ""
}
