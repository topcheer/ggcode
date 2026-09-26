package agent

// interface_compliance_check.go implements Post-Edit Interface Implementation
// Completeness Detection - a deterministic post-edit guard that warns when an
// edit to a Go interface changes its method set in a way that breaks existing
// implementations in the same package.
//
// This catches a common LLM failure mode: the agent adds a new method to an
// interface (or renames/removes one) without updating all concrete types that
// implement it. The compiler only catches this when the type is explicitly
// assigned to the interface (var _ I = T{}), so silently-broken implementations
// can persist until runtime.
//
// Competitor mapping:
//   - Claude Code: relies on post-build compiler errors (too late)
//   - Cursor: real-time LSP diagnostics can catch this, but only for the
//     current file being edited - not across files in the package
//   - GitHub Copilot: no interface-level compliance analysis
//   - OpenHands: no interface compliance detection
//
// Our approach:
//  1. After a successful edit to a .go file, parse the new content for
//     interface declarations and their method sets.
//  2. Compare against the old content (delta-aware): only check interfaces
//     whose method sets changed.
//  3. For each changed interface, scan all other .go files in the same
//     package directory for concrete types that have methods matching the
//     interface name (duck-typing heuristic).
//  4. Report types that are missing methods required by the updated interface.
//
// Safety:
//   - Delta-aware: only fires when interface method sets actually change
//   - Single-directory scan (same package) - fast, no recursive traversal
//   - Non-blocking: warning injected into tool result, edit is not reverted
//   - At most once per file per run

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

// interfaceMethodSet represents the method signatures required by an interface.
type interfaceMethodSet struct {
	Name    string            // interface name (e.g., "Reader")
	Methods map[string]string // method name → normalized signature
}

// complianceViolation represents a type missing one or more interface methods.
type complianceViolation struct {
	InterfaceName  string
	TypeName       string
	MissingMethods []string
}

// checkInterfaceCompliance detects when an edit changes an interface's method
// set in a way that may break existing implementations. Returns a guidance
// string if violations are found, "" otherwise.
//
// This is integrated as a delta-aware check within the post-write integrity
// pipeline (not a separate guard) so it runs synchronously after every edit
// to a Go file.
func checkInterfaceCompliance(filePath, oldContent, newContent string) string {
	if !strings.HasSuffix(filePath, ".go") || strings.HasSuffix(filePath, "_test.go") {
		return ""
	}
	if strings.TrimSpace(newContent) == "" {
		return ""
	}

	// Parse both old and new content to extract interface method sets.
	oldInterfaces := extractInterfaceMethodSets(oldContent)
	newInterfaces := extractInterfaceMethodSets(newContent)

	if len(newInterfaces) == 0 {
		return ""
	}

	// Delta: find interfaces whose method sets changed.
	var changedInterfaces []interfaceMethodSet
	for name, newMS := range newInterfaces {
		oldMS, existed := oldInterfaces[name]
		if !existed {
			// New interface - check if any existing types partially implement it.
			changedInterfaces = append(changedInterfaces, interfaceMethodSet{Name: name, Methods: newMS})
			continue
		}
		// Compare method sets.
		if !methodSetsEqual(oldMS, newMS) {
			changedInterfaces = append(changedInterfaces, interfaceMethodSet{Name: name, Methods: newMS})
		}
	}

	if len(changedInterfaces) == 0 {
		return ""
	}

	// Scan the package directory for types that may implement these interfaces.
	// Bug C fix: Also parse type declarations from the new content to include
	// types defined in the edited file (for proper embedded promotion detection).
	pkgDir := filepath.Dir(filePath)
	typeMethods := scanPackageTypeMethods(pkgDir, filePath, newContent)
	if len(typeMethods) == 0 {
		return ""
	}

	var violations []complianceViolation
	for _, iface := range changedInterfaces {
		for typeName, methods := range typeMethods {
			// Check if this type has at least one method matching the interface
			// (duck-typing heuristic: only check types that partially implement).
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

			// Find missing methods.
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

// extractInterfaceMethodSets parses Go source and returns a map of interface
// name → required method signatures.
func extractInterfaceMethodSets(src string) map[string]map[string]string {
	if strings.TrimSpace(src) == "" {
		return nil
	}
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "", src, 0)
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

// methodSetsEqual compares two interface method signature maps for equality.
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

// scanPackageTypeMethods reads all .go files in the same directory (excluding
// the edited file and test files) and builds a map of type name → set of
// method names. This enables duck-typing checks without full type-checking.
// It handles method promotion from embedded structs (Bug C fix).
// The newContent parameter allows parsing types from the edited file for
// proper embedded promotion detection.
// structEmbeddedFields returns the names of a struct type's embedded fields
// (fields declared without a name), e.g. "Base" for `struct { Base; N int }`.
func structEmbeddedFields(structType *ast.StructType) []string {
	if structType.Fields == nil {
		return nil
	}
	var embedded []string
	for _, field := range structType.Fields.List {
		// Embedded field: has no field names
		if len(field.Names) == 0 {
			embeddedName := receiverTypeName(field.Type)
			if embeddedName != "" {
				embedded = append(embedded, embeddedName)
			}
		}
	}
	return embedded
}

// collectStructTypeDecls walks a file's type declarations and records each
// struct type's embedded field names into typeDecls. Later files overwrite
// earlier entries for the same type name, matching the original
// single-file-at-a-time assignment semantics.
func collectStructTypeDecls(file *ast.File, typeDecls map[string][]string) {
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
			typeDecls[typeSpec.Name.Name] = structEmbeddedFields(structType)
		}
	}
}

// parseEditedContentTypes parses the edited file's new content and collects
// its struct type declarations. Returns nil if newContent is empty or fails
// to parse.
func parseEditedContentTypes(newContent string, typeDecls map[string][]string) *ast.File {
	if strings.TrimSpace(newContent) == "" {
		return nil
	}
	fset := token.NewFileSet()
	newFile, err := parser.ParseFile(fset, "", newContent, 0)
	if err != nil {
		return nil
	}
	collectStructTypeDecls(newFile, typeDecls)
	return newFile
}

// parsePackageFiles parses all non-test .go files in dir (excluding
// excludeFile) and collects their struct type declarations. Directories,
// test files, and unparseable files are skipped.
func parsePackageFiles(dir, excludeFile string, typeDecls map[string][]string) []*ast.File {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	excludeBase := filepath.Base(excludeFile)
	var files []*ast.File
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		if name == excludeBase {
			continue // skip the file being edited (already parsed as newContent)
		}

		path := filepath.Join(dir, name)
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			continue
		}
		files = append(files, file)
		collectStructTypeDecls(file, typeDecls)
	}
	return files
}

// collectReceiverMethods builds a map of receiver type name → set of method
// names defined across the given parsed files.
func collectReceiverMethods(files []*ast.File) map[string]map[string]bool {
	methods := make(map[string]map[string]bool)
	for _, file := range files {
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
	return methods
}

// promoteEmbeddedMethodSets returns, per receiver type, its own methods plus
// all methods promoted transitively from embedded types (cycle-safe).
func promoteEmbeddedMethodSets(methods map[string]map[string]bool, typeDecls map[string][]string) map[string]map[string]bool {
	result := make(map[string]map[string]bool)
	for typeName, ownMethods := range methods {
		completeMethods := make(map[string]bool, len(ownMethods))
		for m := range ownMethods {
			completeMethods[m] = true
		}
		promoteEmbeddedMethods(typeName, typeDecls, methods, completeMethods, make(map[string]bool))
		result[typeName] = completeMethods
	}
	return result
}

// scanPackageTypeMethods reads all .go files in the same directory (excluding
// the edited file and test files) and builds a map of type name → set of
// method names. This enables duck-typing checks without full type-checking.
// It handles method promotion from embedded structs (Bug C fix).
// The newContent parameter allows parsing types from the edited file for
// proper embedded promotion detection.
func scanPackageTypeMethods(dir, excludeFile, newContent string) map[string]map[string]bool {
	// First pass: collect struct type declarations (for embedded field info).
	typeDecls := make(map[string][]string) // type name → list of embedded type names
	var filesToProcess []*ast.File

	// Parse the newContent (edited file) to include its types.
	if edited := parseEditedContentTypes(newContent, typeDecls); edited != nil {
		filesToProcess = append(filesToProcess, edited)
	}
	// Parse other files in the directory.
	filesToProcess = append(filesToProcess, parsePackageFiles(dir, excludeFile, typeDecls)...)

	// Second pass: collect all methods.
	methods := collectReceiverMethods(filesToProcess)

	// Third pass: promote methods from embedded types (depth-first, handle cycles).
	return promoteEmbeddedMethodSets(methods, typeDecls)
}

// promoteEmbeddedMethods recursively promotes methods from embedded types.
// Uses visited map to detect and break cycles.
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
		// Add all methods from embedded type
		if embMethods, ok := methods[embName]; ok {
			for m := range embMethods {
				completeMethods[m] = true
			}
		}
		// Recurse into embedded type's own embeddings
		promoteEmbeddedMethods(embName, typeDecls, methods, completeMethods, visited)
	}
}

// formatComplianceWarning produces the guidance string injected into the tool result.
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
