package tool

// sa-85 tests: Graph-of-Skills dependency closure - chain expansion,
// cycle safety, missing/mismatch reporting, depth cap, search rendering.

import (
	"strings"
	"testing"

	"github.com/topcheer/ggcode/internal/commands"
)

type closureSkillLookup map[string]*commands.Command

func (f closureSkillLookup) Get(name string) (*commands.Command, bool) {
	c, ok := f[name]
	return c, ok
}

func mkSkill(name string, deps ...string) *commands.Command {
	return &commands.Command{Name: name, Enabled: true, Dependencies: deps}
}

// Chain: c -> b -> a fully expands past the direct hop.
func TestSkillClosure_Chain(t *testing.T) {
	lookup := closureSkillLookup{
		"a": mkSkill("a"),
		"b": mkSkill("b", "a"),
		"c": mkSkill("c", "b"),
	}
	c := DependencyClosure(mkSkill("c", "b"), lookup, 0)
	if len(c.Order) != 2 || c.Order[0] != "b" || c.Order[1] != "a" {
		t.Fatalf("closure must reach b->a, got %+v", c.Order)
	}
	if strings.Join(c.Chain, "->") != "c->b->a" {
		t.Fatalf("chain render wrong: %v", c.Chain)
	}
}

// Cycle: a -> b -> a terminates and still reports the reachable member.
func TestSkillClosure_Cycle(t *testing.T) {
	lookup := closureSkillLookup{
		"a": mkSkill("a", "b"),
		"b": mkSkill("b", "a"),
	}
	c := DependencyClosure(mkSkill("a", "b"), lookup, 0)
	if len(c.Order) != 1 || c.Order[0] != "b" {
		t.Fatalf("cycle must terminate reporting b once, got %+v", c.Order)
	}
}

// Missing and disabled deps are named, not silently dropped.
func TestSkillClosure_MissingAndDisabled(t *testing.T) {
	lookup := closureSkillLookup{
		"c":   mkSkill("c", "ghost", "off"),
		"off": {Name: "off", Enabled: false},
	}
	c := DependencyClosure(mkSkill("c", "ghost", "off"), lookup, 0)
	if len(c.Missing) != 2 || c.Missing[0] != "ghost" || c.Missing[1] != "off" {
		t.Fatalf("ghost and disabled must be reported missing, got %+v", c.Missing)
	}
}

// Version-constrained dep that fails the constraint lands in Mismatch.
func TestSkillClosure_VersionMismatch(t *testing.T) {
	lookup := closureSkillLookup{
		"root": mkSkill("root", "dep@>=2.0.0"),
		"dep":  {Name: "dep", Enabled: true, Version: "1.2.3"},
	}
	c := DependencyClosure(mkSkill("root", "dep@>=2.0.0"), lookup, 0)
	if len(c.Mismatch) != 1 || !strings.Contains(c.Mismatch[0], "dep") {
		t.Fatalf("mismatch must be reported, got %+v", c.Mismatch)
	}
	if len(c.Order) != 0 {
		t.Fatalf("mismatched dep must not join the chain, got %+v", c.Order)
	}
}

// Depth cap truncates a 4-deep chain at skillClosureMaxDepth.
func TestSkillClosure_DepthCap(t *testing.T) {
	lookup := closureSkillLookup{
		"s1": mkSkill("s1", "s2"),
		"s2": mkSkill("s2", "s3"),
		"s3": mkSkill("s3", "s4"),
		"s4": mkSkill("s4"),
	}
	c := DependencyClosure(mkSkill("s1", "s2"), lookup, 0)
	if len(c.Order) != skillClosureMaxDepth || !c.Truncated {
		t.Fatalf("expected depth cap at %d with Truncated, got %+v (trunc=%v)", skillClosureMaxDepth, c.Order, c.Truncated)
	}
}

// String renders a compact hint; nothing to report -> empty.
func TestSkillClosure_String(t *testing.T) {
	lookup := closureSkillLookup{"b": mkSkill("b")}
	c := DependencyClosure(mkSkill("c", "b"), lookup, 0)
	if s := c.String(); !strings.Contains(s, "Deps(closure)") || !strings.Contains(s, "chain: c -> b") {
		t.Fatalf("hint wrong: %s", s)
	}
	if s := (DependencyClosure(mkSkill("solo"), lookup, 0)).String(); s != "" {
		t.Fatalf("no deps must render empty, got %s", s)
	}
}

// Search rendering surfaces the closure line for matched skills (queried
// path only; the full listing stays unchanged).
func TestSkillClosure_SearchRender(t *testing.T) {
	lookup := closureSkillLookup{
		"deploy-web": mkSkill("deploy-web", "build-assets"),
		"build-assets": {Name: "build-assets", Enabled: true, Description: "builds assets",
			Dependencies: []string{"install-deps"}},
		"install-deps": mkSkill("install-deps"),
	}
	matches := []skillSearchMatch{{name: "deploy-web", desc: "deploys"}}
	out := formatSkillSearchResults(matches, "deploy", "deploy", 1, lookup)
	if !strings.Contains(out, "Deps(closure)") || !strings.Contains(out, "chain: deploy-web -> build-assets -> install-deps") {
		t.Fatalf("search result must carry the closure chain, got:\n%s", out)
	}
	// Listing (empty query) must not append closures.
	listing := formatSkillSearchResults(matches, "", "", 1, lookup)
	if strings.Contains(listing, "Deps(closure)") {
		t.Fatalf("listing must stay unchanged, got:\n%s", listing)
	}
}

// Load-time hint upgrade: chain prefix + legacy advisory both present.
func TestSkillClosure_LoadHint(t *testing.T) {
	lookup := closureSkillLookup{"b": mkSkill("b")}
	got := closureDependencyHint(mkSkill("c", "b"), lookup)
	if !strings.Contains(got, "Deps(closure)") || !strings.Contains(got, "Prerequisite skills: b") {
		t.Fatalf("hint must carry chain and advisory, got: %s", got)
	}
}

// Nil safety: nil cmd / nil lookup never panic.
func TestSkillClosure_NilSafe(t *testing.T) {
	if s := (DependencyClosure(nil, nil, 0)).String(); s != "" {
		t.Fatalf("nil closure must render empty, got %s", s)
	}
	if s := skillClosureForSearch("x", nil); s != "" {
		t.Fatalf("nil lookup must yield empty, got %s", s)
	}
}
