package agent

import (
	"fmt"
	"go/ast"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

const maxImportWarnings = 4

type goImportInfo struct {
	name string
	path string
}

var commonGoStdlib = map[string]string{
	"fmt":       "fmt",
	"os":        "os",
	"strings":   "strings",
	"strconv":   "strconv",
	"time":      "time",
	"errors":    "errors",
	"context":   "context",
	"sync":      "sync",
	"io":        "io",
	"bytes":     "bytes",
	"bufio":     "bufio",
	"regexp":    "regexp",
	"sort":      "sort",
	"math":      "math",
	"path":      "path",
	"filepath":  "path/filepath",
	"reflect":   "reflect",
	"unicode":   "unicode",
	"hash":      "hash",
	"image":     "image",
	"crypto":    "crypto",
	"json":      "encoding/json",
	"xml":       "encoding/xml",
	"base64":    "encoding/base64",
	"hex":       "encoding/hex",
	"binary":    "encoding/binary",
	"http":      "net/http",
	"url":       "net/url",
	"smtp":      "net/smtp",
	"mail":      "net/mail",
	"flag":      "flag",
	"log":       "log",
	"logf":      "log",
	"slog":      "log/slog",
	"syscall":   "syscall",
	"signal":    "os/signal",
	"exec":      "os/exec",
	"user":      "os/user",
	"env":       "os",
	"text":      "text/template",
	"template":  "html/template",
	"html":      "html",
	"tar":       "archive/tar",
	"zip":       "archive/zip",
	"sql":       "database/sql",
	"driver":    "database/sql/driver",
	"redis":     "",
	"testing":   "testing",
	"tabwriter": "text/tabwriter",
	"scanner":   "text/scanner",
	"rand":      "math/rand",
	"big":       "math/big",
	"cmplx":     "math/cmplx",
	"bits":      "math/bits",
	"sha256":    "crypto/sha256",
	"sha1":      "crypto/sha1",
	"md5":       "crypto/md5",
	"hmac":      "crypto/hmac",
	"aes":       "crypto/aes",
	"cipher":    "crypto/cipher",
	"des":       "crypto/des",
	"tls":       "crypto/tls",
	"x509":      "crypto/x509",
	"ssh":       "golang.org/x/crypto/ssh",
	"pbkdf2":    "golang.org/x/crypto/pbkdf2",
	"bcrypt":    "golang.org/x/crypto/bcrypt",
	"yaml":      "gopkg.in/yaml.v3",
	"uuid":      "github.com/google/uuid",
}

const goModCacheTTL = 30 * time.Second

type goModCacheEntry struct {
	importMap map[string]string
	modTime   time.Time
	loadedAt  time.Time
}

var (
	goModCacheMu    sync.RWMutex
	goModCacheStore = make(map[string]goModCacheEntry)
)

var goModRequirePattern = regexp.MustCompile(`^\s*([a-zA-Z0-9._\-/]+)\s+v[0-9]`)

func loadGoModImports(workingDir string) map[string]string {
	if workingDir == "" {
		return nil
	}

	goModCacheMu.RLock()
	if entry, ok := goModCacheStore[workingDir]; ok && time.Since(entry.loadedAt) < goModCacheTTL {
		goModCacheMu.RUnlock()
		return entry.importMap
	}
	goModCacheMu.RUnlock()

	goModPath := findGoMod(workingDir)
	if goModPath == "" {
		goModCacheMu.Lock()
		goModCacheStore[workingDir] = goModCacheEntry{importMap: nil, loadedAt: time.Now()}
		goModCacheMu.Unlock()
		return nil
	}

	info, err := os.Stat(goModPath)
	if err != nil {
		return nil
	}

	goModCacheMu.RLock()
	if entry, ok := goModCacheStore[workingDir]; ok && !info.ModTime().After(entry.modTime) {
		goModCacheMu.RUnlock()
		return entry.importMap
	}
	goModCacheMu.RUnlock()

	data, err := os.ReadFile(goModPath)
	if err != nil {
		return nil
	}

	result := parseGoModRequires(string(data), goModPath)

	goModCacheMu.Lock()
	goModCacheStore[workingDir] = goModCacheEntry{
		importMap: result,
		modTime:   info.ModTime(),
		loadedAt:  time.Now(),
	}
	goModCacheMu.Unlock()

	return result
}

func findGoMod(startDir string) string {
	dir := startDir
	for i := 0; i < 20; i++ {
		p := filepath.Join(dir, "go.mod")
		if _, err := os.Stat(p); err == nil {
			return p
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return ""
}

func parseGoModRequires(content, goModPath string) map[string]string {
	result := make(map[string]string)

	modulePath := ""
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "module ") {
			modulePath = strings.TrimSpace(strings.TrimPrefix(line, "module "))
			break
		}
	}

	inRequireBlock := false
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)

		if trimmed == "require (" {
			inRequireBlock = true
			continue
		}
		if strings.HasPrefix(trimmed, "require ") {

			parseRequireLine(trimmed[len("require "):], modulePath, result)
			continue
		}
		if trimmed == ")" && inRequireBlock {
			inRequireBlock = false
			continue
		}
		if inRequireBlock {
			parseRequireLine(trimmed, modulePath, result)
		}
	}

	return result
}

func parseRequireLine(line, modulePath string, result map[string]string) {
	matches := goModRequirePattern.FindStringSubmatch(line)
	if len(matches) < 2 {
		return
	}
	modPath := matches[1]

	if modulePath != "" && strings.HasPrefix(modPath, modulePath) {
		return
	}

	if strings.Contains(line, "// indirect") {
		return
	}

	segments := strings.Split(modPath, "/")
	last := segments[len(segments)-1]
	if isVersionSegment(last) && len(segments) >= 2 {
		last = segments[len(segments)-2]
	}

	if last == "" || isVersionSegment(last) {
		return
	}

	if _, isStdlib := commonGoStdlib[last]; isStdlib {
		return
	}

	if result[last] == "" {
		result[last] = modPath
	}
}

func checkGoImportsASTWithDir(filePath string, f *ast.File, workingDir string) []string {
	if f == nil {
		return nil
	}

	var imports []goImportInfo
	for _, imp := range f.Imports {
		if imp == nil || imp.Path == nil {
			continue
		}
		rawPath := strings.Trim(imp.Path.Value, `"`)
		name := ""
		if imp.Name != nil {
			name = imp.Name.Name
		} else {
			parts := strings.Split(rawPath, "/")
			last := parts[len(parts)-1]

			if isVersionSegment(last) && len(parts) >= 2 {
				name = parts[len(parts)-2]
			} else {
				name = last
			}
		}

		if name == "_" || name == "." {
			continue
		}
		imports = append(imports, goImportInfo{name: name, path: rawPath})
	}

	if len(imports) == 0 && !hasGoCodeDecls(f) {

		return nil
	}

	var warnings []string

	usedIdents := make(map[string]bool)
	for _, decl := range f.Decls {
		genDecl, ok := decl.(*ast.GenDecl)
		if ok && genDecl.Tok == token.IMPORT {
			continue
		}
		ast.Inspect(decl, func(n ast.Node) bool {
			if ident, ok := n.(*ast.Ident); ok {
				usedIdents[ident.Name] = true
			}
			return true
		})
	}

	importNameSet := make(map[string]bool)
	for _, imp := range imports {
		importNameSet[imp.name] = true
		if !usedIdents[imp.name] {
			warnings = append(warnings, fmt.Sprintf(
				"Unused import: %q (%s) is imported but never referenced - "+
					"remove it or the build will fail with \"imported and not used\".",
				imp.name, imp.path))
		}
	}

	seenMissing := make(map[string]bool)
	for _, decl := range f.Decls {
		genDecl, ok := decl.(*ast.GenDecl)
		if ok && genDecl.Tok == token.IMPORT {
			continue
		}
		ast.Inspect(decl, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			ident, ok := sel.X.(*ast.Ident)
			if !ok {
				return true
			}
			pkgName := ident.Name

			if importNameSet[pkgName] {
				return true
			}

			suggestedPath, known := commonGoStdlib[pkgName]
			if !known {

				if workingDir != "" {
					if modPath := loadGoModImports(workingDir); modPath != nil {
						if p, ok := modPath[pkgName]; ok && p != "" {
							suggestedPath = p
							known = true
						}
					}
				}
			}
			if !known || suggestedPath == "" {
				return true
			}
			if seenMissing[pkgName] {
				return true
			}
			seenMissing[pkgName] = true
			warnings = append(warnings, fmt.Sprintf(
				"Likely missing import: %q.%s references package %q but it is not imported. "+
					"Add `import %s %q` or the build will fail with \"undefined: %s\".",
				pkgName, sel.Sel.Name, suggestedPath, pkgName, suggestedPath, pkgName))
			return true
		})
	}

	if len(warnings) > maxImportWarnings {
		warnings = warnings[:maxImportWarnings]
	}

	return warnings
}

func hasGoCodeDecls(f *ast.File) bool {
	for _, decl := range f.Decls {
		if _, ok := decl.(*ast.GenDecl); ok {
			continue
		}
		if _, ok := decl.(*ast.FuncDecl); ok {
			return true
		}
	}
	return false
}

func checkGoImports(filePath, src string) []string {
	return checkGoImportsWithDir(filePath, src, "")
}

func checkGoImportsWithDir(filePath, src, workingDir string) []string {
	if strings.TrimSpace(src) == "" {
		return nil
	}
	f, _, err := parseGoSource(filePath, src, 0)
	if err != nil {
		return nil
	}
	return checkGoImportsASTWithDir(filePath, f, workingDir)
}

func isVersionSegment(s string) bool {
	if len(s) < 2 || s[0] != 'v' {
		return false
	}
	for _, c := range s[1:] {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
