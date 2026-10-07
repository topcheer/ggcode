package tool

// sa-85 (Graph-of-Skills-inspired, arXiv:2604.05333): dependency-aware
// skill retrieval.
//
// Frontier claim: semantic retrieval surfaces topically relevant skills
// but misses their PREREQUISITE CHAIN, creating a "prerequisite gap" that
// leaves the retrieved bundle execution-incomplete. GoS solves it with a
// dependency graph + closure-aware bundle retrieval.
//
// Go-native subset: skill packages already DECLARE dependencies
// (skill-dependency-declaration) but the advisory only lists direct
// dependencies and search results carry no dependency context at all.
// This file adds DependencyClosure: a bounded BFS over declared
// dependencies (cycle-safe, depth-capped) producing the prerequisite
// chain, missing and version-mismatched members - wired into both the
// search result rendering (bundle preview) and the load-time advisory
// (chain instead of flat names). Deterministic, zero LLM cost.

import (
	"fmt"
	"strings"

	"github.com/topcheer/ggcode/internal/commands"
)

// skillClosureMaxDepth bounds the BFS: deep chains past this depth are
// truncated (missing deps can never inflate the walk).
const skillClosureMaxDepth = 3

// SkillClosure is the bounded dependency closure of one root skill.
type SkillClosure struct {
	Root      string
	Order     []string // reachable deps in BFS order (excluding the root)
	Chain     []string // linearized render chain root -> d1 -> d2 ...
	Missing   []string // declared deps not found or disabled
	Mismatch  []string // found but failing the declared version constraint
	Truncated bool     // stopped at maxDepth with edges remaining
}

// DependencyClosure walks cmd's declared dependency edges breadth-first
// (max depth skillClosureMaxDepth), collecting the prerequisite chain,
// missing and version-mismatched members. Cycle-safe: a visited set stops
// re-expansion; a self-dependency is ignored. A nil lookup or root yields
// an empty closure.
func DependencyClosure(cmd *commands.Command, lookup SkillLookup, maxDepth int) SkillClosure {
	c := SkillClosure{Root: skillClosureRootName(cmd)}
	if cmd == nil || lookup == nil || len(cmd.Dependencies) == 0 {
		return c
	}
	if maxDepth <= 0 {
		maxDepth = skillClosureMaxDepth
	}
	visited := map[string]bool{c.Root: true}
	type frontier struct {
		cmd   *commands.Command
		depth int
	}
	queue := []frontier{{cmd: cmd, depth: 0}}
	edgesLeft := false
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if cur.depth >= maxDepth {
			edgesLeft = true
			continue
		}
		for _, dep := range cur.cmd.Dependencies {
			dep = strings.TrimSpace(dep)
			if dep == "" {
				continue
			}
			dc := commands.ParseDependency(dep)
			if visited[dc.Name] {
				continue
			}
			visited[dc.Name] = true
			depCmd, ok := lookup.Get(dc.Name)
			if !ok || depCmd == nil || !depCmd.Enabled {
				c.Missing = append(c.Missing, dc.Name)
				continue
			}
			if dc.Version != "" && !commands.CheckVersionConstraint(depCmd.Version, dc.Op, dc.Version) {
				c.Mismatch = append(c.Mismatch, fmt.Sprintf("%s (requires %s%s, found %s)",
					dc.Name, dc.Op, dc.Version, nonEmptyVersion(depCmd.Version)))
				continue
			}
			c.Order = append(c.Order, dc.Name)
			queue = append(queue, frontier{cmd: depCmd, depth: cur.depth + 1})
		}
	}
	c.Truncated = edgesLeft
	c.Chain = append([]string{c.Root}, c.Order...)
	return c
}

func skillClosureRootName(cmd *commands.Command) string {
	if cmd == nil {
		return ""
	}
	return cmd.Name
}

// String renders the closure as a compact bundle hint. Empty when the
// closure has nothing to report (no reachable, missing or mismatched deps).
func (c SkillClosure) String() string {
	if c.Root == "" {
		return ""
	}
	var parts []string
	if len(c.Order) > 0 {
		parts = append(parts, "chain: "+strings.Join(c.Chain, " -> "))
	}
	if len(c.Missing) > 0 {
		parts = append(parts, "missing: "+strings.Join(c.Missing, ", "))
	}
	if len(c.Mismatch) > 0 {
		parts = append(parts, "version: "+strings.Join(c.Mismatch, "; "))
	}
	if c.Truncated {
		parts = append(parts, fmt.Sprintf("(depth capped at %d)", skillClosureMaxDepth))
	}
	if len(parts) == 0 {
		return ""
	}
	return "Deps(closure) " + strings.Join(parts, " | ")
}

// skillClosureForSearch computes the closure hint for one search match;
// empty string when there is nothing to surface.
func skillClosureForSearch(name string, lookup SkillLookup) string {
	if lookup == nil {
		return ""
	}
	cmd, ok := lookup.Get(name)
	if !ok || cmd == nil {
		return ""
	}
	return DependencyClosure(cmd, lookup, skillClosureMaxDepth).String()
}

// closureDependencyHint upgrades buildDependencyHint's flat "Prerequisite
// skills: a, b" to a closure-aware chain so the agent sees the full
// prerequisite chain (GoS "prerequisite gap" fix), not just the first hop.
func closureDependencyHint(cmd *commands.Command, lookup SkillLookup) string {
	base := buildDependencyHint(cmd, lookup)
	c := DependencyClosure(cmd, lookup, skillClosureMaxDepth)
	chain := c.String()
	switch {
	case chain == "":
		return base
	case base == "":
		return chain
	default:
		return chain + " " + base
	}
}
