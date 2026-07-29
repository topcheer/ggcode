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
func detectProjectCommands(workingDir string) projectCommandInfo {
	if workingDir == "" {
		return projectCommandInfo{}
	}
	if info, err := os.Stat(workingDir); err != nil || !info.IsDir() {
		return projectCommandInfo{}
	}

	// 1. Makefile — authoritative build configuration.
	for _, mf := range []string{"Makefile", "makefile", "GNUmakefile"} {
		path := filepath.Join(workingDir, mf)
		if data, err := os.ReadFile(path); err == nil {
			content := string(data)
			for _, target := range []string{"verify-ci", "ci", "verify", "test", "build"} {
				if cmdHasMakeTarget(content, target) {
					return projectCommandInfo{verify: "make " + target}
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
					return projectCommandInfo{verify: "just " + recipe}
				}
			}
			return projectCommandInfo{verify: "just"}
		}
	}

	// 3. Taskfile.
	for _, tf := range []string{"Taskfile.yml", "Taskfile.yaml", ".taskfile.yml"} {
		path := filepath.Join(workingDir, tf)
		if data, err := os.ReadFile(path); err == nil {
			content := string(data)
			for _, task := range []string{"verify-ci", "ci", "verify", "test", "build"} {
				if strings.Contains(content, task+":") {
					return projectCommandInfo{verify: "task " + task}
				}
			}
			return projectCommandInfo{verify: "task"}
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
			return projectCommandInfo{verify: "bash " + script}
		}
	}

	// 5. Language-specific defaults.
	if cmdFileExists(filepath.Join(workingDir, "go.mod")) {
		return projectCommandInfo{
			verify: "go build ./...",
			test:   "go test ./...",
		}
	}
	if cmdFileExists(filepath.Join(workingDir, "Cargo.toml")) {
		return projectCommandInfo{
			verify: "cargo build",
			test:   "cargo test",
		}
	}
	if cmdFileExists(filepath.Join(workingDir, "package.json")) {
		return projectCommandInfo{
			verify: "npm run build",
			test:   "npm test",
		}
	}
	if cmdFileExists(filepath.Join(workingDir, "CMakeLists.txt")) {
		return projectCommandInfo{verify: "cmake --build build"}
	}
	if cmdFileExists(filepath.Join(workingDir, "pyproject.toml")) ||
		cmdFileExists(filepath.Join(workingDir, "setup.py")) {
		return projectCommandInfo{verify: "python -m pytest"}
	}
	if cmdFileExists(filepath.Join(workingDir, "pom.xml")) {
		return projectCommandInfo{
			verify: "mvn compile",
			test:   "mvn test",
		}
	}
	if cmdFileExists(filepath.Join(workingDir, "build.gradle")) ||
		cmdFileExists(filepath.Join(workingDir, "build.gradle.kts")) {
		return projectCommandInfo{
			verify: "gradle build",
			test:   "gradle test",
		}
	}

	return projectCommandInfo{}
}

// projectCommandsSection formats detected commands as a compact system prompt
// section. Returns empty if no commands were detected.
func projectCommandsSection(workingDir string) string {
	cmds := detectProjectCommands(workingDir)
	if cmds.verify == "" && cmds.test == "" {
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
	return strings.TrimRight(sb.String(), "\n")
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
