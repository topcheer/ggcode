package tui

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
)

type langInfo struct {
	code    string
	file    string
	catalog string
	keys    map[string]bool
}

func TestI18NCompleteness(t *testing.T) {

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

	enKeys, err := extractKeysFromSwitchFile("i18n_en.go", "enCatalog")
	if err != nil {
		t.Fatalf("Failed to extract English keys: %v", err)
	}

	if len(enKeys) == 0 {
		t.Fatalf("No keys found in English catalog")
	}

	t.Logf("Found %d keys in English (baseline) catalog", len(enKeys))

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

	for _, lang := range langFiles {
		if len(lang.keys) == 0 {
			continue
		}

		missing := findMissingKeys(enKeys, lang.keys)
		if len(missing) > 0 {
			t.Logf("Language %s is missing %d translations:", lang.code, len(missing))

			sortedMissing := make([]string, 0, len(missing))
			for k := range missing {
				sortedMissing = append(sortedMissing, k)
			}
			sort.Strings(sortedMissing)

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

func extractKeysFromSwitchFile(filename, funcName string) (map[string]bool, error) {

	if _, err := os.Stat(filename); os.IsNotExist(err) {
		return nil, fmt.Errorf("file not found: %s", filename)
	}

	fset := token.NewFileSet()
	node, err := parser.ParseFile(fset, filename, nil, parser.ParseComments)
	if err != nil {
		return nil, fmt.Errorf("parse error: %w", err)
	}

	keys := make(map[string]bool)

	for _, decl := range node.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != funcName {
			continue
		}

		ast.Inspect(fn.Body, func(n ast.Node) bool {
			sw, ok := n.(*ast.SwitchStmt)
			if !ok {
				return true
			}

			for _, stmt := range sw.Body.List {
				caseClause, ok := stmt.(*ast.CaseClause)
				if !ok {
					continue
				}

				for _, expr := range caseClause.List {
					if lit, ok := expr.(*ast.BasicLit); ok && lit.Kind == token.STRING {

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

func findMissingKeys(baseline, target map[string]bool) map[string]bool {
	missing := make(map[string]bool)
	for k := range baseline {
		if !target[k] {
			missing[k] = true
		}
	}
	return missing
}

func TestI18NVerbConsistency(t *testing.T) {
	en := parseCatalogCases(t, "i18n_en.go")
	for _, lang := range []string{"i18n_zh.go", "i18n_ja.go", "i18n_ko.go", "i18n_es.go", "i18n_fr.go", "i18n_de.go", "i18n_ru.go", "i18n_pt.go", "i18n_vi.go"} {
		cat := parseCatalogCases(t, lang)
		for key, enVal := range en {
			lv, ok := cat[key]
			if !ok {
				continue
			}
			if verbCounts(enVal) != verbCounts(lv) {
				t.Errorf("%s %q: verb counts differ - lang=%q en=%q", lang, key, verbCounts(lv), verbCounts(enVal))
			}
		}
	}
}

func parseCatalogCases(t *testing.T, filename string) map[string]string {
	t.Helper()
	data, err := os.ReadFile(filename)
	if err != nil {
		t.Fatalf("read %s: %v", filename, err)
	}

	re := regexp.MustCompile(`(?m)^\tcase "([^"]+)":\n(?:\t*//[^\n]*\n|\t*\n)*\t\treturn "((?:[^"\\]|\\.)*)"`)
	entries := map[string]string{}
	for _, m := range re.FindAllStringSubmatch(string(data), -1) {
		entries[m[1]] = m[2]
	}
	return entries
}

func verbCounts(s string) string {
	re := regexp.MustCompile(`%[a-zA-Z]`)
	vs := re.FindAllString(s, -1)
	sort.Strings(vs)
	return strings.Join(vs, ",")
}
