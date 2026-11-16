package citestselect

import (
	"reflect"
	"testing"
)

// The synthetic graph uses the real repo package paths because the
// extra path mappings hardcode them; synthetic paths would trip the
// stale-mapping fail-open.
const (
	cfg   = "github.com/topcheer/ggcode/internal/config"
	art   = "github.com/topcheer/ggcode/internal/agentruntime"
	webu  = "github.com/topcheer/ggcode/internal/webui"
	tool  = "github.com/topcheer/ggcode/internal/tool"
	ggc   = "github.com/topcheer/ggcode/cmd/ggcode"
	leaf  = "github.com/topcheer/ggcode/internal/leaf"
	skkit = "github.com/topcheer/ggcode/desktop/wailskit"
)

func testGraph() Graph {
	return Graph{
		DirToPkg: map[string]string{
			"internal/config":       cfg,
			"internal/agentruntime": art,
			"internal/webui":        webu,
			"internal/tool":         tool,
			"cmd/ggcode":            ggc,
			"internal/leaf":         leaf,
			"desktop/wailskit":      skkit,
		},
		Imports: map[string][]string{
			tool:  {cfg},
			art:   {cfg, tool},
			ggc:   {tool, webu},
			webu:  {},
			cfg:   {},
			skkit: {},
			// leaf is only imported by tool TEST files; the graph merges
			// test-only imports into the base package.
			leaf: {},
		},
	}
}

func withTestOnlyImport(g Graph) Graph {
	im := map[string][]string{}
	for k, v := range g.Imports {
		im[k] = append([]string{}, v...)
	}
	im[tool] = append(im[tool], leaf)
	return Graph{DirToPkg: g.DirToPkg, Imports: im}
}

func assertPackages(t *testing.T, changed []string, g Graph, want []string) {
	t.Helper()
	got := Select(changed, g)
	if got.Mode != ModePackages || !reflect.DeepEqual(got.Packages, want) {
		t.Errorf("Select(%q) = %+v, want packages %v", changed, got, want)
	}
}

func TestSelectNoneForNonGoPaths(t *testing.T) {
	g := testGraph()
	for _, changed := range [][]string{
		{},
		{"", "   "},
		{"README.md", "docs/ci.md"},
		{"mobile/flutter/lib/main.dart"},
		{"desktop/ggcode-desktop-wails/app.go"},
		{".github/workflows/release.yml"},
	} {
		if got := Select(changed, g); got.Mode != ModeNone {
			t.Errorf("Select(%q) = %+v, want mode %s", changed, got, ModeNone)
		}
	}
}

func TestSelectAllForFullTriggers(t *testing.T) {
	g := testGraph()
	for _, p := range []string{
		"go.mod", "go.sum", "Makefile",
		".github/workflows/ci.yml",
		"internal/citestselect/select.go",
		"internal/citestselect/ciselect/main.go",
	} {
		if got := Select([]string{p, "docs/x.md"}, g); got.Mode != ModeAll {
			t.Errorf("Select(%q) = %+v, want mode %s", p, got, ModeAll)
		}
	}
}

func TestSelectClosureIncludesDependents(t *testing.T) {
	// gates.go: direct config hit, the config->agentruntime source-anchor
	// mapping, plus the transitive importers of config (tool, cmd/ggcode).
	// webui and leaf are outside the closure.
	assertPackages(t, []string{"internal/config/gates.go"}, testGraph(),
		[]string{ggc, art, cfg, tool})
}

func TestSelectHonorsTestOnlyImports(t *testing.T) {
	// Changing leaf selects tool (tool tests import leaf) and tool own
	// dependents (agentruntime, cmd/ggcode).
	assertPackages(t, []string{"internal/leaf/leaf.go"}, withTestOnlyImport(testGraph()),
		[]string{ggc, art, leaf, tool})
}

func TestSelectWebMapping(t *testing.T) {
	// web/ changes run internal/webui tests; cmd/ggcode imports webui,
	// so it joins via the closure.
	assertPackages(t, []string{"web/src/chat/Composer.tsx", "web/vite.config.ts"}, testGraph(),
		[]string{ggc, webu})
}

func TestSelectSubdirectoryFileMapsToPackage(t *testing.T) {
	assertPackages(t, []string{"internal/tool/testdata/fixture.json"}, testGraph(),
		[]string{ggc, art, tool})
}

func TestSelectWailskitMaps(t *testing.T) {
	// desktop/wailskit is part of the root module and IS in the CI test
	// roots (desktop/ggcode-desktop-wails is a separate module and is
	// not); nothing in the roots imports wailskit, so it selects alone.
	assertPackages(t, []string{"desktop/wailskit/chat.go"}, testGraph(),
		[]string{skkit})
}

func TestSelectDeletedPathStillMaps(t *testing.T) {
	assertPackages(t, []string{"internal/webui/handler.go"}, testGraph(),
		[]string{ggc, webu})
}

func TestSelectUnmatchedRootPathFailsOpen(t *testing.T) {
	g := testGraph()
	for _, p := range []string{
		"internal/nosuchpkg/orphan.go", // removed/unknown package dir
		"cmd/README.md",                // no package at or above cmd/
	} {
		if got := Select([]string{p}, g); got.Mode != ModeAll {
			t.Errorf("Select(%q) = %+v, want fail-open %s", p, got, ModeAll)
		}
	}
}

func TestSelectStaleExtraMappingFailsOpen(t *testing.T) {
	g := testGraph()
	delete(g.Imports, webu) // mapping target vanished from the roots
	if got := Select([]string{"web/src/x.tsx"}, g); got.Mode != ModeAll {
		t.Errorf("got %+v, want fail-open %s", got, ModeAll)
	}
}
