package agent

import (
	"fmt"
	"path/filepath"
	"strings"
)

// New-dependency verification notice (r356; slopsquatting defense, 2026
// supply-chain pattern): AI agents hallucinate package names at measurable
// rates, and attackers register those names. The typosquat check only
// fires for names CLOSE to a well-known package; a fully hallucinated
// arbitrary name ("react-pro-memo-hooks") sails through silently. Per CSA
// 2026-04 guidance, an agent that adds a dependency should surface a
// verification step, not skip it.
//
// This check is the deterministic half: whenever a dependency manifest
// gains NEW packages that are not exact well-known names, emit one
// aggregated notice telling the agent (and user) to confirm registry
// existence before trusting the dependency. Zero LLM cost, no network.

// maxNewDepNoticePackages bounds how many package names are listed.
const maxNewDepNoticePackages = 5

// isVCSModulePath reports whether a Go module path starts with a
// dotted-domain first segment (github.com/..., gopkg.in/...) - i.e. it
// resolves to a real VCS URL rather than a registry-style short name.
func isVCSModulePath(modulePath string) bool {
	first := modulePath
	if i := strings.Index(modulePath, "/"); i >= 0 {
		first = modulePath[:i]
	}
	return strings.Contains(first, ".")
}

// checkNewDependencyVerify returns one aggregated notice for newly added
// dependencies that are neither exact well-known packages (those are
// near-certainly real) nor typosquat-suspect (covered by checkTyposquatting
// with its own stronger wording).
func checkNewDependencyVerify(filePath, oldContent, newContent string) []string {
	if strings.TrimSpace(newContent) == "" {
		return nil
	}
	base := filepath.Base(filePath)
	ecosystem, ok := depVulnFiles[base]
	if !ok {
		return nil
	}
	known, hasKnown := wellKnownPackages[ecosystem]
	if !hasKnown {
		return nil
	}

	oldDeps := parseDependencies(ecosystem, oldContent)
	newDeps := parseDependencies(ecosystem, newContent)

	var fresh []string
	for name := range newDeps {
		if _, existed := oldDeps[name]; existed {
			continue
		}
		pkgName := extractPackageName(ecosystem, name)
		if len(pkgName) < minPackageLenForCheck {
			continue
		}
		// #3045 B2: Go module paths with a dotted-domain first segment
		// (github.com/..., gopkg.in/...) are VCS-resolvable - the name maps
		// to a real git URL, so there is no registry-squatting surface the
		// way npm/pypi short names have. Only registry-style short names
		// carry the slopsquatting risk this check targets.
		if ecosystem == "go" && isVCSModulePath(name) {
			continue
		}
		// Exact well-known match: the real, extremely popular package -
		// existence is not in doubt; stay silent to keep noise low.
		isKnown := false
		for _, k := range known {
			if pkgName == k {
				isKnown = true
				break
			}
		}
		if !isKnown {
			fresh = append(fresh, name)
		}
	}
	if len(fresh) == 0 {
		return nil
	}
	if len(fresh) > maxNewDepNoticePackages {
		fresh = append(fresh[:maxNewDepNoticePackages], fmt.Sprintf("+%d more", len(fresh)-maxNewDepNoticePackages))
	}
	return []string{fmt.Sprintf(
		"[Dependency Verification] New dependencies added to %s: %s. "+
			"Confirm each name exists in the package registry before proceeding - "+
			"hallucinated package names that attackers register (slopsquatting) are a "+
			"known AI-agent supply-chain attack vector. Check download counts and "+
			"publish date; an unregistered or brand-new name is a red flag.",
		base, strings.Join(fresh, ", "))}
}

// checkNewDependencyVerifyAsString adapts the check for the string-check
// pipeline (single aggregated message).
func checkNewDependencyVerifyAsString(a, b, c string) string {
	w := checkNewDependencyVerify(a, b, c)
	if len(w) == 0 {
		return ""
	}
	return w[0]
}
