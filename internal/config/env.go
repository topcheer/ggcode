package config

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

var envPattern = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

type envLookupFunc func(string) (string, bool)

// ExpandEnv replaces ${VAR} patterns in a string with environment variable values.
// If the variable is not set, the pattern is left unchanged.
func ExpandEnv(s string) string {
	return ExpandEnvWithLookup(s, os.LookupEnv)
}

func ExpandEnvWithLookup(s string, lookup envLookupFunc) string {
	if lookup == nil {
		lookup = os.LookupEnv
	}
	return envPattern.ReplaceAllStringFunc(s, func(match string) string {
		varName := match[2 : len(match)-1] // strip ${ and }
		if val, ok := lookup(varName); ok {
			return val
		}
		return match
	})
}

// ExpandEnvRecursive expands ${VAR} in all string values of a map recursively.
func ExpandEnvRecursive(m map[string]interface{}) map[string]interface{} {
	return ExpandEnvRecursiveWithLookup(m, os.LookupEnv)
}

func ExpandEnvRecursiveWithLookup(m map[string]interface{}, lookup envLookupFunc) map[string]interface{} {
	result := make(map[string]interface{})
	for k, v := range m {
		result[k] = expandValueWithLookup(v, lookup)
	}
	return result
}

func expandValueWithLookup(v interface{}, lookup envLookupFunc) interface{} {
	switch val := v.(type) {
	case string:
		return ExpandEnvWithLookup(val, lookup)
	case map[string]interface{}:
		return ExpandEnvRecursiveWithLookup(val, lookup)
	case []interface{}:
		result := make([]interface{}, len(val))
		for i, item := range val {
			result[i] = expandValueWithLookup(item, lookup)
		}
		return result
	default:
		return v
	}
}

// HomeDir returns the user's home directory.
func HomeDir() string {
	if h := os.Getenv("HOME"); h != "" {
		return h
	}
	return "/root"
}

// ConfigDir returns ~/.ggcode
func ConfigDir() string {
	return strings.Join([]string{HomeDir(), ".ggcode"}, string(os.PathSeparator))
}

// ConfigPath returns the default config file path.
func ConfigPath() string {
	return strings.Join([]string{ConfigDir(), "ggcode.yaml"}, string(os.PathSeparator))
}

var commonShellEnvFiles = []string{
	".zshrc",
	".bashrc",
	".profile",
	".zsh_profile",
	".zprofile",
	".bash_profile",
}

func runtimeEnvLookup(raw map[string]interface{}) envLookupFunc {
	values := loadRuntimeEnv(raw)
	return func(name string) (string, bool) {
		val, ok := values[name]
		return val, ok
	}
}

func loadRuntimeEnv(raw map[string]interface{}) map[string]string {
	env := make(map[string]string)
	for _, entry := range os.Environ() {
		name, value, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		env[name] = value
	}

	// Load ~/.ggcode/keys.env — these take precedence over shell rc files
	// but not over the current process environment (already loaded above).
	if err := loadKeysEnvInto(func(key, val string) error {
		if _, exists := env[key]; !exists {
			env[key] = val
		}
		return nil
	}); err == nil {
		// Also set into process env so subsequent lookups work.
		for name, value := range env {
			os.Setenv(name, value)
		}
	}

	needed := referencedEnvVars(raw)
	if len(needed) == 0 && raw == nil {
		for _, name := range defaultRuntimeEnvNames() {
			needed[name] = struct{}{}
		}
	}
	if len(needed) == 0 {
		return env
	}
	missing := make(map[string]struct{})
	for name := range needed {
		if _, ok := env[name]; !ok {
			missing[name] = struct{}{}
		}
	}
	if len(missing) == 0 {
		return env
	}
	for _, fileName := range commonShellEnvFiles {
		path := filepath.Join(HomeDir(), fileName)
		values, err := parseShellEnvFile(path, missing)
		if err != nil {
			continue
		}
		for name, value := range values {
			if _, ok := env[name]; ok {
				continue
			}
			env[name] = ExpandEnvWithLookup(value, func(key string) (string, bool) {
				val, ok := env[key]
				return val, ok
			})
			delete(missing, name)
		}
		if len(missing) == 0 {
			break
		}
	}
	return env
}

func defaultRuntimeEnvNames() []string {
	names := make([]string, 0, len(preferredVendorAPIKeyEnvVars)+3)
	seen := make(map[string]struct{}, len(preferredVendorAPIKeyEnvVars)+3)
	for _, name := range preferredVendorAPIKeyEnvVars {
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		names = append(names, name)
	}
	for _, name := range []string{"ANTHROPIC_BASE_URL", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_API_KEY"} {
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		names = append(names, name)
	}
	return names
}

func referencedEnvVars(raw map[string]interface{}) map[string]struct{} {
	names := make(map[string]struct{})
	if raw == nil {
		return names
	}
	var walk func(interface{})
	walk = func(v interface{}) {
		switch val := v.(type) {
		case string:
			for _, match := range envPattern.FindAllStringSubmatch(val, -1) {
				if len(match) == 2 {
					names[match[1]] = struct{}{}
				}
			}
		case map[string]interface{}:
			for _, item := range val {
				walk(item)
			}
		case []interface{}:
			for _, item := range val {
				walk(item)
			}
		}
	}
	walk(raw)
	return names
}

var envAssignmentPattern = regexp.MustCompile(`^(?:export\s+)?([A-Za-z_][A-Za-z0-9_]*)\s*=\s*(.+)$`)

func parseShellEnvFile(path string, wanted map[string]struct{}) (map[string]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	values := make(map[string]string)
	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		name, value, ok := parseEnvAssignment(line)
		if !ok {
			continue
		}
		if len(wanted) > 0 {
			if _, ok := wanted[name]; !ok {
				continue
			}
		}
		values[name] = value
	}
	return values, nil
}

func parseEnvAssignment(line string) (string, string, bool) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || strings.HasPrefix(trimmed, "#") {
		return "", "", false
	}
	matches := envAssignmentPattern.FindStringSubmatch(trimmed)
	if len(matches) != 3 {
		return "", "", false
	}
	name := matches[1]
	value := strings.TrimSpace(matches[2])
	if value == "" {
		return name, "", true
	}
	if strings.HasPrefix(value, "\"") && strings.HasSuffix(value, "\"") {
		unquoted, err := strconv.Unquote(value)
		if err == nil {
			return name, unquoted, true
		}
	}
	if strings.HasPrefix(value, "'") && strings.HasSuffix(value, "'") {
		return name, value[1 : len(value)-1], true
	}
	if idx := strings.Index(value, " #"); idx >= 0 {
		value = strings.TrimSpace(value[:idx])
	}
	return name, value, true
}
