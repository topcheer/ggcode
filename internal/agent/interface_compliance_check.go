package agent

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
)

type interfaceMethodSet struct {
	Name    string
	Methods map[string]string
}

type complianceViolation struct {
	InterfaceName  string
	TypeName       string
	MissingMethods []string
}

func checkInterfaceCompliance(filePath, oldContent, newContent string) string {
	if !strings.HasSuffix(filePath, ".go") || strings.HasSuffix(filePath, "_test.go") {
		return ""
	}
	if strings.TrimSpace(newContent) == "" {
		return ""
	}

	oldInterfaces := extractInterfaceMethodSets(oldContent)
	newInterfaces := extractInterfaceMethodSets(newContent)

	if len(newInterfaces) == 0 {
		return ""
	}

	var changedInterfaces []interfaceMethodSet
	for name, newMS := range newInterfaces {
		oldMS, existed := oldInterfaces[name]
		if !existed {

			changedInterfaces = append(changedInterfaces, interfaceMethodSet{Name: name, Methods: newMS})
			continue
		}

		if !methodSetsEqual(oldMS, newMS) {
			changedInterfaces = append(changedInterfaces, interfaceMethodSet{Name: name, Methods: newMS})
		}
	}

	if len(changedInterfaces) == 0 {
		return ""
	}

	pkgDir := filepath.Dir(filePath)
	typeMethods := scanPackageTypeMethods(pkgDir, filePath, newContent)
	if len(typeMethods) == 0 {
		return ""
	}

	var violations []complianceViolation
	for _, iface := range changedInterfaces {
		for typeName, methods := range typeMethods {

			overlap := false
			for methodName := range iface.Methods {
				if _, ok := methods[methodName]; ok {
					overlap = true
					break
				}
			}
			if !overlap {
				continue
			}

			var missing []string
			for methodName := range iface.Methods {
				if _, ok := methods[methodName]; !ok {
					missing = append(missing, methodName)
				}
			}
			if len(missing) > 0 {
				sort.Strings(missing)
				violations = append(violations, complianceViolation{
					InterfaceName:  iface.Name,
					TypeName:       typeName,
					MissingMethods: missing,
				})
			}
		}
	}

	if len(violations) == 0 {
		return ""
	}

	debug.Log("interface-compliance", "detected %d compliance violation(s) in %s", len(violations), filepath.Base(filePath))
	return formatComplianceWarning(filePath, violations)
}

func extractInterfaceMethodSets(src string) map[string]map[string]string {
	if strings.TrimSpace(src) == "" {
		return nil
	}
	file, _, err := parseGoSource("", src, 0)
	if err != nil {
		return nil
	}

	result := make(map[string]map[string]string)

	for _, decl := range file.Decls {
		genDecl, ok := decl.(*ast.GenDecl)
		if !ok || genDecl.Tok != token.TYPE {
			continue
		}
		for _, spec := range genDecl.Specs {
			typeSpec, ok := spec.(*ast.TypeSpec)
			if !ok || typeSpec.Name == nil {
				continue
			}
			ifaceType, ok := typeSpec.Type.(*ast.InterfaceType)
			if !ok {
				continue
			}
			methods := make(map[string]string)
			if ifaceType.Methods != nil {
				for _, field := range ifaceType.Methods.List {
					if ft, ok := field.Type.(*ast.FuncType); ok && len(field.Names) > 0 {
						for _, name := range field.Names {
							methods[name.Name] = normalizeFuncSignature(ft)
						}
					}
				}
			}
			result[typeSpec.Name.Name] = methods
		}
	}

	return result
}

func methodSetsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if bv, ok := b[k]; !ok || bv != v {
			return false
		}
	}
	return true
}

func scanPackageTypeMethods(dir, excludeFile, newContent string) map[string]map[string]bool {

	typeDecls := make(map[string][]string)
	var filesToProcess []*ast.File

	if strings.TrimSpace(newContent) != "" {
		fset := token.NewFileSet()
		newFile, err := parser.ParseFile(fset, "", newContent, 0)
		if err == nil {
			filesToProcess = append(filesToProcess, newFile)

			for _, decl := range newFile.Decls {
				genDecl, ok := decl.(*ast.GenDecl)
				if !ok || genDecl.Tok != token.TYPE {
					continue
				}
				for _, spec := range genDecl.Specs {
					typeSpec, ok := spec.(*ast.TypeSpec)
					if !ok || typeSpec.Name == nil {
						continue
					}
					structType, ok := typeSpec.Type.(*ast.StructType)
					if !ok {
						continue
					}
					var embedded []string
					if structType.Fields != nil {
						for _, field := range structType.Fields.List {

							if len(field.Names) == 0 {
								embeddedName := receiverTypeName(field.Type)
								if embeddedName != "" {
									embedded = append(embedded, embeddedName)
								}
							}
						}
					}
					typeDecls[typeSpec.Name.Name] = embedded
				}
			}
		}
	}

	entries, err := os.ReadDir(dir)
	if err == nil {
		excludeBase := filepath.Base(excludeFile)
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			name := entry.Name()
			if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
				continue
			}
			if name == excludeBase {
				continue
			}

			path := filepath.Join(dir, name)
			fset := token.NewFileSet()
			file, err := parser.ParseFile(fset, path, nil, 0)
			if err != nil {
				continue
			}
			filesToProcess = append(filesToProcess, file)

			for _, decl := range file.Decls {
				genDecl, ok := decl.(*ast.GenDecl)
				if !ok || genDecl.Tok != token.TYPE {
					continue
				}
				for _, spec := range genDecl.Specs {
					typeSpec, ok := spec.(*ast.TypeSpec)
					if !ok || typeSpec.Name == nil {
						continue
					}
					structType, ok := typeSpec.Type.(*ast.StructType)
					if !ok {
						continue
					}
					var embedded []string
					if structType.Fields != nil {
						for _, field := range structType.Fields.List {

							if len(field.Names) == 0 {
								embeddedName := receiverTypeName(field.Type)
								if embeddedName != "" {
									embedded = append(embedded, embeddedName)
								}
							}
						}
					}
					typeDecls[typeSpec.Name.Name] = embedded
				}
			}
		}
	}

	methods := make(map[string]map[string]bool)
	for _, file := range filesToProcess {
		for _, decl := range file.Decls {
			funcDecl, ok := decl.(*ast.FuncDecl)
			if !ok || funcDecl.Name == nil || funcDecl.Recv == nil || len(funcDecl.Recv.List) == 0 {
				continue
			}
			recvType := receiverTypeName(funcDecl.Recv.List[0].Type)
			if recvType == "" {
				continue
			}
			if _, ok := methods[recvType]; !ok {
				methods[recvType] = make(map[string]bool)
			}
			methods[recvType][funcDecl.Name.Name] = true
		}
	}

	result := make(map[string]map[string]bool)
	for typeName, ownMethods := range methods {

		completeMethods := make(map[string]bool)
		for m := range ownMethods {
			completeMethods[m] = true
		}

		promoteEmbeddedMethods(typeName, typeDecls, methods, completeMethods, make(map[string]bool))
		result[typeName] = completeMethods
	}

	return result
}

func promoteEmbeddedMethods(
	typeName string,
	typeDecls map[string][]string,
	methods map[string]map[string]bool,
	completeMethods map[string]bool,
	visited map[string]bool,
) {
	if visited[typeName] {
		return
	}
	visited[typeName] = true

	embedded := typeDecls[typeName]
	for _, embName := range embedded {

		if embMethods, ok := methods[embName]; ok {
			for m := range embMethods {
				completeMethods[m] = true
			}
		}

		promoteEmbeddedMethods(embName, typeDecls, methods, completeMethods, visited)
	}
}

func formatComplianceWarning(filePath string, violations []complianceViolation) string {
	sort.Slice(violations, func(i, j int) bool {
		if violations[i].InterfaceName != violations[j].InterfaceName {
			return violations[i].InterfaceName < violations[j].InterfaceName
		}
		return violations[i].TypeName < violations[j].TypeName
	})

	var b strings.Builder
	b.WriteString(fmt.Sprintf("[Interface Compliance Warning] Edits to %s changed interface method sets.\n",
		filepath.Base(filePath)))
	b.WriteString(fmt.Sprintf("%d type(s) may no longer satisfy their interface contracts:\n\n", len(violations)))

	maxShow := 5
	for i, v := range violations {
		if i >= maxShow {
			b.WriteString(fmt.Sprintf("  ... and %d more\n", len(violations)-maxShow))
			break
		}
		b.WriteString(fmt.Sprintf("  Type %s is missing method(s) required by interface %s: %s\n",
			v.TypeName, v.InterfaceName, strings.Join(v.MissingMethods, ", ")))
	}

	b.WriteString("\n")
	b.WriteString("Add the missing methods to each type, or remove the interface requirement.\n")
	b.WriteString("Use grep or lsp_references to find all implementations of the changed interface.")
	return b.String()
}
