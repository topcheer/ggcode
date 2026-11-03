package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	envDriftMaxVars = 10

	envDriftCheckInterval = 5 * time.Minute
)

type envDriftState struct {
	fired      bool
	lastCheck  time.Time
	lastResult string
}

func newEnvDriftState() *envDriftState {
	return &envDriftState{}
}

func (e *envDriftState) reset() {
	e.fired = false

}

var envExampleFiles = []string{
	".env.example",
	".env.template",
	".env.sample",
}

var envActualFiles = []string{
	".env",
	".env.local",
}

func (e *envDriftState) check(workingDir string) string {
	if e.fired {
		return e.lastResult
	}

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

	examplePath := ""
	for _, name := range envExampleFiles {
		p := filepath.Join(absDir, name)
		if _, err := os.Stat(p); err == nil {
			examplePath = p
			break
		}
	}
	if examplePath == "" {

		return ""
	}

	exampleVars := parseEnvFile(examplePath)
	if len(exampleVars) == 0 {
		return ""
	}

	actualVars := collectActualEnvVars(absDir)

	var missing []string
	for _, v := range exampleVars {

		if v.hasDefault {
			continue
		}
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

type envVar struct {
	name       string
	hasDefault bool
}

func parseEnvFile(path string) []envVar {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	return parseEnvContentTyped(string(content))
}

func parseEnvContent(content string) map[string]bool {
	result := make(map[string]bool)
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		line = strings.TrimPrefix(line, "export ")
		idx := strings.IndexByte(line, '=')
		if idx <= 0 {
			continue
		}
		name := strings.TrimSpace(line[:idx])
		value := strings.TrimSpace(line[idx+1:])

		if value != "" {
			result[name] = true
		}
	}
	return result
}

func parseEnvContentTyped(content string) []envVar {
	var result []envVar
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		if hashIdx := strings.Index(line, " #"); hashIdx > 0 {
			line = strings.TrimSpace(line[:hashIdx])
		}

		line = strings.TrimPrefix(line, "export ")
		idx := strings.IndexByte(line, '=')
		if idx <= 0 {
			continue
		}
		name := strings.TrimSpace(line[:idx])
		value := strings.TrimSpace(line[idx+1:])

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
