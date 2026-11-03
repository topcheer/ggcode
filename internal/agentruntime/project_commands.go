package agentruntime

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type projectCommandInfo struct {
	verify string
	test   string
	lint   string
	format string
}

func detectProjectCommands(workingDir string) projectCommandInfo {
	if workingDir == "" {
		return projectCommandInfo{}
	}
	if info, err := os.Stat(workingDir); err != nil || !info.IsDir() {
		return projectCommandInfo{}
	}

	lint := detectLintCommand(workingDir)
	format := detectFormatCommand(workingDir)

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
			break
		}
	}

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

	for _, tf := range []string{"Taskfile.yml", "Taskfile.yaml", ".taskfile.yml"} {
		path := filepath.Join(workingDir, tf)
		if data, err := os.ReadFile(path); err == nil {
			content := string(data)
			for _, task := range []string{"verify-ci", "ci", "verify", "test", "build"} {
				if taskfileHasTarget(content, task) {
					return projectCommandInfo{verify: "task " + task, lint: lint, format: format}
				}
			}
			return projectCommandInfo{verify: "task", lint: lint, format: format}
		}
	}

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

	return projectCommandInfo{lint: lint, format: format}
}

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

func taskfileHasTarget(content, task string) bool {

	inTasks := false
	for _, line := range strings.Split(content, "\n") {

		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		indented := line[0] == ' ' || line[0] == '\t'
		if !indented {

			if line[0] == '#' {
				continue
			}

			t := strings.TrimRight(line, ": \t")
			inTasks = strings.TrimSpace(t) == "tasks"
			continue
		}
		if !inTasks {
			continue
		}
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, task+":") {
			return true
		}
	}
	return false
}

func detectLintCommand(workingDir string) string {

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
				if taskfileHasTarget(content, task) {
					return "task " + task
				}
			}
			break
		}
	}

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

func detectFormatCommand(workingDir string) string {

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
				if taskfileHasTarget(content, task) {
					return "task " + task
				}
			}
			break
		}
	}

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

func cmdFileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
