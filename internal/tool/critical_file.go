package tool

import (
	"path/filepath"
	"strings"
)

// Critical Infrastructure File Warning System
//
// Research: Claude Code, Cursor, and Aider all provide contextual guidance
// when the agent modifies project infrastructure files (dependencies, build
// configs, CI/CD pipelines). These files have wide blast radius — a careless
// edit can break builds, deployments, or tests that aren't immediately visible.
//
// The gap in ggcode: when the agent edits go.mod, package.json, Dockerfile,
// or CI workflow files, it receives no contextual warning about:
//   - Required follow-up commands (go mod tidy, npm install, etc.)
//   - Side effects (lock files that need regeneration)
//   - Verification steps to run before committing
//
// This system appends a concise, actionable warning to the tool result when
// a critical infrastructure file is modified. It is:
//   - Zero-LLM-cost (pure pattern matching on file paths)
//   - Non-blocking (informational only — the edit still applies)
//   - Once-per-edit (no spam — fires at most once per tool call)

// criticalFileCategory describes a category of infrastructure files.
type criticalFileCategory struct {
	// matchFn returns true if the file path belongs to this category.
	matchFn func(base, path string) bool
	// warning is the message appended to the tool result.
	warning string
}

// criticalFileCategories defines all known categories of infrastructure files
// and their associated warnings. Ordered by likelihood of occurrence.
var criticalFileCategories = []criticalFileCategory{
	// --- Go dependency files ---
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
	// --- Node.js dependency files ---
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
	// --- Python dependency files ---
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
	// --- Rust dependency files ---
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
	// --- Container / deployment files ---
	{
		matchFn: func(base, _ string) bool {
			return base == "Dockerfile" || strings.HasPrefix(strings.ToLower(base), "dockerfile.")
		},
		warning: "Dockerfile defines the container image build. " +
			"After modifying, verify the image builds with `docker build .` (or `docker build -t test .`).",
	},
	{
		matchFn: func(base, _ string) bool {
			return base == "docker-compose.yml" || base == "docker-compose.yaml" ||
				strings.HasPrefix(base, "docker-compose.")
		},
		warning: "docker-compose file defines multi-container orchestration. " +
			"After modifying, verify with `docker compose config` to validate syntax.",
	},
	// --- CI/CD workflow files ---
	{
		matchFn: func(_, path string) bool {
			// .github/workflows/*.yml, .gitlab-ci.yml, .circleci/config.yml
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
	// --- Build system files ---
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
	// --- Environment / config files ---
	{
		matchFn: func(base, _ string) bool {
			return base == ".env.example" || base == ".env.template"
		},
		warning: "This file documents required environment variables. " +
			"Ensure any new variables are also reflected in the actual deployment configuration.",
	},
	// --- TypeScript / JS config ---
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

// criticalFileWarning returns a warning string if the given file path is a
// critical infrastructure file. Returns empty string for non-critical files.
//
// The warning includes:
//   - What the file controls
//   - Required follow-up commands
//   - Verification steps
//
// This is appended to the tool result after a successful edit/write, giving
// the agent immediate contextual awareness without requiring an LLM call.
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
