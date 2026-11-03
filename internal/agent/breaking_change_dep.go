package agent

import (
	"fmt"
	"path/filepath"
	"strings"
)

type migrationGuide struct {
	ecosystem string
	pkg       string
	fromMajor int
	toMajor   int
	guidance  string
}

var knownMigrations = []migrationGuide{

	{ecosystem: "go", pkg: "github.com/gin-gonic/gin", fromMajor: 1, toMajor: 2, guidance: "Gin v2 changes middleware behavior and context API. Review the migration guide at https://github.com/gin-gonic/gin/releases."},
	{ecosystem: "go", pkg: "github.com/golang-jwt/jwt", fromMajor: 3, toMajor: 4, guidance: "jwt v4 introduces typed claims and changed error handling. Update token parsing and claims access."},
	{ecosystem: "go", pkg: "github.com/spf13/viper", fromMajor: 0, toMajor: 1, guidance: "Viper v1 stabilized the API. Some experimental methods were renamed."},

	{ecosystem: "npm", pkg: "react", fromMajor: 17, toMajor: 18, guidance: "React 18: ReactDOM.render is deprecated. Use createRoot from 'react-dom/client'. Automatic batching changes state update behavior."},
	{ecosystem: "npm", pkg: "react", fromMajor: 18, toMajor: 19, guidance: "React 19: Actions, use() hook, and removed legacy APIs. Review https://react.dev/blog."},
	{ecosystem: "npm", pkg: "next", fromMajor: 12, toMajor: 13, guidance: "Next.js 13: App Router introduced. 'next/image' and 'next/link' APIs changed. Review migration guide."},
	{ecosystem: "npm", pkg: "next", fromMajor: 13, toMajor: 14, guidance: "Next.js 14: Partial Prerendering, updated caching semantics. Review changelog."},
	{ecosystem: "npm", pkg: "express", fromMajor: 3, toMajor: 4, guidance: "Express 4: middleware restructured, app.router removed. Migrate custom middleware."},
	{ecosystem: "npm", pkg: "typescript", fromMajor: 4, toMajor: 5, guidance: "TypeScript 5: decorators rewritten, stricter type inference. Run tsc and review new errors."},
	{ecosystem: "npm", pkg: "vite", fromMajor: 4, toMajor: 5, guidance: "Vite 5: Node 18+ required, CJS Node API deprecated. Review migration guide."},
	{ecosystem: "npm", pkg: "tailwindcss", fromMajor: 2, toMajor: 3, guidance: "Tailwind 3: JIT by default, config changes, some class names renamed."},
	{ecosystem: "npm", pkg: "vue", fromMajor: 2, toMajor: 3, guidance: "Vue 3: Composition API, new reactivity system. Global API changed (createApp). Review https://v3-migration.vuejs.org."},

	{ecosystem: "pypi", pkg: "django", fromMajor: 3, toMajor: 4, guidance: "Django 4: async views, RE_DEFAULT_URL pattern changes, removed deprecated settings. Review release notes."},
	{ecosystem: "pypi", pkg: "django", fromMajor: 4, toMajor: 5, guidance: "Django 5: form rendering changes, removed default_app_config. Python 3.10+ required."},
	{ecosystem: "pypi", pkg: "flask", fromMajor: 1, toMajor: 2, guidance: "Flask 2: async route handlers, removed deprecated APIs. Python 3.7+ required."},
	{ecosystem: "pypi", pkg: "fastapi", fromMajor: 0, toMajor: 1, guidance: "FastAPI 1.0: stabilized API surface. Review breaking changes in release notes."},
	{ecosystem: "pypi", pkg: "pydantic", fromMajor: 1, toMajor: 2, guidance: "Pydantic 2: complete rewrite in Rust, breaking API changes. Use bump-pydantic migration tool. Review https://docs.pydantic.dev/2.0/migration/."},

	{ecosystem: "cargo", pkg: "tokio", fromMajor: 0, toMajor: 1, guidance: "Tokio 1.0: stabilized API. Some runtime and task APIs changed."},
	{ecosystem: "cargo", pkg: "actix-web", fromMajor: 3, toMajor: 4, guidance: "Actix Web 4: middleware trait changes, App::data renamed. Review migration guide."},
}

func checkBreakingChangeDepAsString(filePath, oldContent, newContent string) string {
	warnings := checkBreakingChangeDep(filePath, oldContent, newContent)
	if len(warnings) == 0 {
		return ""
	}
	return strings.Join(warnings, "\n")
}

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

			if base, suffix := goModuleBase(depName); suffix {
				newMajor := extractMajorVersion(depVer)
				if newMajor >= 2 {

					for m := 1; m < newMajor; m++ {
						prevKey := base
						if m > 1 {
							prevKey = fmt.Sprintf("%s/v%d", base, m)
						}
						prevVer, was := oldDeps[prevKey]
						if !was {
							continue
						}
						oldMajor := extractMajorVersion(prevVer)
						if oldMajor >= 0 && newMajor > oldMajor {
							warnings = append(warnings, fmt.Sprintf(
								"[Major Version Bump] %s upgraded v%d -> v%d (module path changed to %s) in %s. "+
									"Breaking changes likely under SemVer. Update all import paths from %s to %s.",
								depName, oldMajor, newMajor, depName, base, prevKey, depName,
							))
						}
						break
					}
				}
			}
			continue
		}

		oldMajor := extractMajorVersion(oldVer)
		newMajor := extractMajorVersion(depVer)
		if oldMajor < 0 || newMajor < 0 {
			continue
		}
		if newMajor <= oldMajor {
			continue
		}

		normalized := strings.ToLower(depName)
		guidance := findMigrationGuide(ecosystem, normalized, oldMajor, newMajor)
		if guidance == "" {

			guidance = genericMajorBumpGuidance(ecosystem, oldMajor, newMajor)
		}

		warnings = append(warnings, fmt.Sprintf(
			"[Major Version Bump] %s upgraded v%d -> v%d in %s. Breaking changes likely under SemVer. %s",
			depName, oldMajor, newMajor, base, guidance,
		))
	}

	return warnings
}

func goModuleBase(modulePath string) (string, bool) {
	i := strings.LastIndex(modulePath, "/")
	if i < 0 {
		return modulePath, false
	}
	suffix := modulePath[i+1:]

	if len(suffix) < 2 || suffix[0] != 'v' {
		return modulePath, false
	}
	num := suffix[1:]
	for _, c := range num {
		if c < '0' || c > '9' {
			return modulePath, false
		}
	}

	major := 0
	for _, c := range num {
		major = major*10 + int(c-'0')
	}
	if num[0] == '0' || major < 2 {
		return modulePath, false
	}
	return modulePath[:i], true
}

func extractMajorVersion(version string) int {
	clean := stripVersionPrefix(version)

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

func findMigrationGuide(ecosystem, pkg string, fromMajor, toMajor int) string {
	for _, mg := range knownMigrations {
		if mg.ecosystem == ecosystem && mg.pkg == pkg &&
			mg.fromMajor == fromMajor && mg.toMajor == toMajor {
			return mg.guidance
		}
	}
	return ""
}

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
