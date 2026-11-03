package agent

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
)

const (
	maxExportedDocWarnings = 3
)

type docMissingInfo struct {
	kind string
	name string
}

func checkMissingExportedDocsAST(filePath, oldContent string, newAST *ast.File) []string {
	if newAST == nil {
		return nil
	}

	if isTestFile(filePath) {
		return nil
	}

	if newAST.Name != nil && newAST.Name.Name == "main" {
		return nil
	}

	newMissing := collectExportedWithoutDocs(newAST)
	if len(newMissing) == 0 {
		return nil
	}

	oldMissing := collectExportedWithoutDocsSrc(oldContent)

	var warnings []string
	seen := make(map[string]bool, len(newMissing))
	for key, info := range newMissing {
		if _, existed := oldMissing[key]; existed {
			continue
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		warnings = append(warnings, formatExportedDocWarning(info))
		if len(warnings) >= maxExportedDocWarnings {
			break
		}
	}

	if len(warnings) == 0 {
		return nil
	}
	return warnings
}

func collectExportedWithoutDocs(file *ast.File) map[string]docMissingInfo {
	result := make(map[string]docMissingInfo)
	for _, decl := range file.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:

			if isUnexportedReceiverMethod(d) {
				continue
			}
			if d.Recv == nil && d.Name.IsExported() && d.Doc == nil {
				result["func:"+d.Name.Name] = docMissingInfo{kind: "function", name: d.Name.Name}
			}
		case *ast.GenDecl:
			collectGenDeclMissingDocs(d, result)
		}
	}
	return result
}

func collectExportedWithoutDocsSrc(src string) map[string]docMissingInfo {
	if strings.TrimSpace(src) == "" {
		return make(map[string]docMissingInfo)
	}
	file, _, err := parseGoSource("", src, parser.ParseComments)
	if err != nil {
		return make(map[string]docMissingInfo)
	}
	return collectExportedWithoutDocs(file)
}

func isUnexportedReceiverMethod(d *ast.FuncDecl) bool {
	if d.Recv == nil || len(d.Recv.List) == 0 {
		return false
	}
	star, ok := d.Recv.List[0].Type.(*ast.StarExpr)
	if ok {
		if id, ok := star.X.(*ast.Ident); ok {
			return !id.IsExported()
		}
	}
	if id, ok := d.Recv.List[0].Type.(*ast.Ident); ok {
		return !id.IsExported()
	}
	return false
}

func collectGenDeclMissingDocs(d *ast.GenDecl, result map[string]docMissingInfo) {
	multiSpec := len(d.Specs) > 1
	for _, spec := range d.Specs {
		switch s := spec.(type) {
		case *ast.TypeSpec:
			if !s.Name.IsExported() {
				continue
			}
			if genDeclHasDoc(d, s, multiSpec) {
				continue
			}
			result["type:"+s.Name.Name] = docMissingInfo{kind: "type", name: s.Name.Name}
		case *ast.ValueSpec:
			for _, name := range s.Names {
				if !name.IsExported() {
					continue
				}
				if genDeclHasDoc(d, s, multiSpec) {
					continue
				}
				kind := "variable"
				if d.Tok == token.CONST {
					kind = "constant"
				}
				result["var:"+name.Name] = docMissingInfo{kind: kind, name: name.Name}
			}
		}
	}
}

func genDeclHasDoc(decl *ast.GenDecl, specDoc ast.Node, multiSpec bool) bool {
	if multiSpec {
		return specDocHasComment(specDoc)
	}
	return decl.Doc != nil || specDocHasComment(specDoc)
}

func specDocHasComment(spec ast.Node) bool {
	switch s := spec.(type) {
	case *ast.TypeSpec:
		return s.Doc != nil
	case *ast.ValueSpec:
		return s.Doc != nil
	}
	return false
}

func formatExportedDocWarning(info docMissingInfo) string {
	return fmt.Sprintf(
		"Exported %s %q was added without a godoc comment. "+
			"Go convention requires exported identifiers to be documented "+
			"(golint/revive). Add a comment starting with %q above the declaration.",
		info.kind, info.name, info.name)
}
