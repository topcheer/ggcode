package agent

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

// TestIssue576_BugC_EmbeddedPromotion tests that interface compliance checks
// properly handle method promotion from embedded fields (Bug C).
func TestIssue576_BugC_EmbeddedPromotion(t *testing.T) {
	// Probe scenario: base has Read(), Wrapper embeds base and has Close()
	// Interface Closer{Read();Close()} should NOT flag Wrapper (has Read via promotion)
	const oldContent = `package test

type base struct{}

func (b base) Read() error {
	return nil
}

type Wrapper struct {
	base
}

func (w Wrapper) Close() error {
	return nil
}
`

	const newContent = `package test

type Closer interface {
	Read() error
	Close() error
}

type base struct{}

func (b base) Read() error {
	return nil
}

type Wrapper struct {
	base
}

func (w Wrapper) Close() error {
	return nil
}
`

	// Use a helper to check only the content provided, without scanning directory
	typeMethods := checkEmbeddedPromotionOnly(newContent)

	// Wrapper should have both Read (from embedded base) and Close (own)
	if methods, ok := typeMethods["Wrapper"]; !ok {
		t.Error("Bug C not fixed: Wrapper should have methods")
	} else if !methods["Read"] || !methods["Close"] {
		t.Errorf("Bug C not fixed: Wrapper should have Read and Close, got: %v", methods)
	}

	// base should have only Read
	if methods, ok := typeMethods["base"]; !ok {
		t.Error("base should have methods")
	} else if !methods["Read"] {
		t.Errorf("base should have Read, got: %v", methods)
	}

	// But if a type genuinely lacks a method (not from promotion), it should still be caught
	const missingContent = `package test

type Closer interface {
	Read() error
	Close() error
}

type base struct{}

func (b base) Read() error {
	return nil
}

type NotEnough struct {
	// Has neither Read nor Close
}
`
	_ = checkInterfaceCompliance("test.go", oldContent, missingContent)
	// NotEnough has no methods, so it shouldn't be flagged (no overlap heuristic)
	// But if we modify it to have one method, it should be flagged for the other
	const partialContent = `package test

type Closer interface {
	Read() error
	Close() error
}

type base struct{}

func (b base) Read() error {
	return nil
}

type Partial struct{}

func (p Partial) Read() error {
	return nil
	// Missing Close()
}
`
	warning3 := checkInterfaceCompliance("test.go", oldContent, partialContent)
	if !strings.Contains(warning3, "Partial is missing method") || !strings.Contains(warning3, "Close") {
		t.Errorf("Genuine missing method should still be detected\nGot: %s", warning3)
	}
}

// checkEmbeddedPromotionOnly tests embedded promotion without directory scanning
func checkEmbeddedPromotionOnly(content string) map[string]map[string]bool {
	// Parse the content to extract type declarations with embedded fields
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "", content, 0)
	if err != nil {
		return nil
	}

	// Collect type declarations
	typeDecls := make(map[string][]string)
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

	// Collect methods
	methods := make(map[string]map[string]bool)
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

	// Promote from embedded types
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

// TestIssue576_BugC_MultiLevelEmbedding tests multi-level embedded promotion.
func TestIssue576_BugC_MultiLevelEmbedding(t *testing.T) {
	const oldContent = `package test

type Level1 struct{}

func (l Level1) A() error {
	return nil
}

type Level2 struct {
	Level1
}

func (l Level2) B() error {
	return nil
}

type Level3 struct {
	Level2
}

func (l Level3) C() error {
	return nil
}
`

	const newContent = `package test

type ABC interface {
	A() error
	B() error
	C() error
}

type Level1 struct{}

func (l Level1) A() error {
	return nil
}

type Level2 struct {
	Level1
}

func (l Level2) B() error {
	return nil
}

type Level3 struct {
	Level2
}

func (l Level3) C() error {
	return nil
}
`

	warning := checkInterfaceCompliance("test.go", oldContent, newContent)

	// Level3 should have A (from Level1 via Level2), B (from Level2), and C (own)
	// No warnings expected
	if strings.Contains(warning, "Level3 is missing method") {
		t.Errorf("Multi-level promotion not working: Level3 should have A and B via promotion\nGot: %s", warning)
	}
}

// TestIssue576_BugB_InterfaceMethodRemoval tests that export_guard detects
// when methods are removed from exported interfaces (Bug B).
func TestIssue576_BugB_InterfaceMethodRemoval(t *testing.T) {
	const oldContent = `package test

type InterfaceA interface {
	A() error
	B() error
	C() error
}
`

	const newContent = `package test

type InterfaceA interface {
	A() error
	B() error
	// C() removed - breaking change!
}
`

	oldSyms := parseExportedSymbolsFromSource([]byte(oldContent))
	newSyms := parseExportedSymbolsFromSource([]byte(newContent))

	changes := diffExportSymbols(oldSyms, newSyms)

	// Bug B fix: Should detect that InterfaceA's method set changed (C removed)
	if len(changes) == 0 {
		t.Error("Bug B not fixed: Interface method removal should be detected")
	}

	// Should report signature-changed for the interface
	found := false
	for _, c := range changes {
		if c.Symbol == "InterfaceA" && c.Kind == "signature-changed" {
			found = true
			// Verify the old and new signatures show the method removal
			if !strings.Contains(c.Detail, "→") {
				t.Errorf("Expected old→new signature format, got: %s", c.Detail)
			}
			break
		}
	}
	if !found {
		t.Errorf("Expected InterfaceA to have signature-changed, got changes: %+v", changes)
	}
}

// TestIssue576_BugB_InterfaceRenameNoChange tests that renaming an interface
// without changing its method set correctly detects the old name as removed.
// Note: The new name is not reported as "added" because diff only checks old symbols.
func TestIssue576_BugB_InterfaceRenameNoChange(t *testing.T) {
	const oldContent = `package test

type Helper interface {
	A() error
	B() error
}
`

	const newContent = `package test

type HelperNewName interface {
	A() error
	B() error
}
`

	oldSyms := parseExportedSymbolsFromSource([]byte(oldContent))
	newSyms := parseExportedSymbolsFromSource([]byte(newContent))

	changes := diffExportSymbols(oldSyms, newSyms)

	// Should detect Helper was removed (not signature-changed, since it's gone)
	hasHelperRemoved := false
	for _, c := range changes {
		if c.Symbol == "Helper" && c.Kind == "removed" {
			hasHelperRemoved = true
			break
		}
	}
	if !hasHelperRemoved {
		t.Errorf("Helper should be marked as removed, got changes: %+v", changes)
	}
}

// TestIssue576_BugB_InterfaceSignatureChange tests that interface method
// signature changes are still detected correctly.
func TestIssue576_BugB_InterfaceSignatureChange(t *testing.T) {
	const oldContent = `package test

type InterfaceX interface {
	Foo(a int) error
	Bar(b string) error
}
`

	const newContent = `package test

type InterfaceX interface {
	Foo(a int, b string) error // Signature changed
	Bar(b string) error
}
`

	oldSyms := parseExportedSymbolsFromSource([]byte(oldContent))
	newSyms := parseExportedSymbolsFromSource([]byte(newContent))

	changes := diffExportSymbols(oldSyms, newSyms)

	// Should detect signature change
	found := false
	for _, c := range changes {
		if c.Symbol == "InterfaceX" && c.Kind == "signature-changed" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("Interface method signature change should be detected, got changes: %+v", changes)
	}
}
