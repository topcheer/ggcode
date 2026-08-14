package agent

// Duplicate Code Detection (Post-Edit)
//
// AI agents frequently introduce copy-paste code patterns: near-identical
// function bodies, duplicated logic blocks, or structurally repeated code
// fragments. This is a well-known code quality issue (detected by tools like
// jscpd, dupl, SonarQube) that increases maintenance burden and bug surface.
//
// Research basis:
//   - "A Large-Scale Study of Code Clones in Open Source Projects" (Bellon
//     et al.): 5-15% of code in typical projects is duplicated.
//   - LLM-specific studies show agents produce copy-paste patterns at higher
//     rates than human developers because they generate code by pattern
//     matching rather than refactoring.
//   - Claude Code, Cursor, Cline: none detect code duplication at write-time.
//     They rely on external linters run post-hoc.
//
// ggcode's approach: AST-based function body fingerprinting for Go files.
// After each edit, we parse the file, extract all function/method bodies,
// normalize them (strip identifiers, keep structure), and compare for
// structural similarity. Delta-aware: only flags duplicates introduced by
// the current edit (at least one of the pair must be new/modified).
//
// Detection strategy:
//   - Type 1 clones: Exact token sequence after normalizing whitespace/comments.
//   - Type 2 clones: Identifier-renamed clones (replace all identifiers with
//     a placeholder, then compare token sequences).
//   - Minimum body size: 5 statements (avoids trivial getters/setters).
//   - Similarity threshold: 85% token overlap for near-duplicates.

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
)

// duplicateCodeMaxWarnings caps the number of duplication warnings per write.
const duplicateCodeMaxWarnings = 3

// duplicateMinStmts is the minimum number of statements in a function body
// to be considered for duplication analysis. Short functions (getters,
// setters, one-liners) are excluded to avoid false positives.
const duplicateMinStmts = 3

// duplicateMinTokens is the minimum normalized token count for a function
// body to be considered. Very short bodies are too likely to be coincidentally
// similar (e.g., return nil, return err).
const duplicateMinTokens = 20

// duplicateSimilarityThreshold is the minimum ratio of matching tokens for
// two function bodies to be considered near-duplicates.
const duplicateSimilarityThreshold = 0.85

// funcSignature is a normalized representation of a function's body for
// comparison purposes.
type funcSignature struct {
	name     string
	pos      token.Position
	tokens   []string // normalized token sequence
	tokenSet map[string]int
}

// checkDuplicateCode detects structurally similar function bodies within
// a single Go file. Returns warnings for pairs where at least one function
// was introduced or modified by the current edit.
//
// This catches the common LLM failure mode of copy-pasting a function and
// making minor modifications (renaming variables, changing constants) rather
// than extracting shared logic into a helper.
func checkDuplicateCode(filePath, oldContent, newContent string) []string {
	if filepath.Ext(filePath) != ".go" || strings.TrimSpace(newContent) == "" {
		return nil
	}

	fset := token.NewFileSet()
	goAST, err := parser.ParseFile(fset, filePath, newContent, 0)
	if err != nil || goAST == nil {
		return nil
	}

	// Collect all function/method signatures.
	signatures := collectFuncSignatures(fset, goAST)
	if len(signatures) < 2 {
		return nil
	}

	// Build a set of function names present in old content for delta filtering.
	oldFuncNames := extractFuncNames(oldContent)

	var warnings []string
	seen := make(map[string]bool) // deduplicate warning pairs

	for i := 0; i < len(signatures); i++ {
		for j := i + 1; j < len(signatures); j++ {
			sigA := signatures[i]
			sigB := signatures[j]

			// Skip if both functions existed in old content (not delta-aware).
			aIsNew := !oldFuncNames[sigA.name]
			bIsNew := !oldFuncNames[sigB.name]
			if !aIsNew && !bIsNew {
				continue
			}

			similarity := computeSimilarity(sigA, sigB)
			if similarity < duplicateSimilarityThreshold {
				continue
			}

			// Create a canonical pair key for deduplication.
			pairKey := sigA.name + "|" + sigB.name
			if seen[pairKey] {
				continue
			}
			seen[pairKey] = true

			cloneType := "near"
			if similarity >= 1.0 && tokenSeqEqual(sigA.tokens, sigB.tokens) {
				// Frequency-multiset similarity can be 1.0 even when token order
				// differs (e.g., two functions executing the same steps in
				// reverse). Only same-order sequences are exact clones.
				cloneType = "exact"
			}

			warnings = append(warnings, fmt.Sprintf(
				"Duplicate code detected (%s clone, %.0f%% similar): %q at %s:%d and %q at %s:%d have structurally identical bodies. "+
					"Consider extracting shared logic into a helper function to reduce maintenance burden.",
				cloneType, similarity*100,
				sigA.name, filepath.Base(filePath), sigA.pos.Line,
				sigB.name, filepath.Base(filePath), sigB.pos.Line))

			if len(warnings) >= duplicateCodeMaxWarnings {
				break
			}
		}
		if len(warnings) >= duplicateCodeMaxWarnings {
			break
		}
	}

	if len(warnings) > 0 {
		debug.Log("integrity", "duplicate code check found %d pair(s) in %s", len(warnings), filePath)
	}

	return warnings
}

// collectFuncSignatures extracts normalized function body signatures from
// the AST. Returns a slice of funcSignature entries for functions with
// bodies large enough to be meaningful for comparison.
func collectFuncSignatures(fset *token.FileSet, file *ast.File) []funcSignature {
	var sigs []funcSignature

	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil || len(fn.Body.List) < duplicateMinStmts {
			continue
		}

		tokens := normalizeBodyTokens(fn.Body)
		if len(tokens) < duplicateMinTokens {
			continue
		}

		name := funcDeclName(fn)
		pos := fset.Position(fn.Pos())

		tokenSet := make(map[string]int, len(tokens))
		for _, t := range tokens {
			tokenSet[t]++
		}

		sigs = append(sigs, funcSignature{
			name:     name,
			pos:      pos,
			tokens:   tokens,
			tokenSet: tokenSet,
		})
	}

	return sigs
}

// literalKindTokens maps Go token kinds to normalized placeholders.
var literalKindTokens = map[token.Token]string{
	token.STRING: "STR",
	token.INT:    "INT",
	token.FLOAT:  "FLOAT",
	token.CHAR:   "CHAR",
}

// keywordTokens maps AST node types to their fixed normalized tokens.
// This replaces a large switch statement with a table lookup.
var keywordTokens = map[string]string{
	"*ast.ReturnStmt":     "return",
	"*ast.IfStmt":         "if",
	"*ast.ForStmt":        "for",
	"*ast.RangeStmt":      "range",
	"*ast.SwitchStmt":     "switch",
	"*ast.SelectStmt":     "select",
	"*ast.DeferStmt":      "defer",
	"*ast.GoStmt":         "go",
	"*ast.CallExpr":       "call",
	"*ast.SelectorExpr":   ".",
	"*ast.IndexExpr":      "[]",
	"*ast.SliceExpr":      "[:]",
	"*ast.TypeAssertExpr": ".()",
	"*ast.StarExpr":       "*",
	"*ast.CompositeLit":   "{}",
}

// nodeTypeName returns the Go AST type name string for a node.
func nodeTypeName(n ast.Node) string {
	// Using fmt.Sprintf("%T") is the simplest portable approach.
	return fmt.Sprintf("%T", n)
}

// normalizeBodyTokens converts a function body into a normalized token
// sequence suitable for comparison. It replaces identifiers with a generic
// placeholder to catch Type 2 clones (renamed copies).
func normalizeBodyTokens(body *ast.BlockStmt) []string {
	var tokens []string

	ast.Inspect(body, func(n ast.Node) bool {
		if tok, ok := normalizeNodeToken(n); ok {
			tokens = append(tokens, tok)
		}
		return true
	})

	return tokens
}

// normalizeNodeToken converts a single AST node to its normalized token
// representation. Returns (token, true) if the node produces a token,
// (empty, false) otherwise.
func normalizeNodeToken(n ast.Node) (string, bool) {
	switch node := n.(type) {
	case *ast.Ident:
		if len(node.Name) > 0 && node.Name[0] >= 'A' && node.Name[0] <= 'Z' {
			return "E", true
		}
		return "v", true
	case *ast.BasicLit:
		if tok, ok := literalKindTokens[node.Kind]; ok {
			return tok, true
		}
		return "LIT", true
	case *ast.BinaryExpr:
		return node.Op.String(), true
	case *ast.UnaryExpr:
		return node.Op.String(), true
	case *ast.AssignStmt:
		return node.Tok.String(), true
	default:
		if tok, ok := keywordTokens[nodeTypeName(n)]; ok {
			return tok, true
		}
		return "", false
	}
}

// computeSimilarity calculates the cosine-similarity-like overlap between
// two function signatures using their token frequency maps.
func computeSimilarity(a, b funcSignature) float64 {
	// Jaccard-like metric using token frequency counts.
	// intersection = sum of min counts per token
	// union = sum of max counts per token
	var intersection, union int

	// Collect all unique token types.
	allTokens := make(map[string]bool)
	for t := range a.tokenSet {
		allTokens[t] = true
	}
	for t := range b.tokenSet {
		allTokens[t] = true
	}

	for t := range allTokens {
		countA := a.tokenSet[t]
		countB := b.tokenSet[t]

		if countA < countB {
			intersection += countA
			union += countB
		} else {
			intersection += countB
			union += countA
		}
	}

	if union == 0 {
		return 0
	}

	return float64(intersection) / float64(union)
}

// funcDeclName returns a readable name for a function declaration,
// including receiver type for methods (e.g. "*Server.Close").
func funcDeclName(fn *ast.FuncDecl) string {
	if prefix := receiverPrefix(fn.Recv); prefix != "" {
		return prefix + "." + fn.Name.Name
	}
	return fn.Name.Name
}

// receiverPrefix returns the normalized receiver type prefix for a method
// ("T" for value receivers, "*T" for pointer receivers), or "" for
// plain functions.
func receiverPrefix(recv *ast.FieldList) string {
	if recv == nil || len(recv.List) == 0 {
		return ""
	}
	switch t := recv.List[0].Type.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.StarExpr:
		if id, ok := t.X.(*ast.Ident); ok {
			return "*" + id.Name
		}
	}
	return ""
}

// tokenSeqEqual reports whether two normalized token sequences are identical
// position by position.
func tokenSeqEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// extractFuncNames returns a set of function/method names found in the
// source text. Names are keyed consistently with funcDeclName: methods are
// included both with receiver prefix (e.g. "*Server.Close") and bare name
// ("Close"). This keeps the delta filter effective for pre-existing
// duplicate methods. Uses AST parsing when possible; falls back to simple
// pattern matching (bare names) when the old content is not valid Go.
func extractFuncNames(src string) map[string]bool {
	names := make(map[string]bool)

	if file, err := parser.ParseFile(token.NewFileSet(), "old.go", src, 0); err == nil && file != nil {
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok {
				continue
			}
			names[fn.Name.Name] = true
			if prefix := receiverPrefix(fn.Recv); prefix != "" {
				names[prefix+"."+fn.Name.Name] = true
			}
		}
		return names
	}

	// Fallback: regex-style extraction for syntactically invalid old content.
	// Only bare names are extracted here.
	lines := strings.Split(src, "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "func ") {
			continue
		}

		// Extract the function name after "func " and optional receiver.
		rest := trimmed[5:] // skip "func "

		// Skip receiver type if present: (recv Type)
		if strings.HasPrefix(rest, "(") {
			// Find closing paren of receiver.
			depth := 1
			end := -1
			for i := 1; i < len(rest); i++ {
				if rest[i] == '(' {
					depth++
				} else if rest[i] == ')' {
					depth--
					if depth == 0 {
						end = i
						break
					}
				}
			}
			if end >= 0 && end+1 < len(rest) {
				rest = strings.TrimSpace(rest[end+1:])
			}
		}

		// Extract the name token before "(".
		parenIdx := strings.IndexByte(rest, '(')
		if parenIdx > 0 {
			name := strings.TrimSpace(rest[:parenIdx])
			if name != "" {
				names[name] = true
			}
		}
	}

	return names
}
