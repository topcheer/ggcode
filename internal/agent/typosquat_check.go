package agent

import (
	"fmt"
	"path/filepath"
	"strings"
)

var wellKnownPackages = map[string][]string{
	"go": {

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

const maxTyposquatDistance = 2

const minPackageLenForCheck = 4

const maxTyposquatWarnings = 3

func checkTyposquattingAsString(filePath, oldContent, newContent string) string {
	warnings := checkTyposquatting(filePath, oldContent, newContent)
	if len(warnings) == 0 {
		return ""
	}
	return strings.Join(warnings, "\n")
}

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

		pkgName := extractPackageName(ecosystem, pkg)
		if len(pkgName) < minPackageLenForCheck {
			continue
		}

		for _, known := range known {
			if pkgName == known {
				continue
			}
			dist := levenshtein(pkgName, known)
			if dist > 0 && dist <= maxTyposquatDistance {
				warnings = append(warnings, fmt.Sprintf(
					"[Supply Chain Alert] Package %q closely resembles well-known package %q "+
						"(edit distance %d). This is a common typosquatting pattern - verify this is the "+
						"INTENDED package and not a malicious lookalike. Check the package registry for "+
						"download counts, maintainer reputation, and recent publish history before proceeding.",
					pkg, known, dist))
				break
			}
		}

		if len(warnings) >= maxTyposquatWarnings {
			break
		}
	}

	return warnings
}

func extractPackageName(ecosystem, depPath string) string {
	if ecosystem == "go" {

		cleaned := strings.TrimSpace(depPath)

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

func levenshtein(a, b string) int {
	la := len(a)
	lb := len(b)

	if la == 0 {
		return lb
	}
	if lb == 0 {
		return la
	}

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
