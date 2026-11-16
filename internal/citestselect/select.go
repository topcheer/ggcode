// Package citestselect computes the path-aware test selection for the CI
// Test job (#5416): which Go packages need go test for a given set of
// changed files. The selection is deliberately conservative - any
// ambiguity fails open to the full suite, never to fewer tests.
package citestselect

import (
	"path"
	"sort"
	"strings"
)

// Selection modes.
const (
	// ModeNone means no Go-test-relevant changes: skip go test entirely
	// (full-tree build and vet still run in the job).
	ModeNone = "none"
	// ModeAll means run the full ./cmd/... ./internal/... suite.
	ModeAll = "all"
	// ModePackages means run exactly Selection.Packages.
	ModePackages = "packages"
)

// Selection is the outcome of Select.
type Selection struct {
	Mode     string
	Packages []string
}

// Graph carries the package facts the selector needs. The caller gathers
// them via go list so the pure logic here stays trivially testable.
type Graph struct {
	// DirToPkg maps package directories (repo-relative, slash-separated)
	// to import paths for packages under the CI test roots.
	DirToPkg map[string]string
	// Imports maps each package under the roots to the packages it
	// imports, INCLUDING test-only imports (external _test packages
	// credit their base package), so dependents are never missed.
	Imports map[string][]string
}

// fullTriggerExacts are paths whose change always selects the full suite:
// module/toolchain state, build gates, and the CI workflow itself.
var fullTriggerExacts = map[string]bool{
	"go.mod":                   true,
	"go.sum":                   true,
	"Makefile":                 true,
	".github/workflows/ci.yml": true,
}

// fullTriggerPrefixes are path prefixes with the same full-suite effect.
// The selector's own package always full-tests: a behavior change here
// must be validated against everything it can deselect.
var fullTriggerPrefixes = []string{
	"internal/citestselect/",
}

// extraPathMappings maps paths outside the Go test roots to packages
// whose tests observe them. These are the regex-on-source anchors that
// read files outside their own package directory; keep in sync when new
// cross-package source readers are added.
var extraPathMappings = []struct{ prefix, pkg string }{
	// internal/webui tests read web/src/* and the committed
	// internal/webui/dist via os.ReadFile/os.ReadDir
	// (zz_sa82_stream_test.go, zz_issue5299_test.go).
	{"web/", "github.com/topcheer/ggcode/internal/webui"},
	// internal/agentruntime/zz_issue5401_describe_test.go reads
	// internal/config/gates.go source directly.
	{"internal/config/", "github.com/topcheer/ggcode/internal/agentruntime"},
}

// testedRootPrefixes are the roots the CI Test job runs (go test
// ./cmd/... ./internal/... ./desktop/wailskit/...). Files under them
// that match no package directory fail open to the full suite.
var testedRootPrefixes = []string{
	"cmd/",
	"internal/",
	"desktop/wailskit/",
}

// Select returns the test selection for the changed repo-relative paths.
func Select(changed []string, g Graph) Selection {
	for _, p := range changed {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if fullTriggerExacts[path.Clean(p)] || hasAnyPrefix(p, fullTriggerPrefixes) {
			return Selection{Mode: ModeAll}
		}
	}

	direct := map[string]bool{}
	for _, p := range changed {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if pkg, ok := matchPackageDir(p, g.DirToPkg); ok {
			direct[pkg] = true
		} else if hasAnyPrefix(p, testedRootPrefixes) {
			// A file under the tested roots that matches no package
			// directory (new directory, removed package, stray file):
			// fail open.
			return Selection{Mode: ModeAll}
		}
		if mapped, ok := extraMapping(p); ok {
			if _, selectable := g.Imports[mapped]; !selectable {
				// Stale mapping entry: fail open.
				return Selection{Mode: ModeAll}
			}
			direct[mapped] = true
		}
	}

	if len(direct) == 0 {
		return Selection{Mode: ModeNone}
	}

	// Reverse-dependency closure: every package that transitively imports
	// a directly-affected package must also be tested, because a change
	// can break dependents' compile or behavior (#5419: a gates.go change
	// broke agentruntime tests).
	affected := make(map[string]bool, len(direct))
	for pkg := range direct {
		affected[pkg] = true
	}
	for grew := true; grew; {
		grew = false
		for pkg, deps := range g.Imports {
			if affected[pkg] {
				continue
			}
			for _, dep := range deps {
				if affected[dep] {
					affected[pkg] = true
					grew = true
					break
				}
			}
		}
	}

	pkgs := make([]string, 0, len(affected))
	for pkg := range affected {
		pkgs = append(pkgs, pkg)
	}
	sort.Strings(pkgs)
	return Selection{Mode: ModePackages, Packages: pkgs}
}

// matchPackageDir maps a changed file to the deepest package whose
// directory contains it (handles testdata/ and other subdirectories).
func matchPackageDir(p string, dirToPkg map[string]string) (string, bool) {
	d := path.Dir(path.Clean(p))
	for {
		if pkg, ok := dirToPkg[d]; ok {
			return pkg, true
		}
		if d == "." || d == "/" {
			return "", false
		}
		d = path.Dir(d)
	}
}

func extraMapping(p string) (string, bool) {
	for _, m := range extraPathMappings {
		if strings.HasPrefix(p, m.prefix) {
			return m.pkg, true
		}
	}
	return "", false
}

func hasAnyPrefix(p string, prefixes []string) bool {
	for _, pre := range prefixes {
		if strings.HasPrefix(p, pre) {
			return true
		}
	}
	return false
}
