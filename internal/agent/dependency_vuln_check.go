package agent

// Dependency Vulnerability Check (SCA - Software Composition Analysis)
//
// Research basis: SCA is a top-tier DevSecOps capability that Claude Code,
// Cursor, Aider, Cline/OpenHands, and Windsurf all lack as a write-time
// feature. Dependabot, Snyk, and npm audit exist as separate CI/external
// tools, but no AI coding agent proactively warns when it introduces a
// dependency with a known critical vulnerability.
//
// Unlike:
//   - insecure_pattern_check.go: detects unsafe CODE patterns (TLS bypass,
//     SQL injection, etc.) in source files
//   - hardcoded_secret_check.go: detects hardcoded credentials
//
// THIS check focuses on the supply chain: when the agent edits a dependency
// manifest file (go.mod, package.json, requirements.txt, Cargo.toml), it:
//   1. Parses dependencies from old and new content
//   2. Identifies ADDED or CHANGED dependencies (delta-aware)
//   3. Checks each against an embedded database of well-known critical CVEs
//   4. Flags any match with CVE ID, severity, and remediation advice
//
// The embedded database covers only the most critical, widely-known
// vulnerabilities across Go, Node.js, Python, and Rust ecosystems.
// It is intentionally small and manually curated for accuracy.

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// vulnEntry represents a known vulnerable package version range.
type vulnEntry struct {
	ecosystem   string // "go", "npm", "pypi", "cargo"
	pkg         string // package name (normalized: lowercase)
	maxSafeV    string // first PATCHED version (anything < this is vulnerable)
	cve         string // CVE or advisory ID
	severity    string // "Critical", "High", "Medium"
	description string // brief vulnerability description
}

// knownVulns is a curated database of the most critical, widely-known
// dependency vulnerabilities. Covers Go modules, npm packages, PyPI
// packages, and Rust crates. For comprehensive scanning, run the
// ecosystem-specific scanner (govulncheck, npm audit, pip-audit, cargo audit).
var knownVulns = []vulnEntry{
	// --- Go modules ---
	{ecosystem: "go", pkg: "golang.org/x/crypto", maxSafeV: "0.31.0", cve: "CVE-2024-45337", severity: "Critical", description: "SSH server authorization bypass via crafted public key"},
	{ecosystem: "go", pkg: "golang.org/x/crypto", maxSafeV: "0.17.0", cve: "CVE-2023-48795", severity: "Medium", description: "Terrapin SSH attack (prefix truncation)"},
	{ecosystem: "go", pkg: "golang.org/x/net", maxSafeV: "0.23.0", cve: "CVE-2024-45338", severity: "High", description: "DoS via crafted HTML in x/net/html"},
	{ecosystem: "go", pkg: "golang.org/x/net", maxSafeV: "0.7.0", cve: "CVE-2022-27664", severity: "High", description: "HTTP/2 server DoS via crafted requests"},
	{ecosystem: "go", pkg: "golang.org/x/text", maxSafeV: "0.3.8", cve: "CVE-2022-32149", severity: "High", description: "DoS in language tag parser"},
	{ecosystem: "go", pkg: "github.com/gin-gonic/gin", maxSafeV: "1.9.1", cve: "CVE-2023-29401", severity: "High", description: "File upload Content-Type validation bypass"},
	{ecosystem: "go", pkg: "github.com/golang-jwt/jwt", maxSafeV: "3.2.2", cve: "CVE-2020-26160", severity: "High", description: "JWT aud claim parsing allows auth bypass"},
	{ecosystem: "go", pkg: "github.com/dgrijalva/jwt-go", maxSafeV: "999.0.0", cve: "CVE-2020-26160", severity: "High", description: "Package DEPRECATED - switch to golang-jwt/jwt/v4"},

	// --- npm packages ---
	{ecosystem: "npm", pkg: "lodash", maxSafeV: "4.17.21", cve: "CVE-2021-23337", severity: "High", description: "Command injection via template"},
	{ecosystem: "npm", pkg: "minimist", maxSafeV: "1.2.6", cve: "CVE-2021-44906", severity: "Critical", description: "Prototype pollution leading to RCE"},
	{ecosystem: "npm", pkg: "handlebars", maxSafeV: "4.7.7", cve: "CVE-2021-23369", severity: "High", description: "Remote code execution via crafted template"},
	{ecosystem: "npm", pkg: "marked", maxSafeV: "4.0.10", cve: "CVE-2021-21365", severity: "High", description: "ReDoS via crafted markdown"},
	{ecosystem: "npm", pkg: "node-forge", maxSafeV: "1.3.0", cve: "CVE-2022-24772", severity: "High", description: "Prototype pollution in forge.util"},
	{ecosystem: "npm", pkg: "qs", maxSafeV: "6.11.0", cve: "CVE-2022-24999", severity: "High", description: "Prototype pollution via __proto__"},
	{ecosystem: "npm", pkg: "ws", maxSafeV: "8.11.0", cve: "CVE-2022-37809", severity: "Medium", description: "DoS via overly large HTTP headers"},
	{ecosystem: "npm", pkg: "event-stream", maxSafeV: "999.0.0", cve: "CVE-2018-1000620", severity: "Critical", description: "Package was compromised with malware (flatmap-stream) - remove immediately"},
	{ecosystem: "npm", pkg: "moment", maxSafeV: "2.29.4", cve: "CVE-2022-31129", severity: "High", description: "ReDoS via crafted date string"},
	{ecosystem: "npm", pkg: "axios", maxSafeV: "0.21.2", cve: "CVE-2021-3749", severity: "High", description: "ReDoS via trimmed whitespace"},
	{ecosystem: "npm", pkg: "jsonwebtoken", maxSafeV: "9.0.0", cve: "CVE-2022-23529", severity: "Critical", description: "Auth bypass via crafted key header"},

	// --- PyPI packages ---
	{ecosystem: "pypi", pkg: "urllib3", maxSafeV: "1.26.18", cve: "CVE-2023-45803", severity: "Medium", description: "Cookie leak on cross-origin redirect"},
	{ecosystem: "pypi", pkg: "requests", maxSafeV: "2.32.0", cve: "CVE-2024-35195", severity: "Medium", description: "verify=False bypassed on redirected requests"},
	{ecosystem: "pypi", pkg: "pyyaml", maxSafeV: "5.4.0", cve: "CVE-2020-1747", severity: "Critical", description: "Arbitrary code execution via yaml.load"},
	{ecosystem: "pypi", pkg: "cryptography", maxSafeV: "42.0.4", cve: "CVE-2024-26130", severity: "High", description: "NULL pointer dereference in PKCS#12"},
	{ecosystem: "pypi", pkg: "jinja2", maxSafeV: "3.1.3", cve: "CVE-2024-22195", severity: "Medium", description: "XSS via xmlattr filter"},
	{ecosystem: "pypi", pkg: "django", maxSafeV: "4.2.13", cve: "CVE-2024-39614", severity: "High", description: "DoS in gettext log stripping"},
	{ecosystem: "pypi", pkg: "pillow", maxSafeV: "10.3.0", cve: "CVE-2024-28219", severity: "High", description: "Buffer overflow in _imagingcms.c"},
	{ecosystem: "pypi", pkg: "aiohttp", maxSafeV: "3.9.4", cve: "CVE-2024-23334", severity: "High", description: "Path traversal in static file serving"},

	// --- Rust crates ---
	{ecosystem: "cargo", pkg: "openssl", maxSafeV: "0.10.41", cve: "CVE-2021-3711", severity: "High", description: "SM2 Decryption Buffer Overflow (inherited from OpenSSL)"},
}

// depVulnFiles maps manifest file names to ecosystem identifiers.
var depVulnFiles = map[string]string{
	"go.mod":           "go",
	"package.json":     "npm",
	"requirements.txt": "pypi",
	"Cargo.toml":       "cargo",
	"Pipfile":          "pypi",
	"pyproject.toml":   "pypi",
}

// checkDependencyVulnsAsString wraps the slice-returning check for the
// registry's stringCheck adapter.
func checkDependencyVulnsAsString(filePath, oldContent, newContent string) string {
	warnings := checkDependencyVulns(filePath, oldContent, newContent)
	if len(warnings) == 0 {
		return ""
	}
	return strings.Join(warnings, "\n")
}

// checkDependencyVulns detects when a dependency manifest is modified and
// checks added/changed dependencies against the embedded vulnerability database.
// Delta-aware: only flags dependencies that are NEW or CHANGED in this edit.
func checkDependencyVulns(filePath, oldContent, newContent string) []string {
	if strings.TrimSpace(newContent) == "" {
		return nil
	}

	base := filepath.Base(filePath)
	ecosystem, ok := depVulnFiles[base]
	if !ok {
		return nil
	}

	oldDeps := parseDependencies(ecosystem, oldContent)
	newDeps := parseDependencies(ecosystem, newContent)

	// Find added or changed dependencies.
	var changes []depChange
	for name, newVer := range newDeps {
		oldVer, existed := oldDeps[name]
		if !existed {
			changes = append(changes, depChange{name: name, version: newVer, action: "added"})
		} else if oldVer != newVer {
			changes = append(changes, depChange{name: name, version: newVer, action: "changed"})
		}
	}

	if len(changes) == 0 {
		return nil
	}

	// Check each changed dependency against the vulnerability database.
	var warnings []string
	matchedPkgs := make(map[string]bool)

	for _, ch := range changes {
		normalized := strings.ToLower(ch.name)
		for _, vuln := range knownVulns {
			if vuln.ecosystem != ecosystem || vuln.pkg != normalized {
				continue
			}
			if matchedPkgs[normalized] {
				continue
			}
			if isVulnerableVersion(ch.version, vuln) {
				matchedPkgs[normalized] = true
				warnings = append(warnings, fmt.Sprintf(
					"[Dependency Vulnerability] %s %s@%s has %s severity issue (%s): %s. Upgrade to %s+.",
					ch.action, ch.name, ch.version, vuln.severity, vuln.cve, vuln.description, vuln.maxSafeV,
				))
			}
		}
	}

	// If no known vulns matched but dependencies changed, remind about scanning.
	if len(warnings) == 0 {
		warnings = append(warnings, fmt.Sprintf(
			"[Dependency Change] %d dependency(ies) %s in %s - consider running %s to check for known vulnerabilities.",
			len(changes), changeAction(changes), base, scannerName(ecosystem),
		))
	}

	return warnings
}

type depChange struct {
	name    string
	version string
	action  string
}

func changeAction(changes []depChange) string {
	allAdded := true
	allChanged := true
	for _, c := range changes {
		if c.action != "added" {
			allAdded = false
		}
		if c.action != "changed" {
			allChanged = false
		}
	}
	if allAdded {
		return "added"
	}
	if allChanged {
		return "changed"
	}
	return "modified"
}

func scannerName(ecosystem string) string {
	switch ecosystem {
	case "go":
		return "`govulncheck ./...`"
	case "npm":
		return "`npm audit`"
	case "pypi":
		return "`pip-audit`"
	case "cargo":
		return "`cargo audit`"
	}
	return "a vulnerability scanner"
}

// parseDependencies extracts package name→version from manifest content.
func parseDependencies(ecosystem, content string) map[string]string {
	switch ecosystem {
	case "go":
		return parseGoMod(content)
	case "npm":
		return parsePackageJSON(content)
	case "pypi":
		return parseRequirements(content)
	case "cargo":
		return parseCargoToml(content)
	}
	return nil
}

// --- Go: go.mod parser ---
var goModRequireRe = regexp.MustCompile(`^\s*(\S+)\s+(v[\d.]+(?:-\S+)?)`)

func parseGoMod(content string) map[string]string {
	deps := make(map[string]string)
	inRequireBlock := false
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "require (") || strings.HasPrefix(trimmed, "require(") {
			inRequireBlock = true
			continue
		}
		if trimmed == ")" && inRequireBlock {
			inRequireBlock = false
			continue
		}
		if !inRequireBlock && strings.HasPrefix(trimmed, "require ") {
			m := goModRequireRe.FindStringSubmatch(strings.TrimPrefix(trimmed, "require "))
			if m != nil {
				deps[strings.ToLower(m[1])] = m[2]
			}
			continue
		}
		if inRequireBlock {
			m := goModRequireRe.FindStringSubmatch(line)
			if m != nil {
				deps[strings.ToLower(m[1])] = m[2]
			}
		}
	}
	return deps
}

// --- npm: package.json parser ---
func parsePackageJSON(content string) map[string]string {
	deps := make(map[string]string)
	re := regexp.MustCompile(`"([^"]+)"\s*:\s*"([^"]+)"`)
	lines := strings.Split(content, "\n")
	inDeps := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.Contains(trimmed, `"dependencies"`) || strings.Contains(trimmed, `"devDependencies"`) {
			inDeps = true
			continue
		}
		if inDeps && trimmed == "}" {
			inDeps = false
			continue
		}
		if inDeps {
			m := re.FindStringSubmatch(line)
			if m != nil && !strings.HasPrefix(m[1], "//") {
				version := strings.TrimLeft(m[2], "^~>=< ")
				if version != "" {
					deps[strings.ToLower(m[1])] = version
				}
			}
		}
	}
	return deps
}

// --- PyPI: requirements.txt parser ---
var requirementsLineRe = regexp.MustCompile(`^([a-zA-Z0-9_.-]+)\s*(==|>=|~=|>|<)\s*([0-9][\w.!?+*-]*)`)

func parseRequirements(content string) map[string]string {
	deps := make(map[string]string)
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "-") {
			continue
		}
		m := requirementsLineRe.FindStringSubmatch(line)
		if m != nil {
			deps[strings.ToLower(m[1])] = m[3]
		}
	}
	return deps
}

// --- Rust: Cargo.toml parser ---
var cargoDepRe = regexp.MustCompile(`^\s*([a-zA-Z0-9_-]+)\s*=\s*"([\d.]+)"`)

func parseCargoToml(content string) map[string]string {
	deps := make(map[string]string)
	inDepsSection := false
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[dependencies") {
			inDepsSection = true
			continue
		}
		if inDepsSection && strings.HasPrefix(trimmed, "[") {
			inDepsSection = false
			continue
		}
		if inDepsSection {
			m := cargoDepRe.FindStringSubmatch(line)
			if m != nil {
				deps[strings.ToLower(m[1])] = m[2]
			}
		}
	}
	return deps
}

// isVulnerableVersion checks if a version string falls below the first
// patched version (maxSafeV). Anything strictly less than maxSafeV is vulnerable.
func isVulnerableVersion(version string, vuln vulnEntry) bool {
	cleanVer := stripVersionPrefix(version)
	return compareVersions(cleanVer, vuln.maxSafeV) < 0
}

// stripVersionPrefix removes "v" prefix, commit hashes, and other decorations.
func stripVersionPrefix(version string) string {
	version = strings.TrimPrefix(version, "v")
	if idx := strings.Index(version, "-"); idx > 0 {
		version = version[:idx]
	}
	return version
}

// compareVersions compares two semver-like version strings.
// Returns -1 if a < b, 0 if a == b, 1 if a > b.
func compareVersions(a, b string) int {
	partsA := strings.Split(a, ".")
	partsB := strings.Split(b, ".")

	maxLen := len(partsA)
	if len(partsB) > maxLen {
		maxLen = len(partsB)
	}

	for i := 0; i < maxLen; i++ {
		na, nb := 0, 0
		if i < len(partsA) {
			na = parseVersionPart(partsA[i])
		}
		if i < len(partsB) {
			nb = parseVersionPart(partsB[i])
		}
		if na < nb {
			return -1
		}
		if na > nb {
			return 1
		}
	}
	return 0
}

// parseVersionPart extracts the numeric portion of a version component.
func parseVersionPart(s string) int {
	numStr := ""
	for _, c := range s {
		if c >= '0' && c <= '9' {
			numStr += string(c)
		} else {
			break
		}
	}
	if numStr == "" {
		return 0
	}
	n, _ := strconv.Atoi(numStr)
	return n
}
