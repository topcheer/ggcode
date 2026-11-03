package agent

import (
	"fmt"
	"go/ast"
	"go/token"
	"path/filepath"
	"regexp"
	"strings"
)

const maxDuplicateWarnings = 3

func checkDuplicateDeclarations(filePath, oldContent, newContent string) string {
	if strings.TrimSpace(newContent) == "" {
		return ""
	}

	if isTestFile(filePath) {
		return ""
	}

	if strings.HasSuffix(filePath, ".d.ts") {
		return ""
	}

	ext := filepath.Ext(filePath)

	var dups []dupDecl

	switch ext {
	case ".go":
		dups = checkGoDuplicateDecls(filePath, oldContent, newContent)
	case ".py":
		dups = checkPythonDuplicateDecls(oldContent, newContent)
	case ".js", ".jsx", ".ts", ".tsx":
		dups = checkJSDuplicateDecls(oldContent, newContent, ext)
	default:
		return ""
	}

	if len(dups) == 0 {
		return ""
	}

	if len(dups) > maxDuplicateWarnings {
		dups = dups[:maxDuplicateWarnings]
	}

	var b strings.Builder
	for i, d := range dups {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(formatDuplicateWarning(d))
	}
	return b.String()
}

type dupDecl struct {
	kind  string
	name  string
	count int
}

func formatDuplicateWarning(d dupDecl) string {
	return fmt.Sprintf(
		"Duplicate %s %q detected (%d occurrences in file). "+
			"This will cause a compilation/runtime error — "+
			"remove the duplicate or merge the declarations.",
		d.kind, d.name, d.count)
}

func checkGoDuplicateDecls(filePath, oldContent, newContent string) []dupDecl {
	oldDecls := collectGoDecls(filePath, oldContent)
	newDecls := collectGoDecls(filePath, newContent)

	var dups []dupDecl
	seen := make(map[string]bool)

	for name, newCount := range newDecls {
		if newCount < 2 {
			continue
		}
		oldCount := oldDecls[name]

		if newCount <= oldCount {
			continue
		}

		if oldCount >= 2 {
			continue
		}
		key := name.kind + ":" + name.name
		if seen[key] {
			continue
		}
		seen[key] = true
		dups = append(dups, dupDecl{
			kind:  name.kind,
			name:  name.name,
			count: newCount,
		})
	}

	return dups
}

type goDeclKey struct {
	kind string
	name string
}

func collectGoDecls(filePath, src string) map[goDeclKey]int {
	counts := make(map[goDeclKey]int)
	if strings.TrimSpace(src) == "" {
		return counts
	}

	f, _, err := parseGoSource(filePath, src, 0)
	if err != nil {

		return counts
	}

	for _, decl := range f.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			if d.Name == nil || d.Name.Name == "" {
				continue
			}

			if d.Recv != nil && len(d.Recv.List) > 0 {
				recvType := receiverTypeName(d.Recv.List[0].Type)
				if recvType != "" {
					counts[goDeclKey{"method", recvType + "." + d.Name.Name}]++
					continue
				}
			}
			counts[goDeclKey{"function", d.Name.Name}]++
		case *ast.GenDecl:
			for _, spec := range d.Specs {
				switch s := spec.(type) {
				case *ast.TypeSpec:
					if s.Name != nil && s.Name.Name != "" {
						counts[goDeclKey{"type", s.Name.Name}]++
					}
				case *ast.ValueSpec:
					for _, name := range s.Names {
						if name.Name != "" && name.Name != "_" {
							kind := "var"
							if d.Tok == token.CONST {
								kind = "const"
							}
							counts[goDeclKey{kind, name.Name}]++
						}
					}
				}
			}
		}
	}

	return counts
}

var pythonFuncRe = regexp.MustCompile(`(?m)^(\s*)(async\s+)?def\s+(\w+)\s*\(`)

var pythonClassRe = regexp.MustCompile(`(?m)^class\s+(\w+)\s*[\(:]`)

func checkPythonDuplicateDecls(oldContent, newContent string) []dupDecl {
	return compareDeclCounts(
		collectPythonDecls(oldContent),
		collectPythonDecls(newContent),
	)
}

func collectPythonDecls(src string) map[regexDeclKey]int {
	counts := make(map[regexDeclKey]int)
	if strings.TrimSpace(src) == "" {
		return counts
	}
	currentClass := ""

	methodIndent := -1
	for _, raw := range strings.Split(src, "\n") {
		line := strings.TrimRight(raw, "\r")
		if cm := pythonClassRe.FindStringSubmatch(line); cm != nil {
			currentClass = cm[1]
			methodIndent = -1
			counts[regexDeclKey{"class", cm[1]}]++
			continue
		}
		fm := pythonFuncRe.FindStringSubmatch(line)
		if fm == nil {
			continue
		}
		name := fm[3]
		indent := len(fm[1])
		switch {
		case indent == 0:

			currentClass = ""
			methodIndent = -1
			counts[regexDeclKey{"function", name}]++
		case currentClass != "" && methodIndent == -1:

			methodIndent = indent
			counts[regexDeclKey{"method", currentClass + "." + name}]++
		case currentClass != "" && indent == methodIndent:

			counts[regexDeclKey{"method", currentClass + "." + name}]++
		case currentClass != "" && indent > methodIndent:

		default:

			counts[regexDeclKey{"function", name}]++
		}
	}
	return counts
}

var jsFuncRe = regexp.MustCompile(`(?m)^(?:export\s+)?(?:default\s+)?(?:async\s+)?function\s+(\w+)\s*\((?:[^;{}()]|\([^()]*\))*\)\s*(?::\s*[^;{=]+)?\s*\{`)

var jsClassRe = regexp.MustCompile(`(?m)^(?:export\s+)?(?:default\s+)?(?:abstract\s+)?class\s+(\w+)(?:\s+extends\s+[\w.<>[\]]+)?(?:\s+implements\s+[\w.<>[\],\s]+)?\s*[\{<]`)

var jsConstFuncRe = regexp.MustCompile(`(?m)^(?:export\s+)?const\s+(\w+)\s*=\s*(?:async\s*)?\(?`)

func checkJSDuplicateDecls(oldContent, newContent, ext string) []dupDecl {
	patterns := []regexDeclPattern{
		{re: jsFuncRe, kind: "function", group: 1},
		{re: jsClassRe, kind: "class", group: 1},
		{re: jsConstFuncRe, kind: "const", group: 1},
	}
	return checkRegexDuplicates(oldContent, newContent, patterns)
}

type regexDeclPattern struct {
	re    *regexp.Regexp
	kind  string
	group int
}

func checkRegexDuplicates(oldContent, newContent string, patterns []regexDeclPattern) []dupDecl {
	return compareDeclCounts(
		collectRegexDecls(oldContent, patterns),
		collectRegexDecls(newContent, patterns),
	)
}

func compareDeclCounts(oldCounts, newCounts map[regexDeclKey]int) []dupDecl {
	var dups []dupDecl
	seen := make(map[string]bool)

	for key, newCount := range newCounts {
		if newCount < 2 {
			continue
		}
		oldCount := oldCounts[key]
		if newCount <= oldCount {
			continue
		}
		if oldCount >= 2 {
			continue
		}
		mapKey := key.kind + ":" + key.name
		if seen[mapKey] {
			continue
		}
		seen[mapKey] = true
		dups = append(dups, dupDecl{
			kind:  key.kind,
			name:  key.name,
			count: newCount,
		})
	}

	return dups
}

type regexDeclKey struct {
	kind string
	name string
}

func collectRegexDecls(src string, patterns []regexDeclPattern) map[regexDeclKey]int {
	counts := make(map[regexDeclKey]int)
	if strings.TrimSpace(src) == "" {
		return counts
	}
	for _, p := range patterns {
		matches := p.re.FindAllStringSubmatch(src, -1)
		for _, m := range matches {
			if p.group < len(m) && m[p.group] != "" {
				counts[regexDeclKey{p.kind, m[p.group]}]++
			}
		}
	}
	return counts
}
