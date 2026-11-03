package tool

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/topcheer/ggcode/internal/debug"
)

const precommitBuildTimeout = 30 * time.Second

const precommitMaxErrors = 10

var precommitGateEnabled = true

func precommitBuildCheck(ctx context.Context, dir string) string {
	if !precommitGateEnabled {
		return ""
	}
	if dir == "" {
		return ""
	}

	cmd := detectPrecommitBuildCommand(dir)
	if cmd == "" {

		return ""
	}

	debug.Log("precommit-gate", "running build check: %s in %s", cmd, dir)

	checkCtx, cancel := context.WithTimeout(ctx, precommitBuildTimeout)
	defer cancel()

	parts := strings.Fields(cmd)
	if len(parts) == 0 {
		return ""
	}

	var c *exec.Cmd
	if len(parts) > 1 {
		c = exec.CommandContext(checkCtx, parts[0], parts[1:]...)
	} else {
		c = exec.CommandContext(checkCtx, parts[0])
	}
	c.Dir = dir

	var stdout, stderr bytes.Buffer
	c.Stdout = &stdout
	c.Stderr = &stderr

	err := c.Run()
	if err == nil {

		debug.Log("precommit-gate", "build check passed: %s", cmd)
		return ""
	}

	if checkCtx.Err() != nil {
		debug.Log("precommit-gate", "build check aborted (ctx: %v): %s", checkCtx.Err(), cmd)
		return ""
	}

	output := stderr.String()
	if output == "" {
		output = stdout.String()
	}

	errors := extractBuildErrors(output)
	if len(errors) == 0 {

		return fmt.Sprintf(
			"Warning: pre-commit build check failed (command: %q). "+
				"The committed code may not compile. Consider verifying before pushing.",
			cmd,
		)
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf(
		"Warning: pre-commit build check FAILED (command: %q).\n"+
			"The committed code does NOT compile. Consider fixing these errors and "+
			"amending the commit before pushing.\n\nBuild errors:\n",
		cmd,
	))
	shown := errors
	if len(shown) > precommitMaxErrors {
		shown = shown[:precommitMaxErrors]
	}
	for _, e := range shown {
		b.WriteString("  " + e + "\n")
	}
	if len(errors) > precommitMaxErrors {
		b.WriteString(fmt.Sprintf("  ... and %d more\n", len(errors)-precommitMaxErrors))
	}

	debug.Log("precommit-gate", "build check FAILED: %d errors", len(errors))
	return b.String()
}

func detectPrecommitBuildCommand(dir string) string {
	if dir == "" {
		return ""
	}

	for _, mf := range []string{"Makefile", "makefile", "GNUmakefile"} {
		path := filepath.Join(dir, mf)
		if data, err := os.ReadFile(path); err == nil {
			content := string(data)

			for _, target := range []string{"build", "verify-ci"} {
				if hasPrecommitMakeTarget(content, target) {
					return "make " + target
				}
			}
			break
		}
	}

	if fileExistsPrecommit(filepath.Join(dir, "go.mod")) {
		return "go build ./..."
	}
	if fileExistsPrecommit(filepath.Join(dir, "Cargo.toml")) {

		return "cargo check"
	}
	if fileExistsPrecommit(filepath.Join(dir, "package.json")) {

		if hasNPMBuildScript(filepath.Join(dir, "package.json")) {
			return "npm run build"
		}

		return ""
	}
	if fileExistsPrecommit(filepath.Join(dir, "CMakeLists.txt")) {
		return "cmake --build build"
	}

	return ""
}

func hasPrecommitMakeTarget(content, target string) bool {
	prefix := target + ":"
	for _, line := range strings.Split(content, "\n") {

		if strings.HasPrefix(line, "\t") {
			continue
		}
		trimmed := strings.TrimLeft(line, " ")
		if strings.HasPrefix(trimmed, "#") {
			continue
		}
		if strings.HasPrefix(trimmed, prefix) {
			return true
		}
	}
	return false
}

func fileExistsPrecommit(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func hasNPMBuildScript(pkgJSONPath string) bool {
	data, err := os.ReadFile(pkgJSONPath)
	if err != nil {
		return false
	}

	return bytes.Contains(data, []byte(`"build"`))
}

func extractBuildErrors(output string) []string {
	if output == "" {
		return nil
	}

	var errors []string
	scanner := bufio.NewScanner(strings.NewReader(output))
	scanner.Buffer(make([]byte, 0, 64*1024), 256*1024)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		if isBuildErrorLine(line) {

			if len(line) > 200 {
				line = line[:200] + "..."
			}
			errors = append(errors, line)
		}
	}

	return errors
}

func isBuildErrorLine(line string) bool {
	lower := strings.ToLower(line)

	if strings.Contains(line, ".go:") && strings.Contains(lower, "error") {
		return true
	}

	if strings.Contains(line, ".go:") && (strings.Contains(lower, "undefined") ||
		strings.Contains(lower, "cannot use") ||
		strings.Contains(lower, "mismatched") ||
		strings.Contains(lower, "not enough") ||
		strings.Contains(lower, "too many")) {
		return true
	}

	if strings.HasPrefix(lower, "error[") || strings.HasPrefix(lower, "error:") {
		return true
	}

	if strings.Contains(lower, ": error:") {
		return true
	}

	if strings.Contains(lower, ": error ts") {
		return true
	}

	if strings.HasPrefix(lower, "error ") || strings.HasPrefix(line, "Error ") {
		return true
	}

	if strings.Contains(lower, "error:") && !regexp.MustCompile(`\b0 errors`).MatchString(lower) {
		return true
	}

	return false
}
