package agent

import (
	"go/ast"
	"os"
	"path/filepath"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
)

type exportedFuncInfo struct {
	DisplayName string
	IsMethod    bool
}

func parseExportedFuncs(filePath string) []exportedFuncInfo {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil
	}
	file, _, err := parseGoSource(filePath, string(data), 0)
	if err != nil {
		return nil
	}

	var funcs []exportedFuncInfo
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name == nil {
			continue
		}
		name := fn.Name.Name

		if !fn.Name.IsExported() {
			continue
		}
		if name == "Test" || strings.HasPrefix(name, "Test") {

			continue
		}

		var display string
		isMethod := false
		if fn.Recv != nil && len(fn.Recv.List) > 0 {
			isMethod = true
			recvType := receiverTypeName(fn.Recv.List[0].Type)
			if recvType != "" {
				display = recvType + "_" + name
			} else {
				display = name
			}
		} else {
			display = name
		}
		funcs = append(funcs, exportedFuncInfo{
			DisplayName: display,
			IsMethod:    isMethod,
		})
	}
	return funcs
}

func receiverTypeName(expr ast.Expr) string {
	if star, ok := expr.(*ast.StarExpr); ok {
		expr = star.X
	}
	if ident, ok := expr.(*ast.Ident); ok {
		return ident.Name
	}
	return ""
}

func parseTestFuncNames(filePath string) (map[string]bool, bool) {
	data, err := os.ReadFile(filePath)
	if err != nil {

		if os.IsNotExist(err) {
			return nil, true
		}
		return nil, false
	}
	file, _, err := parseGoSource(filePath, string(data), 0)
	if err != nil {
		return nil, false
	}
	names := make(map[string]bool)
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name == nil {
			continue
		}
		if strings.HasPrefix(fn.Name.Name, "Test") {
			names[fn.Name.Name] = true
		}
	}
	return names, true
}

func untestedExportedFuncs(workingDir, goFile string) []string {
	abs := goFile
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(workingDir, goFile)
	}
	funcs := parseExportedFuncs(abs)
	if len(funcs) == 0 {
		return nil
	}

	anyParseFailed := false

	base := filepath.Base(goFile)
	srcName := strings.TrimSuffix(base, ".go")
	testFile := filepath.Join(filepath.Dir(abs), srcName+"_test.go")
	testFuncs, siblingOK := parseTestFuncNames(testFile)
	if !siblingOK {
		anyParseFailed = true
	}
	noSibling := len(testFuncs) == 0
	if noSibling {

		testFiles, _ := filepath.Glob(filepath.Join(filepath.Dir(abs), "*_test.go"))
		if len(testFiles) == 0 {
			result := make([]string, 0, len(funcs))
			for _, f := range funcs {
				result = append(result, f.DisplayName)
			}
			return result
		}

		testFuncs = map[string]bool{}
		for _, tf := range testFiles {
			names, ok := parseTestFuncNames(tf)
			if !ok {
				anyParseFailed = true
				debug.Log("test-impact", "sibling test parse failed (partial): %s", tf)
				continue
			}
			for name := range names {
				testFuncs[name] = true
			}
		}
	}

	var untested []string
	for _, f := range funcs {

		primary := "Test" + f.DisplayName
		if testFuncs[primary] {
			continue
		}

		if f.IsMethod {
			parts := strings.SplitN(f.DisplayName, "_", 2)
			if len(parts) == 2 {
				altName := "Test" + parts[0] + parts[1]
				if testFuncs[altName] {
					continue
				}
			}
		}

		if dirTestFilesReferenceSymbol(filepath.Dir(abs), f.DisplayName) {
			continue
		}

		if f.IsMethod {
			if idx := strings.LastIndex(f.DisplayName, "_"); idx >= 0 {
				if bare := f.DisplayName[idx+1:]; len(bare) >= 3 {
					if dirTestFilesReferenceSymbol(filepath.Dir(abs), bare) {
						continue
					}
				}
			}
		}

		if anyParseFailed {
			continue
		}
		untested = append(untested, f.DisplayName)
	}
	return untested
}

func dirTestFilesReferenceSymbol(dir, symbol string) bool {
	if symbol == "" {
		return false
	}
	testFiles, _ := filepath.Glob(filepath.Join(dir, "*_test.go"))
	for _, tf := range testFiles {
		data, err := os.ReadFile(tf)
		if err != nil {
			continue
		}
		if identBoundaryReferenced(string(data), symbol) {
			return true
		}
	}
	return false
}

type funcCoverageGap struct {
	File  string
	Funcs []string
}

func funcLevelCoverageGaps(workingDir string, maxFiles, maxFuncsPerFile int) []funcCoverageGap {
	files := changedGoFilesFromGit(workingDir)
	if len(files) == 0 {
		return nil
	}

	var gaps []funcCoverageGap
	for _, f := range files {
		untested := untestedExportedFuncs(workingDir, f)
		if len(untested) == 0 {
			continue
		}
		if len(untested) > maxFuncsPerFile {
			untested = untested[:maxFuncsPerFile]
		}
		gaps = append(gaps, funcCoverageGap{
			File:  filepath.Base(f),
			Funcs: untested,
		})
		if len(gaps) >= maxFiles {
			break
		}
	}
	return gaps
}

func identBoundaryReferenced(src, symbol string) bool {
	for i := 0; i+len(symbol) <= len(src); i++ {
		if src[i:i+len(symbol)] != symbol {
			continue
		}
		beforeOK := i == 0 || !isIdentByte(src[i-1])
		afterOK := i+len(symbol) >= len(src) || !isIdentByte(src[i+len(symbol)])
		if beforeOK && afterOK {
			return true
		}
	}
	return false
}

func isIdentByte(b byte) bool {
	return b == '_' || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9')
}
