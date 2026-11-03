package agent

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

func TestIssue576_BugC_EmbeddedPromotion(t *testing.T) {
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

	typeMethods := checkEmbeddedPromotionOnly(newContent)

	if methods, ok := typeMethods["Wrapper"]; !ok {
		t.Error("Bug C not fixed: Wrapper should have methods")
	} else if !methods["Read"] || !methods["Close"] {
		t.Errorf("Bug C not fixed: Wrapper should have Read and Close, got: %v", methods)
	}

	if methods, ok := typeMethods["base"]; !ok {
		t.Error("base should have methods")
	} else if !methods["Read"] {
		t.Errorf("base should have Read, got: %v", methods)
	}

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

func checkEmbeddedPromotionOnly(content string) map[string]map[string]bool {

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "", content, 0)
	if err != nil {
		return nil
	}

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

	if strings.Contains(warning, "Level3 is missing method") {
		t.Errorf("Multi-level promotion not working: Level3 should have A and B via promotion\nGot: %s", warning)
	}
}

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

	if len(changes) == 0 {
		t.Error("Bug B not fixed: Interface method removal should be detected")
	}

	found := false
	for _, c := range changes {
		if c.Symbol == "InterfaceA" && c.Kind == "signature-changed" {
			found = true

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
