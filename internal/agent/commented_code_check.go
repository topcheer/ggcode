package agent

import (
	"fmt"
	"path/filepath"
	"strings"
)

const (
	commentedBlockThreshold = 3

	commentedMaxWarnings = 2
)

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

func checkCommentedCodeBlocks(filePath, oldContent, newContent string) []string {
	ext := strings.ToLower(filepath.Ext(filePath))
	commentSyntax := commentSyntaxForExt(ext)
	if commentSyntax == nil {
		return nil
	}

	newBlocks := findCommentedCodeBlocks(newContent, commentSyntax)
	if len(newBlocks) == 0 {
		return nil
	}

	oldBlocks := findCommentedCodeBlocks(oldContent, commentSyntax)

	oldCounts := make(map[string]int, len(oldBlocks))
	for _, b := range oldBlocks {
		oldCounts[b]++
	}

	var warnings []string
	for _, block := range newBlocks {

		if oldCounts[block] > 0 {
			oldCounts[block]--
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

type commentSyntaxInfo struct {
	lineComment  string
	blockOpen    string
	blockClose   string
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

func findCommentedCodeBlocks(content string, cs *commentSyntaxInfo) []string {
	var blocks []string
	lines := strings.Split(content, "\n")

	var currentBlock []string

	codeLike := 0

	flush := func() {
		if len(currentBlock) >= commentedBlockThreshold && codeLike >= commentedBlockThreshold {
			blocks = append(blocks, strings.Join(currentBlock, "\n"))
		}
		currentBlock = nil
		codeLike = 0
	}

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

		if isCommentedCodeLine(trimmed, cs.lineComment) {
			currentBlock = append(currentBlock, line)
			if blockContent := strings.TrimSpace(strings.TrimPrefix(trimmed, cs.lineComment)); blockContent != "" && looksLikeCode(blockContent) {
				codeLike++
			}
			continue
		}

		flush()

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

func isCommentedCodeLine(trimmed, commentPrefix string) bool {

	if !strings.HasPrefix(trimmed, commentPrefix) {
		return false
	}

	content := strings.TrimSpace(trimmed[len(commentPrefix):])

	if content == "" {
		return true
	}

	if strings.HasPrefix(trimmed[len(commentPrefix):], "\t") {
		return false
	}

	if strings.Contains(strings.ToLower(content), "license") ||
		strings.Contains(strings.ToLower(content), "copyright") {
		return false
	}

	return looksLikeCode(content)
}

func looksLikeCode(content string) bool {

	if strings.Contains(content, "=") && !strings.Contains(content, "==") {

		return true
	}

	if strings.HasSuffix(content, ";") {
		return true
	}

	if !hasProseSignal(content) {
		for _, indicator := range commentCodeIndicators {
			if strings.Contains(content, indicator) {
				return true
			}
		}
	}

	if !hasProseSignal(content) && hasCallPattern(content) {
		return true
	}

	return false
}

var proseSignalWords = map[string]bool{
	"a": true, "an": true, "the": true, "and": true, "or": true,
	"but": true, "is": true, "are": true, "was": true, "were": true,
	"be": true, "been": true, "of": true, "to": true, "in": true,
	"on": true, "at": true, "by": true, "with": true, "from": true,
	"as": true, "into": true, "than": true, "then": true, "this": true,
	"that": true, "these": true, "those": true, "it": true, "its": true,
	"we": true, "our": true, "you": true, "your": true, "they": true,
	"their": true, "not": true, "no": true, "do": true, "does": true,
	"did": true, "have": true, "has": true, "had": true, "will": true,
	"would": true, "can": true, "could": true, "should": true, "may": true,
	"might": true, "must": true, "when": true, "where": true, "which": true,
	"who": true, "whose": true, "why": true, "how": true, "what": true,
	"also": true, "only": true, "just": true, "any": true, "all": true,
	"some": true, "such": true, "both": true, "each": true, "other": true,
	"more": true, "most": true, "many": true, "see": true, "above": true,
	"below": true, "use": true, "used": true, "using": true, "via": true,
	"per": true, "over": true, "under": true, "once": true, "here": true,
	"there": true, "again": true, "further": true,
}

func hasProseSignal(content string) bool {
	trimmed := strings.TrimSpace(content)
	if strings.HasSuffix(trimmed, ".") {
		return true
	}
	for _, f := range strings.Fields(strings.ToLower(trimmed)) {
		f = strings.Trim(f, ".,;:!?()\"'`")
		if proseSignalWords[f] {
			return true
		}
	}
	return false
}

func hasCallPattern(content string) bool {
	for i := 0; i < len(content); i++ {
		if content[i] != '(' {
			continue
		}
		if i == 0 || !isIdentCharCCC(content[i-1]) {
			continue
		}
		if strings.Contains(content[i:], ")") {
			return true
		}
	}
	return false
}

func isIdentCharCCC(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}
