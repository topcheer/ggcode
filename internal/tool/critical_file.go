package tool

import (
	"path/filepath"
	"strconv"
	"strings"
)

type criticalFileCategory struct {
	matchFn func(base, path string) bool
	warning string
}

var criticalFileCategories = []criticalFileCategory{

	{
		matchFn: func(base, _ string) bool { return base == "go.mod" },
		warning: "go.mod controls the Go module's dependencies and version. " +
			"After modifying, run `go mod tidy` to sync go.sum and remove unused deps. " +
			"Verify with `go build ./...` and `go test ./...`.",
	},
	{
		matchFn: func(base, _ string) bool { return base == "go.sum" },
		warning: "go.sum contains cryptographic checksums for module dependencies. " +
			"Do not edit manually — regenerate with `go mod tidy` or `go mod download`.",
	},

	{
		matchFn: func(base, _ string) bool { return base == "package.json" },
		warning: "package.json defines npm dependencies and scripts. " +
			"After changing dependencies, run `npm install` (or `yarn`/`pnpm install`) to update the lock file. " +
			"Verify the project builds with `npm run build` or equivalent.",
	},
	{
		matchFn: func(base, _ string) bool {
			return base == "package-lock.json" || base == "yarn.lock" || base == "pnpm-lock.yaml"
		},
		warning: "This is a lock file that pins exact dependency versions. " +
			"Do not edit manually — regenerate it by running `npm install` (or `yarn`/`pnpm install`).",
	},

	{
		matchFn: func(base, _ string) bool {
			return base == "requirements.txt" || strings.HasPrefix(base, "requirements-") && strings.HasSuffix(base, ".txt")
		},
		warning: "requirements.txt pins Python dependencies. " +
			"After modifying, run `pip install -r requirements.txt` to install new packages. " +
			"Verify imports work correctly.",
	},
	{
		matchFn: func(base, _ string) bool {
			return base == "Pipfile" || base == "Pipfile.lock" || base == "pyproject.toml"
		},
		warning: "This file controls Python project/dependency configuration. " +
			"After modifying, reinstall dependencies with `pipenv install`, `poetry install`, or `pip install -e .` as appropriate.",
	},

	{
		matchFn: func(base, _ string) bool { return base == "Cargo.toml" },
		warning: "Cargo.toml defines Rust dependencies and project metadata. " +
			"After modifying, run `cargo build` to update Cargo.lock and verify compilation.",
	},
	{
		matchFn: func(base, _ string) bool { return base == "Cargo.lock" },
		warning: "Cargo.lock pins exact dependency versions for reproducible builds. " +
			"Do not edit manually — regenerate with `cargo build` or `cargo update`.",
	},

	{
		matchFn: func(base, _ string) bool {

			lower := strings.ToLower(base)
			if base == "Dockerfile" {
				return true
			}
			if !strings.HasPrefix(lower, "dockerfile.") {
				return false
			}
			ext := lower[len("dockerfile."):]
			switch ext {
			case "base", "dev", "prod", "production", "development", "local",
				"linux", "windows", "alpine", "debian", "web", "api", "app",
				"test", "stage", "staging", "build":
				return true
			}

			if _, err := strconv.Atoi(ext); err == nil {
				return true
			}
			return false
		},
		warning: "Dockerfile defines the container image build. " +
			"After modifying, verify the image builds with `docker build .` (or `docker build -t test .`).",
	},
	{
		matchFn: func(base, _ string) bool {

			if base == "docker-compose.yml" || base == "docker-compose.yaml" ||
				base == "compose.yml" || base == "compose.yaml" {
				return true
			}
			lower := strings.ToLower(base)
			if !strings.HasPrefix(lower, "docker-compose.") {
				return false
			}
			ext := lower[len("docker-compose."):]
			for _, suffix := range []string{"yml", "yaml"} {
				for _, variant := range []string{"base", "dev", "prod", "production",
					"development", "local", "test", "stage", "staging", "build",
					"override", "ci", "web", "api", "app"} {
					if ext == variant+"."+suffix {
						return true
					}
				}
			}
			return false
		},
		warning: "docker-compose file defines multi-container orchestration. " +
			"After modifying, verify with `docker compose config` to validate syntax.",
	},

	{
		matchFn: func(_, path string) bool {

			lower := strings.ToLower(path)
			return strings.Contains(lower, ".github/workflows/") ||
				strings.HasSuffix(lower, ".gitlab-ci.yml") ||
				strings.HasSuffix(lower, ".gitlab-ci.yaml") ||
				strings.Contains(lower, ".circleci/config.yml")
		},
		warning: "This CI/CD pipeline file automates build, test, and deployment. " +
			"Syntax errors here can silently break all automated checks. " +
			"Validate the YAML syntax and check that referenced actions/images/scripts exist.",
	},

	{
		matchFn: func(base, _ string) bool {
			return base == "Makefile" || base == "GNUmakefile" || base == "makefile"
		},
		warning: "Makefile defines build automation targets. " +
			"After modifying, verify that key targets still work with `make build` (or the primary target).",
	},
	{
		matchFn: func(base, _ string) bool {
			return base == "CMakeLists.txt" || strings.HasSuffix(base, ".cmake")
		},
		warning: "CMake build configuration file. " +
			"After modifying, verify the build system regenerates and compiles successfully.",
	},

	{
		matchFn: func(base, _ string) bool {
			return base == ".env.example" || base == ".env.template"
		},
		warning: "This file documents required environment variables. " +
			"Ensure any new variables are also reflected in the actual deployment configuration.",
	},

	{
		matchFn: func(base, _ string) bool {
			return base == "tsconfig.json" || base == "jsconfig.json"
		},
		warning: "TypeScript/JavaScript compiler configuration. " +
			"After modifying, verify with `npx tsc --noEmit` to catch type errors.",
	},
	{
		matchFn: func(base, _ string) bool {
			return base == ".eslintrc.js" || base == ".eslintrc.json" || base == ".eslintrc.yml" ||
				base == "eslint.config.js" || base == "eslint.config.mjs" || base == "biome.json"
		},
		warning: "Linter/formatter configuration. " +
			"After modifying, run the linter to verify rules are valid and the codebase still passes.",
	},
}

func criticalFileWarning(filePath string) string {
	if filePath == "" {
		return ""
	}
	base := filepath.Base(filePath)
	for _, cat := range criticalFileCategories {
		if cat.matchFn(base, filePath) {
			return "\n[Critical file] " + cat.warning
		}
	}
	return ""
}
