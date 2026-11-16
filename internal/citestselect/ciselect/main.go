// Command ciselect prints the CI test selection (#5416) for a list of
// changed paths: "none", "all", or a space-separated package list. It
// always exits 0 and fails open to "all" on any internal error so a
// selector hiccup can never reduce CI coverage.
package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/topcheer/ggcode/internal/citestselect"
)

// nl is the line separator, spelled without an escape so source
// generation pipelines cannot eat the backslash.
var nl = string(rune(10))

func main() {
	if len(os.Args) != 3 || os.Args[1] != "-changed" {
		fmt.Fprintln(os.Stderr, "usage: ciselect -changed <changed-paths-file>")
		fmt.Println(citestselect.ModeAll)
		return
	}
	data, err := os.ReadFile(os.Args[2])
	if err != nil {
		fmt.Fprintln(os.Stderr, "ciselect: read changed paths:", err, "- failing open to full suite")
		fmt.Println(citestselect.ModeAll)
		return
	}
	graph, err := loadGraph()
	if err != nil {
		fmt.Fprintln(os.Stderr, "ciselect:", err, "- failing open to full suite")
		fmt.Println(citestselect.ModeAll)
		return
	}
	sel := citestselect.Select(strings.Split(string(data), nl), graph)
	if sel.Mode == citestselect.ModePackages {
		fmt.Println(strings.Join(sel.Packages, " "))
		return
	}
	fmt.Println(sel.Mode)
}

// loadGraph gathers dir->package and importer->imports (including
// test-only imports) for the CI test roots via one go list call.
//
// go list -test renders synthesized entries; only two shapes matter here:
//
//	pkg [pkg.test]          test-augmented variant of pkg
//	pkg_test [pkg.test]     external test package of pkg
//
// Both credit their edges to the selectable package encoded in the
// bracket suffix. Bare "<pkg>.test" rows are synthesized test mains and
// are skipped entirely.
func loadGraph() (citestselect.Graph, error) {
	cmd := exec.Command("go", "list", "-tags", "goolm", "-test",
		"-f", "{{.Dir}}|{{.ImportPath}}|{{join .Imports \",\"}}",
		"./cmd/...", "./internal/...", "./desktop/wailskit/...")
	out, err := cmd.Output()
	if err != nil {
		return citestselect.Graph{}, fmt.Errorf("go list: %v", err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		return citestselect.Graph{}, err
	}
	cwd = filepath.Clean(cwd)

	g := citestselect.Graph{DirToPkg: map[string]string{}, Imports: map[string][]string{}}
	addDeps := func(name, imports string) {
		for _, dep := range strings.Split(imports, ",") {
			dep = strings.TrimSpace(dep)
			if i := strings.Index(dep, " ["); i >= 0 {
				dep = dep[:i]
			}
			if dep == "" {
				continue
			}
			g.Imports[name] = append(g.Imports[name], dep)
		}
	}
	for _, line := range strings.Split(string(out), nl) {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "|", 3)
		if len(parts) != 3 {
			return citestselect.Graph{}, fmt.Errorf("unparseable go list row: %q", line)
		}
		dir, ip, imports := parts[0], parts[1], parts[2]
		if i := strings.Index(ip, " ["); i >= 0 {
			base := strings.TrimSuffix(strings.TrimSuffix(ip[i+2:], "]"), ".test")
			addDeps(base, imports)
			continue
		}
		if strings.HasSuffix(ip, ".test") {
			continue // synthesized test main
		}
		rel := strings.TrimPrefix(filepath.Clean(dir), cwd+string(os.PathSeparator))
		if rel == filepath.Clean(dir) {
			return citestselect.Graph{}, fmt.Errorf("package %s dir %s is outside the repo root", ip, dir)
		}
		if _, seen := g.DirToPkg[filepath.ToSlash(rel)]; !seen {
			g.DirToPkg[filepath.ToSlash(rel)] = ip
		}
		if _, seen := g.Imports[ip]; !seen {
			g.Imports[ip] = nil
		}
		addDeps(ip, imports)
	}
	return g, nil
}
