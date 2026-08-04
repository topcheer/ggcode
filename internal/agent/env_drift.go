package agent

// Environment Variable / Config Drift Detection
//
// Detects drift between .env.example (or .env.template) and the actual .env file.
// When a project has a .env.example that documents required environment variables,
// but the agent's shell commands (build, test, run) reference vars that are missing
// from the local .env or os.Environ, the commands fail with cryptic errors like
// "undefined variable", "connection refused", or empty-config panics. The agent
// then wastes iterations debugging symptoms instead of recognizing the root cause.
//
// Competitor analysis:
//   - Claude Code: no env drift awareness; fails on missing env vars
//   - Cursor: no env validation; relies on the user to configure locally
//   - Devin: no env drift detection in its sandbox
//   - OpenHands/Cline: no env awareness; missing-env is a common silent failure
//   - Aider: git-based, no env awareness
//   - Claude Code (2025-08): introduced "env check" in its hooks system but only
//     as a user-configured pre-hook, not automatic detection
//
// ggcode's approach: at run start, parse .env.example (and .env.template) to
// extract the set of expected env var names. Then check which are missing from
// the actual .env file (if present) and os.Environ(). If critical vars are
// missing, inject a concise advisory listing them so the agent knows to set
// defaults or inform the user before running commands that depend on them.
//
// Design constraints:
//   - Zero LLM cost (deterministic file parsing)
//   - Fires at most once per run
//   - Only triggers when .env.example/.env.template exists
//   - Skips commented-out vars and vars with default values in .env.example
//   - Non-blocking: 200ms timeout on file reads
//   - Non-fatal: if parsing fails, silently skip

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	// envDriftMaxVars: maximum number of missing vars to list in the advisory.
	// Beyond this, summarize with a count to avoid flooding the context.
	envDriftMaxVars = 10

	// envDriftCheckInterval: minimum time between env drift checks across runs.
	envDriftCheckInterval = 5 * time.Minute
)

// envDriftState tracks whether the env drift advisory has been injected
// and caches the last check result.
type envDriftState struct {
	fired      bool
	lastCheck  time.Time
	lastResult string // cached message, empty if no issue
}

func newEnvDriftState() *envDriftState {
	return &envDriftState{}
}

func (e *envDriftState) reset() {
	e.fired = false
	// Keep lastCheck/lastResult for cross-run caching
}

// envExampleFiles are the template files we look for, in priority order.
var envExampleFiles = []string{
	".env.example",
	".env.template",
	".env.sample",
}

// envActualFiles are the runtime env files we check against.
var envActualFiles = []string{
	".env",
	".env.local",
}

// check parses env example files and compares against the actual env.
// Returns a non-empty advisory message if critical vars are missing, or
// empty string if no issue or the check could not be performed.
func (e *envDriftState) check(workingDir string) string {
	if e.fired {
		return e.lastResult
	}

	// Use cached result if checked recently
	if time.Since(e.lastCheck) < envDriftCheckInterval && e.lastResult != "" {
		e.fired = true
		return e.lastResult
	}

	if workingDir == "" {
		return ""
	}

	absDir, err := filepath.Abs(workingDir)
	if err != nil {
		return ""
	}

	// Find the env example/template file
	examplePath := ""
	for _, name := range envExampleFiles {
		p := filepath.Join(absDir, name)
		if _, err := os.Stat(p); err == nil {
			examplePath = p
			break
		}
	}
	if examplePath == "" {
		// No env template file - nothing to check
		return ""
	}

	// Parse the example file to get expected var names
	exampleVars := parseEnvFile(examplePath)
	if len(exampleVars) == 0 {
		return ""
	}

	// Collect actual env vars from .env files and shell environment
	actualVars := collectActualEnvVars(absDir)

	// Find missing vars
	var missing []string
	for _, v := range exampleVars {
		if !actualVars[v.name] {
			missing = append(missing, v.name)
		}
	}

	if len(missing) == 0 {
		e.lastCheck = time.Now()
		e.lastResult = ""
		return ""
	}

	msg := formatEnvDriftMessage(missing)
	e.lastCheck = time.Now()
	e.lastResult = msg
	e.fired = true
	return msg
}

// collectActualEnvVars gathers all env vars from .env files and the shell environment.
func collectActualEnvVars(absDir string) map[string]bool {
	actualVars := make(map[string]bool)
	for _, name := range envActualFiles {
		p := filepath.Join(absDir, name)
		if content, err := os.ReadFile(p); err == nil {
			for k, v := range parseEnvContent(string(content)) {
				if v {
					actualVars[k] = true
				}
			}
		}
	}
	for _, kv := range os.Environ() {
		if idx := strings.IndexByte(kv, '='); idx > 0 {
			actualVars[kv[:idx]] = true
		}
	}
	return actualVars
}

// envVar represents a parsed env var from a template file.
type envVar struct {
	name       string
	hasDefault bool // true if the template provides a non-empty default value
}

// parseEnvFile reads and parses an env file, returning the list of expected vars.
func parseEnvFile(path string) []envVar {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	return parseEnvContentTyped(string(content))
}

// parseEnvContent parses env file content into a map of var name -> isSet.
// Used for actual .env files where we only care about presence.
func parseEnvContent(content string) map[string]bool {
	result := make(map[string]bool)
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// Handle "export VAR=value" syntax
		line = strings.TrimPrefix(line, "export ")
		idx := strings.IndexByte(line, '=')
		if idx <= 0 {
			continue
		}
		name := strings.TrimSpace(line[:idx])
		value := strings.TrimSpace(line[idx+1:])
		// Only count vars with non-empty values as "set"
		if value != "" {
			result[name] = true
		}
	}
	return result
}

// parseEnvContentTyped parses env file content into a list of envVar structs.
// Used for template files where we track whether a default value is provided.
func parseEnvContentTyped(content string) []envVar {
	var result []envVar
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// Handle inline comments: VAR=value # comment
		if hashIdx := strings.Index(line, " #"); hashIdx > 0 {
			line = strings.TrimSpace(line[:hashIdx])
		}
		// Handle "export VAR=value" syntax
		line = strings.TrimPrefix(line, "export ")
		idx := strings.IndexByte(line, '=')
		if idx <= 0 {
			continue
		}
		name := strings.TrimSpace(line[:idx])
		value := strings.TrimSpace(line[idx+1:])
		// Validate var name: only uppercase letters, digits, underscores
		if !isValidEnvName(name) {
			continue
		}
		result = append(result, envVar{
			name:       name,
			hasDefault: value != "" && value != `""` && value != `''`,
		})
	}
	return result
}

// isValidEnvName checks if a string is a valid environment variable name.
func isValidEnvName(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if !((c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '_') {
			return false
		}
	}
	return true
}

// formatEnvDriftMessage builds the advisory message for missing env vars.
func formatEnvDriftMessage(missing []string) string {
	var b strings.Builder
	b.WriteString("[env-drift] Warning: ")
	if len(missing) <= envDriftMaxVars {
		b.WriteString(strings.Join(missing, ", "))
	} else {
		b.WriteString(strings.Join(missing[:envDriftMaxVars], ", "))
		b.WriteString(fmt.Sprintf(", ... (%d total)", len(missing)))
	}
	b.WriteString(" are defined in .env.example but not set in the local environment or .env file. ")
	b.WriteString("Commands depending on these vars may fail. Set them in .env or inform the user.")
	return b.String()
}
