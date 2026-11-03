package agent

import (
	"fmt"
	"go/ast"
	"go/token"
	"strings"
)

const maxRangeNilPtrWarnings = 6

type rnpWarning struct {
	text        string
	fingerprint string
}

type rnpNilGuard struct {
	negated    bool
	ifPos      token.Pos
	thenEnd    token.Pos
	ifEnd      token.Pos
	hasElse    bool
	terminates bool
}

func checkRangeNilPtr(filePath, oldContent, newContent string) string {
	if strings.TrimSpace(newContent) == "" {
		return ""
	}

	newWarnings := rnpScanContent(filePath, newContent)

	if len(newWarnings) > 0 && strings.TrimSpace(oldContent) != "" {
		oldSet := rnpScanFingerprints(filePath, oldContent)
		kept := newWarnings[:0]
		for _, w := range newWarnings {
			if !oldSet[w.fingerprint] {
				kept = append(kept, w)
			}
		}
		newWarnings = kept
	}

	if len(newWarnings) == 0 {
		return ""
	}

	msgs := make([]string, 0, len(newWarnings))
	for _, w := range newWarnings {
		msgs = append(msgs, w.text)
	}
	if len(msgs) > maxRangeNilPtrWarnings {
		remaining := len(msgs) - maxRangeNilPtrWarnings
		msgs = msgs[:maxRangeNilPtrWarnings]
		msgs = append(msgs, fmt.Sprintf("...and %d more range-nil-pointer warning(s)", remaining))
	}
	return strings.Join(msgs, "\n")
}

func rnpScanFingerprints(filePath, content string) map[string]bool {
	warnings := rnpScanContent(filePath, content)
	set := make(map[string]bool, len(warnings))
	for _, w := range warnings {
		set[w.fingerprint] = true
	}
	return set
}

func rnpScanContent(filePath, content string) []rnpWarning {
	if strings.TrimSpace(content) == "" {
		return nil
	}
	file, fset, err := parseGoSource(filePath, content, 0)
	if err != nil || file == nil {
		return nil
	}
	lines := strings.Split(content, "\n")

	var warnings []rnpWarning
	for _, decl := range file.Decls {
		fd, ok := decl.(*ast.FuncDecl)
		if !ok || fd.Body == nil {
			continue
		}
		guards := rnpCollectNilGuards(fd.Body)
		ast.Inspect(fd.Body, func(n ast.Node) bool {
			rs, ok := n.(*ast.RangeStmt)
			if !ok {
				return true
			}
			msg, varName := rnpCheckStmt(rs, fset, guards)
			if msg == "" {
				return true
			}
			lineText := rnpLineText(lines, fset.Position(rs.X.Pos()).Line)
			warnings = append(warnings, rnpWarning{
				text:        msg,
				fingerprint: varName + "|" + lineText,
			})
			return true
		})
	}
	return warnings
}

func rnpLineText(lines []string, line int) string {
	if line < 1 || line > len(lines) {
		return ""
	}
	return strings.TrimSpace(lines[line-1])
}

func rnpCollectNilGuards(body *ast.BlockStmt) map[string][]rnpNilGuard {
	guards := make(map[string][]rnpNilGuard)
	ast.Inspect(body, func(n ast.Node) bool {
		ifStmt, ok := n.(*ast.IfStmt)
		if !ok {
			return true
		}
		names, negated, found := rnpNilCompareIdents(ifStmt.Cond)
		if !found {
			return true
		}
		_, isPlainElse := ifStmt.Else.(*ast.BlockStmt)
		for _, name := range names {
			guards[name] = append(guards[name], rnpNilGuard{
				negated:    negated,
				ifPos:      ifStmt.Pos(),
				thenEnd:    ifStmt.Body.End(),
				ifEnd:      ifStmt.End(),
				hasElse:    isPlainElse,
				terminates: ifBodyTerminates(ifStmt.Body),
			})
		}
		return true
	})
	return guards
}

func rnpNilCompareIdents(cond ast.Expr) (names []string, negated, found bool) {

	for {
		paren, isParen := cond.(*ast.ParenExpr)
		if !isParen {
			break
		}
		cond = paren.X
	}
	bin, ok := cond.(*ast.BinaryExpr)
	if !ok {
		return nil, false, false
	}
	if bin.Op == token.LAND {
		if ns := rnpNilCompareLeaves(cond, token.NEQ, token.LAND); len(ns) > 0 {
			return ns, true, true
		}
		return nil, false, false
	}
	if bin.Op == token.LOR {
		if ns := rnpNilCompareLeaves(cond, token.EQL, token.LOR); len(ns) > 0 {
			return ns, false, true
		}
		return nil, false, false
	}
	if bin.Op != token.EQL && bin.Op != token.NEQ {
		return nil, false, false
	}
	negated = bin.Op == token.NEQ
	if rnpIsNilIdent(bin.Y) {
		if n := rnpDottedName(bin.X); n != "" {
			return []string{n}, negated, true
		}
	}
	if rnpIsNilIdent(bin.X) {
		if n := rnpDottedName(bin.Y); n != "" {
			return []string{n}, negated, true
		}
	}
	return nil, false, false
}

func rnpNilCompareLeaves(e ast.Expr, want, chainOp token.Token) []string {

	for {
		paren, isParen := e.(*ast.ParenExpr)
		if !isParen {
			break
		}
		e = paren.X
	}
	if bin, ok := e.(*ast.BinaryExpr); ok && bin.Op == chainOp {
		var out []string
		for _, n := range append(
			rnpNilCompareLeaves(bin.X, want, chainOp),
			rnpNilCompareLeaves(bin.Y, want, chainOp)...,
		) {
			if n != "" {
				out = append(out, n)
			}
		}
		return out
	}
	if n := rnpLeafNilCompare(e, want); n != "" {
		return []string{n}
	}
	return nil
}

func rnpLeafNilCompare(e ast.Expr, want token.Token) string {
	for {
		paren, isParen := e.(*ast.ParenExpr)
		if !isParen {
			break
		}
		e = paren.X
	}
	bin, ok := e.(*ast.BinaryExpr)
	if !ok || bin.Op != want {
		return ""
	}
	if n := rnpDottedName(bin.X); n != "" && rnpIsNilIdent(bin.Y) {
		return n
	}
	if n := rnpDottedName(bin.Y); n != "" && rnpIsNilIdent(bin.X) {
		return n
	}
	return ""
}

func rnpDottedName(e ast.Expr) string {

	switch v := e.(type) {
	case *ast.Ident:
		return v.Name
	case *ast.SelectorExpr:
		base := rnpDottedName(v.X)
		if base == "" {
			return ""
		}
		return base + "." + v.Sel.Name
	}
	return ""
}

func rnpIsNilIdent(e ast.Expr) bool {
	id, ok := e.(*ast.Ident)
	return ok && id.Name == "nil"
}

func rnpCheckStmt(rs *ast.RangeStmt, fset *token.FileSet, guards map[string][]rnpNilGuard) (string, string) {
	star, ok := rs.X.(*ast.StarExpr)
	if !ok {
		return "", ""
	}

	varName := rnpExprName(star.X)
	pos := fset.Position(star.Pos())

	if varName != "" && rnpHasNilGuard(varName, rs.Pos(), guards) {
		return "", ""
	}

	if varName == "" {
		return fmt.Sprintf("%s: range over pointer dereference of a non-variable expression -- "+
			"if the pointer is nil this will panic. Store in a variable and check for nil first.",
			rnpFormatPos(pos)), ""
	}

	return fmt.Sprintf("%s: range over *%s without nil check -- if %s is nil this will "+
		"panic with nil pointer dereference. Add `if %s == nil { return }` before the loop.",
		rnpFormatPos(pos), varName, varName, varName), varName
}

func rnpHasNilGuard(varName string, rangePos token.Pos, guards map[string][]rnpNilGuard) bool {
	for _, g := range guards[varName] {
		if g.ifPos < rangePos && rangePos < g.thenEnd {

			return g.negated
		}
		if g.hasElse && g.thenEnd <= rangePos && rangePos < g.ifEnd {

			return !g.negated
		}
		if g.ifPos < rangePos && rangePos < g.ifEnd {

			continue
		}
		if !g.negated && g.terminates && g.ifEnd < rangePos {

			return true
		}
	}
	return false
}

func rnpExprName(expr ast.Expr) string {
	return rnpDottedName(expr)
}

func rnpFormatPos(pos token.Position) string {
	return fmt.Sprintf("%s:%d", pos.Filename, pos.Line)
}
