package agent

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
)

type deprecatedAPIInstance struct {
	posStr      string
	identifier  string
	replacement string
	severity    string
}

type deprecatedAPIRule struct {
	kind        string
	pkg         string
	name        string
	replacement string
	explanation string
}

var deprecatedRules = []deprecatedAPIRule{

	{kind: "import", pkg: "io/ioutil", replacement: "io and os packages",
		explanation: "The entire io/ioutil package was deprecated in Go 1.16. Use io.ReadAll, io.Copy, os.ReadFile, os.WriteFile, os.MkdirTemp, os.CreateTemp instead."},

	{kind: "selector", pkg: "ioutil", name: "ReadFile", replacement: "os.ReadFile",
		explanation: "ioutil.ReadFile was deprecated in Go 1.16. Use os.ReadFile instead."},
	{kind: "selector", pkg: "ioutil", name: "WriteFile", replacement: "os.WriteFile",
		explanation: "ioutil.WriteFile was deprecated in Go 1.16. Use os.WriteFile instead."},
	{kind: "selector", pkg: "ioutil", name: "ReadAll", replacement: "io.ReadAll",
		explanation: "ioutil.ReadAll was deprecated in Go 1.16. Use io.ReadAll instead."},
	{kind: "selector", pkg: "ioutil", name: "ReadDir", replacement: "os.ReadDir",
		explanation: "ioutil.ReadDir was deprecated in Go 1.16. Use os.ReadDir instead (returns []os.DirEntry, not []os.FileInfo)."},
	{kind: "selector", pkg: "ioutil", name: "TempFile", replacement: "os.CreateTemp",
		explanation: "ioutil.TempFile was deprecated in Go 1.16. Use os.CreateTemp instead."},
	{kind: "selector", pkg: "ioutil", name: "TempDir", replacement: "os.MkdirTemp",
		explanation: "ioutil.TempDir was deprecated in Go 1.16. Use os.MkdirTemp instead."},
	{kind: "selector", pkg: "ioutil", name: "NopCloser", replacement: "io.NopCloser",
		explanation: "ioutil.NopCloser was deprecated in Go 1.16. Use io.NopCloser instead."},
	{kind: "selector", pkg: "ioutil", name: "Discard", replacement: "io.Discard",
		explanation: "ioutil.Discard was deprecated in Go 1.16. Use io.Discard instead."},

	{kind: "selector", pkg: "rand", name: "Seed", replacement: "remove the call",
		explanation: "rand.Seed was deprecated in Go 1.20. The global generator is now automatically seeded. Simply remove the rand.Seed() call."},

	{kind: "selector", pkg: "strings", name: "Title", replacement: "golang.org/x/text/cases",
		explanation: "strings.Title was deprecated in Go 1.18. It does not handle Unicode word boundaries properly. Use golang.org/x/text/cases.Title instead."},

	{kind: "selector", pkg: "bytes", name: "Title", replacement: "golang.org/x/text/cases",
		explanation: "bytes.Title was deprecated in Go 1.18. It does not handle Unicode word boundaries properly."},

	{kind: "selector", pkg: "os", name: "SEEK_SET", replacement: "io.SeekStart",
		explanation: "os.SEEK_SET was deprecated. Use io.SeekStart instead."},
	{kind: "selector", pkg: "os", name: "SEEK_CUR", replacement: "io.SeekCurrent",
		explanation: "os.SEEK_CUR was deprecated. Use io.SeekCurrent instead."},
	{kind: "selector", pkg: "os", name: "SEEK_END", replacement: "io.SeekEnd",
		explanation: "os.SEEK_END was deprecated. Use io.SeekEnd instead."},
}

func checkDeprecatedAPI(filePath, oldContent, newContent string) string {
	if filepath.Ext(filePath) != ".go" || strings.TrimSpace(newContent) == "" {
		return ""
	}

	file, fset, err := parseGoSource(filePath, newContent, parser.AllErrors)
	if err != nil {
		return ""
	}

	importAliases := collectImportAliases(file)

	instances := findDeprecatedImports(fset, file)
	instances = append(instances, findDeprecatedSelectors(fset, file, importAliases, instances)...)

	if len(instances) == 0 {
		return ""
	}

	newInstances := filterNewInstances(filePath, instances, oldContent)
	if len(newInstances) == 0 {
		return ""
	}

	return formatDeprecatedWarnings(newInstances)
}

func collectImportAliases(file *ast.File) map[string]string {
	aliases := make(map[string]string)
	for _, imp := range file.Imports {
		impPath := strings.Trim(imp.Path.Value, `"`)
		if imp.Name != nil {
			aliases[imp.Name.Name] = impPath
		} else {
			parts := strings.Split(impPath, "/")
			aliases[parts[len(parts)-1]] = impPath
		}
	}
	return aliases
}

func findDeprecatedImports(fset *token.FileSet, file *ast.File) []deprecatedAPIInstance {
	var instances []deprecatedAPIInstance
	for _, imp := range file.Imports {
		impPath := strings.Trim(imp.Path.Value, `"`)
		for _, rule := range deprecatedRules {
			if rule.kind == "import" && impPath == rule.pkg {
				pos := fset.Position(imp.Pos())
				instances = append(instances, deprecatedAPIInstance{
					posStr:      fmt.Sprintf("%s:%d", filepath.Base(pos.Filename), pos.Line),
					identifier:  impPath,
					replacement: rule.replacement,
				})
			}
		}
	}
	return instances
}

func findDeprecatedSelectors(fset *token.FileSet, file *ast.File, importAliases map[string]string, alreadyFound []deprecatedAPIInstance) []deprecatedAPIInstance {

	flaggedSet := make(map[string]bool, len(alreadyFound))
	for _, inst := range alreadyFound {
		flaggedSet[inst.identifier] = true
		if strings.Contains(inst.identifier, "io/ioutil") {
			flaggedSet["io/ioutil"] = true
		}
	}

	var instances []deprecatedAPIInstance
	ast.Inspect(file, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		ident, ok := sel.X.(*ast.Ident)
		if !ok {
			return true
		}

		impPath := importAliases[ident.Name]

		canonicalPkg := ident.Name
		if impPath != "" {
			if segs := strings.Split(impPath, "/"); len(segs) > 0 {
				canonicalPkg = segs[len(segs)-1]
			}
		}
		fullName := canonicalPkg + "." + sel.Sel.Name

		for _, rule := range deprecatedRules {
			if rule.kind != "selector" || canonicalPkg != rule.pkg || sel.Sel.Name != rule.name {
				continue
			}

			if flaggedSet[fullName] {
				continue
			}
			if impPath == "io/ioutil" && flaggedSet["io/ioutil"] {
				continue
			}
			pos := fset.Position(sel.Pos())
			instances = append(instances, deprecatedAPIInstance{
				posStr:      fmt.Sprintf("%s:%d", filepath.Base(pos.Filename), pos.Line),
				identifier:  fullName,
				replacement: rule.replacement,
			})
		}
		return true
	})
	return instances
}

func filterNewInstances(filePath string, instances []deprecatedAPIInstance, oldContent string) []deprecatedAPIInstance {
	remaining := countDeprecatedInstances(filePath, oldContent)
	var result []deprecatedAPIInstance
	for _, inst := range instances {
		if remaining[inst.identifier] > 0 {
			remaining[inst.identifier]--
			continue
		}
		result = append(result, inst)
	}
	return result
}

func countDeprecatedInstances(filePath, content string) map[string]int {
	counts := make(map[string]int)
	if strings.TrimSpace(content) == "" {
		return counts
	}
	file, fset, err := parseGoSource(filePath, content, parser.AllErrors)
	if err != nil {
		return counts
	}
	aliases := collectImportAliases(file)
	instances := findDeprecatedImports(fset, file)
	instances = append(instances, findDeprecatedSelectors(fset, file, aliases, instances)...)
	for _, inst := range instances {
		counts[inst.identifier]++
	}
	return counts
}

func formatDeprecatedWarnings(instances []deprecatedAPIInstance) string {
	var b strings.Builder
	b.WriteString("[Deprecated API detection] The following use deprecated Go standard library APIs:\n")
	for _, inst := range instances {
		reason := findDeprecatedExplanation(inst.identifier)
		b.WriteString(fmt.Sprintf("  - %s: '%s' is deprecated. %s Use '%s' instead.\n",
			inst.posStr, inst.identifier, reason, inst.replacement))
	}
	return b.String()
}

func findDeprecatedExplanation(identifier string) string {
	for _, rule := range deprecatedRules {
		if rule.kind == "import" && identifier == rule.pkg {
			return rule.explanation
		}
		if rule.kind == "selector" && identifier == rule.pkg+"."+rule.name {
			return rule.explanation
		}
	}
	return "This API is deprecated and may be removed in future Go versions."
}
