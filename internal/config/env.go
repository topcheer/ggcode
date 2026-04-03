package config

import (
	"os"
	"regexp"
	"strings"
)

var envPattern = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// ExpandEnv replaces ${VAR} patterns in a string with environment variable values.
// If the variable is not set, the pattern is left unchanged.
func ExpandEnv(s string) string {
	return envPattern.ReplaceAllStringFunc(s, func(match string) string {
		varName := match[2 : len(match)-1] // strip ${ and }
		if val, ok := os.LookupEnv(varName); ok {
			return val
		}
		return match
	})
}

// ExpandEnvRecursive expands ${VAR} in all string values of a map recursively.
func ExpandEnvRecursive(m map[string]interface{}) map[string]interface{} {
	result := make(map[string]interface{})
	for k, v := range m {
		result[k] = expandValue(v)
	}
	return result
}

func expandValue(v interface{}) interface{} {
	switch val := v.(type) {
	case string:
		return ExpandEnv(val)
	case map[string]interface{}:
		return ExpandEnvRecursive(val)
	case []interface{}:
		result := make([]interface{}, len(val))
		for i, item := range val {
			result[i] = expandValue(item)
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
