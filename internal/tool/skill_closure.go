package tool

import (
	"fmt"
	"strings"

	"github.com/topcheer/ggcode/internal/commands"
)

const skillClosureMaxDepth = 3

type SkillClosure struct {
	Root      string
	Order     []string
	Chain     []string
	Missing   []string
	Mismatch  []string
	Truncated bool
}

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

			for _, dep := range cur.cmd.Dependencies {
				dep = strings.TrimSpace(dep)
				if dep == "" {
					continue
				}
				if !visited[commands.ParseDependency(dep).Name] {
					edgesLeft = true
					break
				}
			}
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
