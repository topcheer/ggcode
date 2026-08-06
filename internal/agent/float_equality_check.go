package agent

// Float Equality Comparison Detection (Check #76)
//
// Problem: Comparing floating-point values with == or != is a classic
// source of subtle bugs. Due to IEEE 754 representation, computations
// like 0.1 + 0.2 != 0.3. AI coding agents frequently produce code that
// uses exact equality for floats, leading to intermittent failures.
//
// The correct approach is to compare against an epsilon tolerance:
//
//	math.Abs(a-b) < epsilon
//
// Competitor analysis:
//   - Claude Code: no write-time detection
//   - Cursor: no detection (staticcheck SA9000+ partially covers, post-save)
//   - OpenHands/Cline: no detection
//   - Aider: no detection
//   - golangci-lint: does not flag float == by default
//
// Approach: AST-based analysis. For each Go file:
//  1. Find binary expressions using == or != operators.
//  2. Check if either operand is a floating-point value (float32/float64
//     typed, float literal with decimal point, or float-typed identifier).
//  3. Warn about potential precision issues with exact float comparison.
//
// Zero LLM cost -- pure AST pattern matching with Go's standard library.

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
)

const maxFloatEqWarnings = 5

// feqFloatTypes identifies built-in floating-point type names.
var feqFloatTypes = map[string]bool{
	"float32": true,
	"float64": true,
}

// checkFloatEquality detects float32/float64 comparisons using == or !=.
func checkFloatEquality(filePath, _, newContent string) []string {
	if filepath.Ext(filePath) != ".go" {
		return nil
	}
	src := strings.TrimSpace(newContent)
	if src == "" {
		return nil
	}

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filePath, newContent, 0)
	if err != nil || file == nil {
		return nil
	}

	// Collect float-typed identifiers from declarations.
	floatVars := feqCollectFloatVars(file)

	var warnings []string
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			bin, ok := n.(*ast.BinaryExpr)
			if !ok {
				return true
			}
			if bin.Op != token.EQL && bin.Op != token.NEQ {
				return true
			}
			if feqIsFloatOperand(bin.X, floatVars) || feqIsFloatOperand(bin.Y, floatVars) {
				if len(warnings) < maxFloatEqWarnings {
					pos := fset.Position(bin.Pos())
					warnings = append(warnings, fmt.Sprintf(
						"%s:%d: float comparison with %s -- floating-point equality is unreliable due to IEEE 754 precision; use math.Abs(a-b) < epsilon instead",
						filepath.Base(pos.Filename), pos.Line, bin.Op,
					))
				}
			}
			return true
		})
	}

	if len(warnings) >= maxFloatEqWarnings {
		warnings = append(warnings, fmt.Sprintf("... (%d float comparison warnings truncated)", maxFloatEqWarnings))
	}
	return warnings
}

// feqCollectFloatVars finds all top-level and local variables with float types.
func feqCollectFloatVars(file *ast.File) map[string]bool {
	result := make(map[string]bool)
	for _, decl := range file.Decls {
		feqCollectFromDecl(decl, result)
	}
	return result
}

// feqCollectFromDecl extracts float-typed variable names from a declaration.
func feqCollectFromDecl(decl ast.Decl, result map[string]bool) {
	switch d := decl.(type) {
	case *ast.GenDecl:
		if d.Tok == token.VAR {
			feqCollectFloatSpecs(d.Specs, result)
		}
	case *ast.FuncDecl:
		if d.Body != nil {
			feqCollectLocalFloats(d.Body, result)
		}
	}
}

// feqCollectFloatSpecs adds float-typed names from value specs.
func feqCollectFloatSpecs(specs []ast.Spec, result map[string]bool) {
	for _, spec := range specs {
		vs, ok := spec.(*ast.ValueSpec)
		if !ok || !feqIsFloatTypeIdent(vs.Type) {
			continue
		}
		for _, name := range vs.Names {
			result[name.Name] = true
		}
	}
}

// feqCollectLocalFloats finds local float-typed vars inside a function body.
func feqCollectLocalFloats(body *ast.BlockStmt, result map[string]bool) {
	ast.Inspect(body, func(n ast.Node) bool {
		ds, ok := n.(*ast.DeclStmt)
		if !ok {
			return true
		}
		gd, ok := ds.Decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.VAR {
			return true
		}
		feqCollectFloatSpecs(gd.Specs, result)
		return true
	})
}

// feqIsFloatTypeIdent checks if an ast.Expr is a float32 or float64 type identifier.
func feqIsFloatTypeIdent(expr ast.Expr) bool {
	if ident, ok := expr.(*ast.Ident); ok {
		return feqFloatTypes[ident.Name]
	}
	// Handle qualified types like pkg.Float64Type - skip for simplicity.
	return false
}

// feqIsFloatOperand determines if an expression evaluates to a float.
func feqIsFloatOperand(expr ast.Expr, floatVars map[string]bool) bool {
	switch e := expr.(type) {
	case *ast.BasicLit:
		// Float literal: contains a decimal point or exponent.
		if e.Kind == token.FLOAT {
			return true
		}
	case *ast.Ident:
		// Named variable known to be float-typed.
		if floatVars[e.Name] {
			return true
		}
	case *ast.CallExpr:
		// Calls to math functions return float64.
		if feqIsMathFunc(e) {
			return true
		}
	case *ast.BinaryExpr:
		// Arithmetic on floats produces floats.
		return feqIsFloatOperand(e.X, floatVars) || feqIsFloatOperand(e.Y, floatVars)
	case *ast.ParenExpr:
		return feqIsFloatOperand(e.X, floatVars)
	}
	return false
}

// feqIsMathFunc checks if a call is to a math package function returning float64.
func feqIsMathFunc(call *ast.CallExpr) bool {
	fn, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkg, ok := fn.X.(*ast.Ident)
	if !ok {
		return false
	}
	// Common math package functions that return float64.
	if pkg.Name == "math" {
		return true
	}
	return false
}
