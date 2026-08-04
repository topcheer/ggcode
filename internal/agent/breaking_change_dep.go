package agent

// Major Version Bump Breaking Change Detection for Dependencies
//
// Problem: AI agents frequently upgrade dependencies without considering
// breaking changes introduced by major version bumps. Under Semantic
// Versioning, a major version change (v1.x -> v2.x) signals incompatible
// API changes. Agents often blindly bump versions and don't realize:
//   - Go modules: v2+ requires import path change (/v2 suffix)
//   - React 17->18: ReactDOM.render -> createRoot
//   - Express 3->4: middleware/app restructuring
//   - Django 3->4: async views, removed deprecated settings
//
// Competitor analysis:
//   - Claude Code: no detection - agent may bump deps blindly
//   - Cursor: no detection - relies on linter/type-checker post-hoc
//   - Cline/OpenHands: reactive only - build errors surface later
//   - Aider: no detection
//   - Dependabot: creates PRs with changelog links, but not write-time
//
// Approach: delta-aware check on dependency manifest files. When a
// dependency's MAJOR version increases, warn about likely breaking
// changes. For well-known packages, provide specific migration guidance.
// Reuses parsing and version comparison from dependency_vuln_check.go.

import (
	"fmt"
	"path/filepath"
	"strings"
)

// migrationGuide provides package-specific guidance for known major bumps.
type migrationGuide struct {
	ecosystem string
	pkg       string // normalized lowercase
	fromMajor int    // source major version
	toMajor   int    // target major version
	guidance  string // migration instructions
}

// knownMigrations is a curated database of high-impact major version
// migrations with specific, actionable guidance.
var knownMigrations = []migrationGuide{
	// --- Go ---
	{ecosystem: "go", pkg: "github.com/gin-gonic/gin", fromMajor: 1, toMajor: 2, guidance: "Gin v2 changes middleware behavior and context API. Review the migration guide at https://github.com/gin-gonic/gin/releases."},
	{ecosystem: "go", pkg: "github.com/golang-jwt/jwt", fromMajor: 3, toMajor: 4, guidance: "jwt v4 introduces typed claims and changed error handling. Update token parsing and claims access."},
	{ecosystem: "go", pkg: "github.com/spf13/viper", fromMajor: 0, toMajor: 1, guidance: "Viper v1 stabilized the API. Some experimental methods were renamed."},

	// --- npm ---
	{ecosystem: "npm", pkg: "react", fromMajor: 17, toMajor: 18, guidance: "React 18: ReactDOM.render is deprecated. Use createRoot from 'react-dom/client'. Automatic batching changes state update behavior."},
	{ecosystem: "npm", pkg: "react", fromMajor: 18, toMajor: 19, guidance: "React 19: Actions, use() hook, and removed legacy APIs. Review https://react.dev/blog."},
	{ecosystem: "npm", pkg: "next", fromMajor: 12, toMajor: 13, guidance: "Next.js 13: App Router introduced. 'next/image' and 'next/link' APIs changed. Review migration guide."},
	{ecosystem: "npm", pkg: "next", fromMajor: 13, toMajor: 14, guidance: "Next.js 14: Partial Prerendering, updated caching semantics. Review changelog."},
	{ecosystem: "npm", pkg: "express", fromMajor: 3, toMajor: 4, guidance: "Express 4: middleware restructured, app.router removed. Migrate custom middleware."},
	{ecosystem: "npm", pkg: "typescript", fromMajor: 4, toMajor: 5, guidance: "TypeScript 5: decorators rewritten, stricter type inference. Run tsc and review new errors."},
	{ecosystem: "npm", pkg: "vite", fromMajor: 4, toMajor: 5, guidance: "Vite 5: Node 18+ required, CJS Node API deprecated. Review migration guide."},
	{ecosystem: "npm", pkg: "tailwindcss", fromMajor: 2, toMajor: 3, guidance: "Tailwind 3: JIT by default, config changes, some class names renamed."},
	{ecosystem: "npm", pkg: "vue", fromMajor: 2, toMajor: 3, guidance: "Vue 3: Composition API, new reactivity system. Global API changed (createApp). Review https://v3-migration.vuejs.org."},

	// --- Python ---
	{ecosystem: "pypi", pkg: "django", fromMajor: 3, toMajor: 4, guidance: "Django 4: async views, RE_DEFAULT_URL pattern changes, removed deprecated settings. Review release notes."},
	{ecosystem: "pypi", pkg: "django", fromMajor: 4, toMajor: 5, guidance: "Django 5: form rendering changes, removed default_app_config. Python 3.10+ required."},
	{ecosystem: "pypi", pkg: "flask", fromMajor: 1, toMajor: 2, guidance: "Flask 2: async route handlers, removed deprecated APIs. Python 3.7+ required."},
	{ecosystem: "pypi", pkg: "fastapi", fromMajor: 0, toMajor: 1, guidance: "FastAPI 1.0: stabilized API surface. Review breaking changes in release notes."},
	{ecosystem: "pypi", pkg: "pydantic", fromMajor: 1, toMajor: 2, guidance: "Pydantic 2: complete rewrite in Rust, breaking API changes. Use bump-pydantic migration tool. Review https://docs.pydantic.dev/2.0/migration/."},

	// --- Rust ---
	{ecosystem: "cargo", pkg: "tokio", fromMajor: 0, toMajor: 1, guidance: "Tokio 1.0: stabilized API. Some runtime and task APIs changed."},
	{ecosystem: "cargo", pkg: "actix-web", fromMajor: 3, toMajor: 4, guidance: "Actix Web 4: middleware trait changes, App::data renamed. Review migration guide."},
}

// checkBreakingChangeDepAsString wraps the slice-returning check for the
// registry's stringCheck adapter.
func checkBreakingChangeDepAsString(filePath, oldContent, newContent string) string {
	warnings := checkBreakingChangeDep(filePath, oldContent, newContent)
	if len(warnings) == 0 {
		return ""
	}
	return strings.Join(warnings, "\n")
}

// checkBreakingChangeDep detects major version bumps in dependency manifests
// and warns about potential breaking changes with migration guidance.
func checkBreakingChangeDep(filePath, oldContent, newContent string) []string {
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

	var warnings []string
	for depName, depVer := range newDeps {
		oldVer, existed := oldDeps[depName]
		if !existed {
			continue // newly added deps handled by vuln check
		}

		oldMajor := extractMajorVersion(oldVer)
		newMajor := extractMajorVersion(depVer)
		if oldMajor < 0 || newMajor < 0 {
			continue
		}
		if newMajor <= oldMajor {
			continue // downgrade or same major - safe under SemVer
		}

		// Major version bump detected.
		normalized := strings.ToLower(depName)
		guidance := findMigrationGuide(ecosystem, normalized, oldMajor, newMajor)
		if guidance == "" {
			// Generic guidance for unknown packages.
			guidance = genericMajorBumpGuidance(ecosystem, oldMajor, newMajor)
		}

		warnings = append(warnings, fmt.Sprintf(
			"[Major Version Bump] %s upgraded v%d -> v%d in %s. Breaking changes likely under SemVer. %s",
			depName, oldMajor, newMajor, base, guidance,
		))
	}

	return warnings
}

// extractMajorVersion extracts the major version number from a version string.
// Returns -1 if the version cannot be parsed.
func extractMajorVersion(version string) int {
	clean := stripVersionPrefix(version)
	// Strip semver constraint prefixes: >=, >, <=, <, ~=, ==, ^
	for _, constraintPre := range []string{">=", "<=", "~=", "==", ">", "<", "^", "~"} {
		clean = strings.TrimPrefix(clean, constraintPre)
	}
	clean = strings.TrimSpace(clean)
	clean = strings.SplitN(clean, ".", 2)[0]
	if clean == "" || clean[0] < '0' || clean[0] > '9' {
		return -1
	}
	return parseVersionPart(clean)
}

// findMigrationGuide looks up package-specific migration guidance.
func findMigrationGuide(ecosystem, pkg string, fromMajor, toMajor int) string {
	for _, mg := range knownMigrations {
		if mg.ecosystem == ecosystem && mg.pkg == pkg &&
			mg.fromMajor == fromMajor && mg.toMajor == toMajor {
			return mg.guidance
		}
	}
	return ""
}

// genericMajorBumpGuidance provides ecosystem-specific generic advice.
func genericMajorBumpGuidance(ecosystem string, fromMajor, toMajor int) string {
	switch ecosystem {
	case "go":
		return fmt.Sprintf("Go modules: v%d+ requires import path suffix (e.g., /v%d). Update all import statements.", toMajor, toMajor)
	case "npm":
		return "Review the package's CHANGELOG and migration guide. Test all affected code paths."
	case "pypi":
		return "Review release notes. Run your test suite to catch API changes."
	case "cargo":
		return "Review CHANGELOG.md. Run `cargo check` and `cargo test` to catch breakage."
	}
	return "Review the changelog and migration guide."
}
