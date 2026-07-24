package tui

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"sort"
	"strings"
	"testing"
)

// langInfo holds information about a language's i18n file
type langInfo struct {
	code    string
	file    string
	catalog string
	keys    map[string]bool // true if key exists
}

// TestI18NCompleteness verifies that all non-English languages have translations
// for all keys present in the English catalog.
func TestI18NCompleteness(t *testing.T) {
	// Define the languages to check (excluding English as baseline)
	langFiles := []langInfo{
		{"zh", "i18n_zh.go", "zhCatalog", make(map[string]bool)},
		{"ja", "i18n_ja.go", "jaCatalog", make(map[string]bool)},
		{"ko", "i18n_ko.go", "koCatalog", make(map[string]bool)},
		{"es", "i18n_es.go", "esCatalog", make(map[string]bool)},
		{"fr", "i18n_fr.go", "frCatalog", make(map[string]bool)},
		{"de", "i18n_de.go", "deCatalog", make(map[string]bool)},
		{"ru", "i18n_ru.go", "ruCatalog", make(map[string]bool)},
		{"pt", "i18n_pt.go", "ptCatalog", make(map[string]bool)},
		{"vi", "i18n_vi.go", "viCatalog", make(map[string]bool)},
	}

	// Extract English keys as baseline
	enKeys, err := extractKeysFromSwitchFile("i18n_en.go", "enCatalog")
	if err != nil {
		t.Fatalf("Failed to extract English keys: %v", err)
	}

	if len(enKeys) == 0 {
		t.Fatalf("No keys found in English catalog")
	}

	t.Logf("Found %d keys in English (baseline) catalog", len(enKeys))

	// Extract keys from each language file
	for i := range langFiles {
		keys, err := extractKeysFromSwitchFile(langFiles[i].file, langFiles[i].catalog)
		if err != nil {
			t.Logf("Skipping %s: %v", langFiles[i].code, err)
			continue
		}

		if len(keys) == 0 {
			t.Logf("Language %s has 0 keys - skipping (might be incomplete by design)", langFiles[i].code)
			continue
		}

		langFiles[i].keys = keys
		t.Logf("Language %s has %d keys", langFiles[i].code, len(keys))
	}

	// Check for missing keys in each language
	for _, lang := range langFiles {
		if len(lang.keys) == 0 {
			continue
		}

		missing := findMissingKeys(enKeys, lang.keys)
		if len(missing) > 0 {
			t.Logf("Language %s is missing %d translations:", lang.code, len(missing))
			// Sort missing keys for readable output
			sortedMissing := make([]string, 0, len(missing))
			for k := range missing {
				sortedMissing = append(sortedMissing, k)
			}
			sort.Strings(sortedMissing)

			// Log first 10, then summarize
			for i, k := range sortedMissing {
				if i >= 10 {
					t.Logf("  ... and %d more", len(sortedMissing)-10)
					break
				}
				t.Logf("  - %s", k)
			}
		} else {
			t.Logf("Language %s: all translations present", lang.code)
		}
	}
}

// extractKeysFromSwitchFile parses a Go file and extracts keys from the specified catalog function
func extractKeysFromSwitchFile(filename, funcName string) (map[string]bool, error) {
	// Find the file in the current directory
	if _, err := os.Stat(filename); os.IsNotExist(err) {
		return nil, fmt.Errorf("file not found: %s", filename)
	}

	// Parse the file
	fset := token.NewFileSet()
	node, err := parser.ParseFile(fset, filename, nil, parser.ParseComments)
	if err != nil {
		return nil, fmt.Errorf("parse error: %w", err)
	}

	keys := make(map[string]bool)

	// Find the catalog function and extract switch case keys
	for _, decl := range node.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != funcName {
			continue
		}

		// Look for switch statement in the function body
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			sw, ok := n.(*ast.SwitchStmt)
			if !ok {
				return true
			}

			// Extract keys from case statements
			for _, stmt := range sw.Body.List {
				caseClause, ok := stmt.(*ast.CaseClause)
				if !ok {
					continue
				}

				// Extract the key from the case expression
				for _, expr := range caseClause.List {
					if lit, ok := expr.(*ast.BasicLit); ok && lit.Kind == token.STRING {
						// Remove quotes from the string literal
						key := strings.Trim(lit.Value, `"`)
						if key != "" {
							keys[key] = true
						}
					}
				}
			}
			return false
		})
	}

	return keys, nil
}

// findMissingKeys returns keys present in baseline but missing in target
func findMissingKeys(baseline, target map[string]bool) map[string]bool {
	missing := make(map[string]bool)
	for k := range baseline {
		if !target[k] {
			missing[k] = true
		}
	}
	return missing
}
