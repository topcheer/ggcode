package agentruntime

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// projectCommandInfo holds detected build/test commands for a project.
// These are injected into the system prompt so the agent knows how to
// verify changes from the start of the session, rather than discovering
// the commands through trial-and-error after several edits.
type projectCommandInfo struct {
	verify string // primary verification command (build + test combined)
	test   string // test-only command (if distinct from verify)
	lint   string // lint command (static analysis / code quality)
	format string // format command (code formatting / style)
}

// detectProjectCommands examines the working directory for build system
// markers and returns detected commands. Returns zero values if the
// directory cannot be read or no build system is recognized.
//
// Priority order (matches detectBuildSystem in agent/verify_hint.go):
//  1. Makefile with high-value targets (verify-ci, ci, verify, test, build)
//  2. Justfile / Taskfile recipes
//  3. Project-specific verification scripts (scripts/dev/verify-ci.sh, etc.)
//  4. Language-specific defaults (go.mod, Cargo.toml, package.json, etc.)
//
// detectProjectCommands examines the working directory for build system
// markers and returns detected commands. Returns zero values if the
// directory cannot be read or no build system is recognized.
//
// Priority order (matches detectBuildSystem in agent/verify_hint.go):
//  1. Makefile with high-value targets (verify-ci, ci, verify, test, build)
//  2. Justfile / Taskfile recipes
//  3. Project-specific verification scripts (scripts/dev/verify-ci.sh, etc.)
//  4. Language-specific defaults (go.mod, Cargo.toml, package.json, etc.)
//
// In addition to build/test commands, lint and format commands are detected
// from Makefile targets or language-specific defaults, so the agent knows
// the project's quality tooling from the start.
func detectProjectCommands(workingDir string) projectCommandInfo {
	if workingDir == "" {
		return projectCommandInfo{}
	}
	if info, err := os.Stat(workingDir); err != nil || !info.IsDir() {
		return projectCommandInfo{}
	}

	// Detect lint and format commands independently — these are orthogonal
	// to the build system and may come from different sources (e.g. a Makefile
	// may define `lint` and `fmt` targets alongside `build`).
	lint := detectLintCommand(workingDir)
	format := detectFormatCommand(workingDir)

	// 1. Makefile — authoritative build configuration.
	for _, mf := range []string{"Makefile", "makefile", "GNUmakefile"} {
		path := filepath.Join(workingDir, mf)
		if data, err := os.ReadFile(path); err == nil {
			content := string(data)
			for _, target := range []string{"verify-ci", "ci", "verify", "test", "build"} {
				if cmdHasMakeTarget(content, target) {
					return projectCommandInfo{
						verify: "make " + target,
						lint:   lint,
						format: format,
					}
				}
			}
			break // Makefile exists but no recognized target; fall through
		}
	}

	// 2. Justfile.
	for _, jf := range []string{"Justfile", "justfile", ".justfile"} {
		path := filepath.Join(workingDir, jf)
		if data, err := os.ReadFile(path); err == nil {
			content := string(data)
			for _, recipe := range []string{"verify-ci", "ci", "verify", "test", "build"} {
				if strings.Contains(content, "\n"+recipe+":") || strings.HasPrefix(content, recipe+":") {
					return projectCommandInfo{verify: "just " + recipe, lint: lint, format: format}
				}
			}
			return projectCommandInfo{verify: "just", lint: lint, format: format}
		}
	}

	// 3. Taskfile.
	for _, tf := range []string{"Taskfile.yml", "Taskfile.yaml", ".taskfile.yml"} {
		path := filepath.Join(workingDir, tf)
		if data, err := os.ReadFile(path); err == nil {
			content := string(data)
			for _, task := range []string{"verify-ci", "ci", "verify", "test", "build"} {
				if strings.Contains(content, task+":") {
					return projectCommandInfo{verify: "task " + task, lint: lint, format: format}
				}
			}
			return projectCommandInfo{verify: "task", lint: lint, format: format}
		}
	}

	// 4. Project-specific verification scripts.
	for _, script := range []string{
		filepath.Join("scripts", "dev", "verify-ci.sh"),
		filepath.Join("scripts", "verify.sh"),
		filepath.Join("scripts", "ci.sh"),
	} {
		path := filepath.Join(workingDir, script)
		if cmdFileExists(path) {
			return projectCommandInfo{verify: "bash " + script, lint: lint, format: format}
		}
	}

	// 5. Language-specific defaults.
	if cmdFileExists(filepath.Join(workingDir, "go.mod")) {
		return projectCommandInfo{
			verify: "go build ./...",
			test:   "go test ./...",
			lint:   lint,
			format: format,
		}
	}
	if cmdFileExists(filepath.Join(workingDir, "Cargo.toml")) {
		return projectCommandInfo{
			verify: "cargo build",
			test:   "cargo test",
			lint:   lint,
			format: format,
		}
	}
	if cmdFileExists(filepath.Join(workingDir, "package.json")) {
		return projectCommandInfo{
			verify: "npm run build",
			test:   "npm test",
			lint:   lint,
			format: format,
		}
	}
	if cmdFileExists(filepath.Join(workingDir, "CMakeLists.txt")) {
		return projectCommandInfo{verify: "cmake --build build", lint: lint, format: format}
	}
	if cmdFileExists(filepath.Join(workingDir, "pyproject.toml")) ||
		cmdFileExists(filepath.Join(workingDir, "setup.py")) {
		return projectCommandInfo{verify: "python -m pytest", lint: lint, format: format}
	}
	if cmdFileExists(filepath.Join(workingDir, "pom.xml")) {
		return projectCommandInfo{
			verify: "mvn compile",
			test:   "mvn test",
			lint:   lint,
			format: format,
		}
	}
	if cmdFileExists(filepath.Join(workingDir, "build.gradle")) ||
		cmdFileExists(filepath.Join(workingDir, "build.gradle.kts")) {
		return projectCommandInfo{
			verify: "gradle build",
			test:   "gradle test",
			lint:   lint,
			format: format,
		}
	}

	// Even with no build system, return lint/format if detected.
	return projectCommandInfo{lint: lint, format: format}
}

// projectCommandsSection formats detected commands as a compact system prompt
// section. Returns empty if no commands were detected.
func projectCommandsSection(workingDir string) string {
	cmds := detectProjectCommands(workingDir)
	if cmds.verify == "" && cmds.test == "" && cmds.lint == "" && cmds.format == "" {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("\n\n## Project commands\n")
	sb.WriteString("Detected build/test commands for this project. Use them to verify changes.\n")
	if cmds.verify != "" {
		sb.WriteString(fmt.Sprintf("- Verify: `%s`\n", cmds.verify))
	}
	if cmds.test != "" {
		sb.WriteString(fmt.Sprintf("- Test: `%s`\n", cmds.test))
	}
	if cmds.lint != "" {
		sb.WriteString(fmt.Sprintf("- Lint: `%s`\n", cmds.lint))
	}
	if cmds.format != "" {
		sb.WriteString(fmt.Sprintf("- Format: `%s`\n", cmds.format))
	}
	return strings.TrimRight(sb.String(), "\n")
}

// detectLintCommand tries to find the project's lint command.
// Priority: Makefile/Justfile/Taskfile targets > language-specific defaults.
func detectLintCommand(workingDir string) string {
	// 1. Build runner targets.
	for _, mf := range []string{"Makefile", "makefile", "GNUmakefile"} {
		path := filepath.Join(workingDir, mf)
		if data, err := os.ReadFile(path); err == nil {
			content := string(data)
			for _, target := range []string{"lint", "lint-check", "check", "vet"} {
				if cmdHasMakeTarget(content, target) {
					return "make " + target
				}
			}
			break
		}
	}
	for _, jf := range []string{"Justfile", "justfile", ".justfile"} {
		path := filepath.Join(workingDir, jf)
		if data, err := os.ReadFile(path); err == nil {
			content := string(data)
			for _, recipe := range []string{"lint", "lint-check", "check", "vet"} {
				if strings.Contains(content, "\n"+recipe+":") || strings.HasPrefix(content, recipe+":") {
					return "just " + recipe
				}
			}
			break
		}
	}
	for _, tf := range []string{"Taskfile.yml", "Taskfile.yaml", ".taskfile.yml"} {
		path := filepath.Join(workingDir, tf)
		if data, err := os.ReadFile(path); err == nil {
			content := string(data)
			for _, task := range []string{"lint", "lint-check", "check", "vet"} {
				if strings.Contains(content, task+":") {
					return "task " + task
				}
			}
			break
		}
	}

	// 2. Language-specific defaults based on config files present.
	if cmdFileExists(filepath.Join(workingDir, ".golangci.yml")) ||
		cmdFileExists(filepath.Join(workingDir, ".golangci.yaml")) ||
		cmdFileExists(filepath.Join(workingDir, ".golangci.toml")) {
		return "golangci-lint run"
	}
	if cmdFileExists(filepath.Join(workingDir, "go.mod")) {
		return "go vet ./..."
	}
	if cmdFileExists(filepath.Join(workingDir, ".eslintrc.js")) ||
		cmdFileExists(filepath.Join(workingDir, ".eslintrc.json")) ||
		cmdFileExists(filepath.Join(workingDir, ".eslintrc.cjs")) ||
		cmdFileExists(filepath.Join(workingDir, "eslint.config.js")) ||
		cmdFileExists(filepath.Join(workingDir, "eslint.config.mjs")) {
		return "eslint ."
	}
	if cmdFileExists(filepath.Join(workingDir, "ruff.toml")) ||
		cmdFileExists(filepath.Join(workingDir, ".ruff.toml")) {
		return "ruff check ."
	}
	if cmdFileExists(filepath.Join(workingDir, ".rubocop.yml")) {
		return "rubocop"
	}
	if cmdFileExists(filepath.Join(workingDir, ".hadolint.yaml")) {
		return "hadolint Dockerfile"
	}

	return ""
}

// detectFormatCommand tries to find the project's format command.
// Priority: Makefile/Justfile/Taskfile targets > language-specific defaults.
func detectFormatCommand(workingDir string) string {
	// 1. Build runner targets.
	for _, mf := range []string{"Makefile", "makefile", "GNUmakefile"} {
		path := filepath.Join(workingDir, mf)
		if data, err := os.ReadFile(path); err == nil {
			content := string(data)
			for _, target := range []string{"fmt", "format"} {
				if cmdHasMakeTarget(content, target) {
					return "make " + target
				}
			}
			break
		}
	}
	for _, jf := range []string{"Justfile", "justfile", ".justfile"} {
		path := filepath.Join(workingDir, jf)
		if data, err := os.ReadFile(path); err == nil {
			content := string(data)
			for _, recipe := range []string{"fmt", "format"} {
				if strings.Contains(content, "\n"+recipe+":") || strings.HasPrefix(content, recipe+":") {
					return "just " + recipe
				}
			}
			break
		}
	}
	for _, tf := range []string{"Taskfile.yml", "Taskfile.yaml", ".taskfile.yml"} {
		path := filepath.Join(workingDir, tf)
		if data, err := os.ReadFile(path); err == nil {
			content := string(data)
			for _, task := range []string{"fmt", "format"} {
				if strings.Contains(content, task+":") {
					return "task " + task
				}
			}
			break
		}
	}

	// 2. Language-specific defaults based on config files present.
	if cmdFileExists(filepath.Join(workingDir, "go.mod")) {
		return "gofmt -w ."
	}
	if cmdFileExists(filepath.Join(workingDir, ".prettierrc")) ||
		cmdFileExists(filepath.Join(workingDir, ".prettierrc.js")) ||
		cmdFileExists(filepath.Join(workingDir, ".prettierrc.json")) ||
		cmdFileExists(filepath.Join(workingDir, ".prettierrc.yml")) ||
		cmdFileExists(filepath.Join(workingDir, "prettier.config.js")) {
		return "prettier --write ."
	}
	if cmdFileExists(filepath.Join(workingDir, "ruff.toml")) ||
		cmdFileExists(filepath.Join(workingDir, ".ruff.toml")) {
		return "ruff format ."
	}
	if cmdFileExists(filepath.Join(workingDir, ".rustfmt.toml")) ||
		cmdFileExists(filepath.Join(workingDir, "rustfmt.toml")) {
		return "cargo fmt"
	}

	return ""
}

// cmdHasMakeTarget checks if a Makefile defines a target with the given name.
func cmdHasMakeTarget(makefileContent, target string) bool {
	targetPrefix := target + ":"
	for _, line := range strings.Split(makefileContent, "\n") {
		trimmed := strings.TrimLeft(line, " \t")
		if strings.HasPrefix(trimmed, "#") {
			continue
		}
		if strings.HasPrefix(trimmed, targetPrefix) {
			return true
		}
	}
	return false
}

// cmdFileExists is a local file-existence helper to avoid polluting the
// package namespace with a second fileExists symbol.
func cmdFileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
