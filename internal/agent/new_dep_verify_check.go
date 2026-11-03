package agent

import (
	"fmt"
	"path/filepath"
	"strings"
)

const maxNewDepNoticePackages = 5

func isVCSModulePath(modulePath string) bool {
	first := modulePath
	if i := strings.Index(modulePath, "/"); i >= 0 {
		first = modulePath[:i]
	}
	return strings.Contains(first, ".")
}

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

		if ecosystem == "go" && isVCSModulePath(name) {
			continue
		}

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

func checkNewDependencyVerifyAsString(a, b, c string) string {
	w := checkNewDependencyVerify(a, b, c)
	if len(w) == 0 {
		return ""
	}
	return w[0]
}
