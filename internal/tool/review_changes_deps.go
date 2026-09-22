package tool

// review_changes_deps.go - Dependency change transparency for review_changes.
//
// Research grounding (CCS/arXiv 2601.00205, "Understanding Security Risks of
// AI Agents' Dependency Updates"): agents modify dependency manifests
// frequently and select known-vulnerable versions more often than humans;
// the paper motivates PR-time dependency-change screening. ggcode already
// has the detection side (dependency_vuln_check, typosquat_check,
// critical_file edit guards); what was missing is review-time transparency:
// a human-readable summary of which packages a diff adds, removes, or
// changes across ecosystem manifests.
//
// This is a report enhancement, not a new checker: it never blocks commit,
// only summarizes and calls out major-version jumps (the most disruptive
// remediation class per the paper) so the reviewer sees them at a glance.

import (
	"fmt"
	"sort"
	"strings"
)

// reviewManifestFiles maps manifest basenames to their ecosystem parser id.
var reviewManifestFiles = map[string]string{
	"go.mod":           "go",
	"package.json":     "npm",
	"composer.json":    "npm", // same quoted-key:value shape as package.json
	"requirements.txt": "pypi",
	"pyproject.toml":   "pypi-list",
	"Cargo.toml":       "cargo",
	"Gemfile":          "gem",
}

// reviewManifestKeyDenylist skips package.json top-level keys that look like
// dependency entries but are not (their values are version-like strings).
var reviewManifestKeyDenylist = map[string]bool{
	"version": true, "name": true, "type": true, "main": true,
	"private": true, "engines": true, "package_manager": true,
}

// manifestDep is one <name, version> tuple extracted from a manifest diff line.
type manifestDep struct {
	name    string
	version string
}

// reviewManifestBaseName returns the basename of a diff path.
func reviewManifestBaseName(path string) string {
	if i := strings.LastIndexByte(path, '/'); i >= 0 {
		return path[i+1:]
	}
	return path
}

// reviewManifestEcosystem reports which manifest format a path uses, if any.
func reviewManifestEcosystem(path string) string {
	return reviewManifestFiles[reviewManifestBaseName(path)]
}

// extractManifestDeps parses dependency tuples from one manifest file diff.
// lines must be the ordered +/-/context line stream (see reviewDiffLine.kind):
// section headers like `require (` or `[dependencies]` often appear as
// unchanged context lines, so block state must be tracked across all of them.
// Tuples are only extracted from added ('+') and removed ('-') lines.
func extractManifestDeps(eco string, lines []reviewDiffLine) (added, removed []manifestDep) {
	inRequireBlock := false
	inCargoDepSection := false
	collect := func(d manifestDep, kind byte) {
		if kind == '-' {
			removed = append(removed, d)
		} else {
			added = append(added, d)
		}
	}
	for _, dl := range lines {
		line := strings.TrimSpace(dl.content)
		switch eco {
		case "go":
			if line == "require (" {
				inRequireBlock = true
				continue
			}
			if line == ")" {
				inRequireBlock = false
				continue
			}
			if dl.kind == ' ' {
				continue
			}
			if strings.HasPrefix(line, "//") || strings.HasPrefix(line, "replace ") ||
				strings.HasPrefix(line, "exclude ") || strings.HasPrefix(line, "retract ") {
				continue
			}
			fields := strings.Fields(line)
			var name, ver string
			if strings.HasPrefix(line, "require ") && len(fields) >= 3 {
				name, ver = fields[1], fields[2]
			} else if inRequireBlock && len(fields) >= 2 {
				name, ver = fields[0], fields[1]
			} else {
				continue
			}
			if i := strings.Index(ver, "//"); i >= 0 {
				ver = strings.TrimSpace(ver[:i])
			}
			collect(manifestDep{name: name, version: ver}, dl.kind)
		case "npm":
			if dl.kind == ' ' {
				continue
			}
			if name, ver, ok := parseNPMStyleDep(line); ok {
				collect(manifestDep{name: name, version: ver}, dl.kind)
			}
		case "pypi":
			if dl.kind == ' ' {
				continue
			}
			if name, ver, ok := parsePyPIDep(line); ok {
				collect(manifestDep{name: name, version: ver}, dl.kind)
			}
		case "pypi-list":
			if dl.kind == ' ' {
				continue
			}
			if name, ver, ok := parsePyPIListDep(line); ok {
				collect(manifestDep{name: name, version: ver}, dl.kind)
			}
		case "cargo":
			if strings.HasPrefix(line, "[") {
				section := strings.ToLower(strings.Trim(line, "[]"))
				inCargoDepSection = strings.Contains(section, "dependencies")
				continue
			}
			if dl.kind == ' ' || !inCargoDepSection {
				continue
			}
			if i := strings.Index(line, "="); i > 0 {
				name := strings.TrimSpace(line[:i])
				rest := strings.TrimSpace(line[i+1:])
				if ver, ok := extractQuotedVersion(rest); ok {
					collect(manifestDep{name: name, version: ver}, dl.kind)
				}
			}
		case "gem":
			if dl.kind == ' ' || !strings.HasPrefix(line, "gem ") {
				continue
			}
			if name, ver, ok := parseGemDep(line); ok {
				collect(manifestDep{name: name, version: ver}, dl.kind)
			}
		}
	}
	return added, removed
}

// parseNPMStyleDep handles `"name": "version"` lines (package.json, composer.json).
func parseNPMStyleDep(line string) (string, string, bool) {
	i := strings.Index(line, `":`)
	if i < 0 || !strings.HasPrefix(strings.TrimSpace(line), `"`) {
		return "", "", false
	}
	name := strings.Trim(strings.TrimSpace(line[:i]), `" `)
	rest := strings.TrimSpace(line[i+2:])
	if ver, ok := extractQuotedVersion(rest); ok && name != "" && !reviewManifestKeyDenylist[name] {
		return name, ver, true
	}
	return "", "", false
}

// extractQuotedVersion pulls a quoted value and keeps it only if it looks
// like a version constraint (starts with a version-ish character).
func extractQuotedVersion(s string) (string, bool) {
	i := strings.IndexByte(s, '"')
	if i < 0 {
		return "", false
	}
	rest := s[i+1:]
	j := strings.IndexByte(rest, '"')
	if j < 0 {
		return "", false
	}
	ver := rest[:j]
	if ver == "" {
		return "", false
	}
	switch ver[0] {
	case '^', '~', '>', '<', '=', '*', 'v', '0', '1', '2', '3', '4', '5', '6', '7', '8', '9':
		return ver, true
	}
	return "", false
}

// parsePyPIDep handles requirements.txt lines: `name==1.0`, `name>=1.0`.
func parsePyPIDep(line string) (string, string, bool) {
	for _, bad := range []string{"#", "-"} {
		if strings.HasPrefix(line, bad) {
			return "", "", false
		}
	}
	i := strings.IndexAny(line, "=<>!~")
	if i <= 0 {
		return "", "", false
	}
	name := strings.TrimSpace(line[:i])
	if j := strings.IndexAny(name, "[ "); j >= 0 {
		name = name[:j]
	}
	ver := strings.TrimSpace(line[i:])
	if k := strings.IndexAny(ver, " ;,"); k >= 0 {
		ver = ver[:k]
	}
	if name == "" || ver == "" {
		return "", "", false
	}
	return name, ver, true
}

// parsePyPIListDep handles pyproject.toml dependency array entries:
// `"requests>=2.0",`.
func parsePyPIListDep(line string) (string, string, bool) {
	if !strings.HasPrefix(line, `"`) {
		return "", "", false
	}
	end := strings.IndexByte(line[1:], '"')
	if end < 0 {
		return "", "", false
	}
	inner := line[1 : 1+end]
	name, ver, ok := parsePyPIDep(inner)
	if !ok {
		// bare name entry: "requests"
		if strings.ContainsAny(inner, "=<>!~") {
			return "", "", false
		}
		return inner, "", true
	}
	return name, ver, true
}

// parseGemDep handles Gemfile lines: gem "rails", "7.0".
func parseGemDep(line string) (string, string, bool) {
	parts := strings.Split(line, ",")
	name := strings.Trim(strings.TrimSpace(strings.TrimPrefix(parts[0], "gem ")), `"' `)
	if name == "" {
		return "", "", false
	}
	if len(parts) > 1 {
		if ver, ok := extractQuotedVersion(strings.TrimSpace(parts[1])); ok {
			return name, ver, true
		}
	}
	return name, "", true
}

// depMajor extracts the leading major version number, or -1 when unknown.
func depMajor(ver string) int {
	ver = strings.TrimLeft(ver, "v^~><= *")
	num := 0
	got := false
	for i := 0; i < len(ver); i++ {
		if ver[i] >= '0' && ver[i] <= '9' {
			num = num*10 + int(ver[i]-'0')
			got = true
		} else {
			break
		}
	}
	if !got {
		return -1
	}
	return num
}

// buildDependencySummary compares removed vs added dependency tuples per
// manifest file and renders the review report section. Returns "" when no
// manifest files changed.
func buildDependencySummary(files []*reviewDiffFile) string {
	var sections []string
	// Deterministic file order.
	sorted := make([]*reviewDiffFile, len(files))
	copy(sorted, files)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].path < sorted[j].path })

	for _, f := range sorted {
		eco := reviewManifestEcosystem(f.path)
		if eco == "" {
			continue
		}
		added, removed := extractManifestDeps(eco, f.allLines)

		addedByName := map[string]string{}
		for _, d := range added {
			addedByName[d.name] = d.version
		}
		removedByName := map[string]string{}
		for _, d := range removed {
			removedByName[d.name] = d.version
		}

		var lines []string
		names := make([]string, 0, len(addedByName)+len(removedByName))
		seen := map[string]bool{}
		for n := range addedByName {
			names = append(names, n)
			seen[n] = true
		}
		for n := range removedByName {
			if !seen[n] {
				names = append(names, n)
			}
		}
		sort.Strings(names)
		for _, n := range names {
			oldV, hadOld := removedByName[n]
			newV, hasNew := addedByName[n]
			switch {
			case hadOld && hasNew && oldV == newV:
				continue // version unchanged (e.g. moved sections)
			case hadOld && hasNew:
				if mOld, mNew := depMajor(oldV), depMajor(newV); mOld >= 0 && mNew >= 0 && mOld != mNew {
					lines = append(lines, fmt.Sprintf("  ~ %s %s -> %s (MAJOR version jump, review breaking changes)", n, oldV, newV))
				} else {
					lines = append(lines, fmt.Sprintf("  ~ %s %s -> %s", n, oldV, newV))
				}
			case hasNew:
				lines = append(lines, fmt.Sprintf("  + %s %s (added)", n, newV))
			default:
				lines = append(lines, fmt.Sprintf("  - %s %s (removed)", n, oldV))
			}
		}
		if len(lines) == 0 {
			lines = append(lines, "  (manifest touched, no parseable dependency entries changed)")
		}
		sections = append(sections, fmt.Sprintf("  %s:", f.path))
		sections = append(sections, lines...)
	}
	if len(sections) == 0 {
		return ""
	}
	return "DEPENDENCIES (dependency manifest changes, verify versions are intentional):\n" +
		strings.Join(sections, "\n")
}
