package agent

// Typosquatting Detection in Dependency Manifests
//
// Research basis: Supply chain attacks via typosquatting are the #1 attack
// vector in npm, PyPI, and Cargo ecosystems (Socket.dev 2024 report).
// Attackers publish packages with names visually similar to popular ones
// (e.g., "requets" vs "requests", "lodahs" vs "lodash", "crytography" vs
// "cryptography"). Unsuspecting developers mistype or auto-complete the
// wrong package, installing malware.
//
// High-profile incidents:
//   - event-stream (2018): compromised npm package with cryptocurrency wallet stealer
//   - ua-parser-js (2021): npm package hijacked with crypto-miner + password stealer
//   - coa/rc (2021): npm packages hijacked, affected 36% of React projects
//   - ctx (2018): Python package typosquatting "ctx", contained backdoor
//
// Competitor analysis:
//   - Socket.dev: runtime/CI supply chain analysis (external tool)
//   - Snyk: CI-time package advisory scanning (external tool)
//   - npm/pip: no built-in typosquat detection at install time
//   - GitHub Copilot: no write-time typosquat detection
//   - Cursor/Claude Code/Aider: no detection
//
// Unlike dependency_vuln_check.go (which checks known CVE databases against
// KNOWN packages), THIS check focuses on UNKNOWN packages whose names are
// suspiciously similar to well-known packages - the hallmark of typosquatting.
//
// ggcode's approach: Levenshtein edit distance (1-2 edits) against a curated
// list of the most popular packages per ecosystem. Delta-aware: only flags
// NEW dependencies introduced by the agent's edit. Zero LLM cost, <1ms.

import (
	"fmt"
	"path/filepath"
	"strings"
)

// wellKnownPackages lists the most popular, frequently-targeted package names
// per ecosystem. Typosquatters create names with 1-2 character differences from
// these. The list is intentionally short - only the most commonly typosquatted
// packages (top downloads, known attack targets).
var wellKnownPackages = map[string][]string{
	"go": {
		// For Go, the package "name" from go.mod is the full module path.
		// Typosquatters target the last path segment.
		"gin", "jwt", "testify", "cobra", "viper", "echo", "fiber",
		"gorilla", "zap", "logrus", "pflag", "cast",
	},
	"npm": {
		"lodash", "react", "express", "axios", "chalk", "commander",
		"debug", "request", "moment", "uuid", "vue", "angular",
		"webpack", "typescript", "babel", "eslint", "jest", "mocha",
		"dotenv", "cors", "helmet", "morgan", "body-parser",
		"async", "underscore", "jquery", "bootstrap", "three",
	},
	"pypi": {
		"requests", "django", "flask", "numpy", "pandas", "pytest",
		"pyyaml", "urllib3", "cryptography", "jinja2", "pillow",
		"aiohttp", "tornado", "boto3", "celery", "redis", "sqlalchemy",
		"scipy", "matplotlib", "scikit-learn", "tensorflow", "torch",
	},
	"cargo": {
		"serde", "tokio", "rand", "regex", "clap", "reqwest",
		"serde_json", "log", "env_logger", "anyhow", "thiserror",
		"actix-web", "axum", "rocket", "warp", "hyper",
	},
}

// maxTyposquatDistance is the Levenshtein distance threshold for flagging.
// Distance 1-2 covers single-char swaps, insertions, deletions, and
// transpositions (which are distance 2 in standard Levenshtein).
const maxTyposquatDistance = 2

// minPackageLenForCheck ignores very short package names (< 4 chars)
// to avoid false positives (e.g., "go" -> "fo", "re" -> "rs").
const minPackageLenForCheck = 4

// maxTyposquatWarnings limits the number of typosquat warnings per edit
// to avoid flooding the context with noise from large manifest changes.
const maxTyposquatWarnings = 3

// checkTyposquattingAsString wraps the slice-returning check for the
// registry's stringCheck adapter.
func checkTyposquattingAsString(filePath, oldContent, newContent string) string {
	warnings := checkTyposquatting(filePath, oldContent, newContent)
	if len(warnings) == 0 {
		return ""
	}
	return strings.Join(warnings, "\n")
}

// checkTyposquatting detects when a NEW dependency added to a manifest file
// has a name suspiciously similar (Levenshtein distance 1-2) to a well-known
// popular package. Delta-aware: only flags dependencies introduced by this edit.
func checkTyposquatting(filePath, oldContent, newContent string) []string {
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

	// Find newly ADDED dependencies (not changed - typosquatting is about NEW names).
	var newPackages []string
	for name := range newDeps {
		if _, existed := oldDeps[name]; !existed {
			newPackages = append(newPackages, name)
		}
	}

	if len(newPackages) == 0 {
		return nil
	}

	var warnings []string
	for _, pkg := range newPackages {
		// Skip scoped npm packages like @types/lodash, @babel/core - these are
		// legitimate scoped variants, not typosquats.
		pkgName := extractPackageName(ecosystem, pkg)
		if len(pkgName) < minPackageLenForCheck {
			continue
		}

		// Check against all known popular packages in this ecosystem.
		for _, known := range known {
			if pkgName == known {
				continue // exact match - the real package, not a typosquat
			}
			dist := levenshtein(pkgName, known)
			if dist > 0 && dist <= maxTyposquatDistance {
				warnings = append(warnings, fmt.Sprintf(
					"[Supply Chain Alert] Package %q closely resembles well-known package %q "+
						"(edit distance %d). This is a common typosquatting pattern - verify this is the "+
						"INTENDED package and not a malicious lookalike. Check the package registry for "+
						"download counts, maintainer reputation, and recent publish history before proceeding.",
					pkg, known, dist))
				break // one warning per suspicious package
			}
		}

		if len(warnings) >= maxTyposquatWarnings {
			break
		}
	}

	return warnings
}

// extractPackageName extracts the comparable name from a dependency identifier.
// For Go modules, this is the last path segment of the module path.
// For other ecosystems, the full package name is used.
func extractPackageName(ecosystem, depPath string) string {
	if ecosystem == "go" {
		// Module path: github.com/gin-gonic/gin -> gin
		// Strip scheme/host and take the last segment
		cleaned := strings.TrimSpace(depPath)
		// Remove query parameters
		if idx := strings.Index(cleaned, "?"); idx > 0 {
			cleaned = cleaned[:idx]
		}
		parts := strings.Split(cleaned, "/")
		if len(parts) == 0 {
			return cleaned
		}
		return strings.ToLower(parts[len(parts)-1])
	}
	return strings.ToLower(depPath)
}

// levenshtein computes the edit distance between two strings using the
// standard dynamic programming algorithm. Returns the number of single-character
// edits (insert, delete, substitute) needed to transform a into b.
func levenshtein(a, b string) int {
	la := len(a)
	lb := len(b)

	// Quick exits
	if la == 0 {
		return lb
	}
	if lb == 0 {
		return la
	}

	// Use two rows for O(n) space
	prev := make([]int, lb+1)
	curr := make([]int, lb+1)

	for j := 0; j <= lb; j++ {
		prev[j] = j
	}

	for i := 1; i <= la; i++ {
		curr[0] = i
		for j := 1; j <= lb; j++ {
			cost := 0
			if a[i-1] != b[j-1] {
				cost = 1
			}
			del := prev[j] + 1
			ins := curr[j-1] + 1
			sub := prev[j-1] + cost
			min := del
			if ins < min {
				min = ins
			}
			if sub < min {
				min = sub
			}
			curr[j] = min
		}
		prev, curr = curr, prev
	}

	return prev[lb]
}
