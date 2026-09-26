package context

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// importSpecRe matches a single-line import spec that pulls in this package:
//
//	[import] [alias] "github.com/topcheer/ggcode/internal/context" [// comment]
//
// Group 1 captures the alias, if any. Lines that merely mention the path
// inside a string literal or a leading comment do not match.
var importSpecRe = regexp.MustCompile(
	`^(?:import\s+)?(?:(\w+)\s+)?"github\.com/topcheer/ggcode/internal/context"(?:\s*//.*)?$`)

// TestCtxpkgImportAlias enforces the repo-wide import convention documented
// on ContextManager (manager.go): every file outside this package that
// imports github.com/topcheer/ggcode/internal/context must alias it as
// "ctxpkg" so the identifier "context" stays reserved for the standard
// library. Without the alias, adding a stdlib context usage (for example a
// ctx context.Context parameter) to such a file produces a confusing
// compile error, because "context" resolves to this package instead.
func TestCtxpkgImportAlias(t *testing.T) {
	pkgDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	root := filepath.Dir(filepath.Dir(pkgDir)) // internal/context -> repo root

	var violations []string
	walkErr := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			name := d.Name()
			// .ggcode hosts worktrees with stale copies of this repo;
			// mobile, desktop, npm and python live outside this Go module.
			switch name {
			case ".git", ".ggcode", "vendor", "node_modules", "mobile", "desktop", "npm", "python", "bin":
				if path != root {
					return filepath.SkipDir
				}
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for i, line := range strings.Split(string(data), "\n") {
			trimmed := strings.TrimSpace(line)
			m := importSpecRe.FindStringSubmatch(trimmed)
			if len(m) < 2 {
				continue
			}
			if m[1] != "ctxpkg" {
				rel, relErr := filepath.Rel(root, path)
				if relErr != nil {
					rel = path
				}
				alias := m[1]
				if alias == "" {
					alias = "no alias"
				}
				violations = append(violations,
					rel+":"+strconv.Itoa(i+1)+
						": import of internal/context must be aliased as ctxpkg (got "+alias+")")
			}
		}
		return nil
	})
	if walkErr != nil {
		t.Fatalf("walk %s: %v", root, walkErr)
	}
	if len(violations) > 0 {
		t.Errorf("found %d ctxpkg alias violation(s):\n%s",
			len(violations), strings.Join(violations, "\n"))
	}
}
