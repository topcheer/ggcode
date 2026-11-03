package config

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
	"github.com/topcheer/ggcode/internal/util"
)

var envPattern = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

var envDefaultPattern = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)(?::?-([^}]*))?\}`)

var envAnyRefPattern = regexp.MustCompile(`\$\{[^}]*\}`)

type envLookupFunc func(string) (string, bool)

func ExpandEnv(s string) string {
	return ExpandEnvWithLookup(s, os.LookupEnv)
}

func ExpandEnvWithLookup(s string, lookup envLookupFunc) string {
	if lookup == nil {
		lookup = os.LookupEnv
	}
	return envDefaultPattern.ReplaceAllStringFunc(s, func(match string) string {
		sub := envDefaultPattern.FindStringSubmatch(match)
		if sub == nil {

			debug.Log("config", "env expansion: unrecognized form %q left unchanged", match)
			return match
		}
		varName := sub[1]
		rest := match[2+len(varName):]
		colonForm := strings.HasPrefix(rest, ":-")
		dashForm := !colonForm && strings.HasPrefix(rest, "-")
		val, set := lookup(varName)
		switch {
		case colonForm:

			if set && val != "" {
				return val
			}
			return sub[2]
		case dashForm:

			if set {
				return val
			}
			return sub[2]
		default:

			if set {
				return val
			}
			return match
		}
	})
}

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

func WarnUnresolvedEnvRefs(raw map[string]interface{}) {
	if raw == nil {
		return
	}
	var walk func(interface{})
	walk = func(v interface{}) {
		switch val := v.(type) {
		case string:

			for _, m := range envAnyRefPattern.FindAllString(val, -1) {
				debug.Log("config", "unrecognized env reference %q left as literal value; supported forms are ${VAR} and ${VAR:-default}", m)
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
}

func HomeDir() string {
	return util.HomeDir()
}

func ConfigDir() string {
	guardRealHomeDir("ConfigDir()")

	home := HomeDir()
	if home == "" {
		return ""
	}
	return filepath.Join(home, ".ggcode")
}

func ConfigPath() string {

	return filepath.Join(ConfigDir(), "ggcode.yaml")
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

	if err := loadKeysEnvInto(func(key, val string) error {
		if _, exists := env[key]; !exists {
			env[key] = val
		}
		return nil
	}, KeysEnvPath()); err == nil {

	}

	if p := instanceKeysEnvPointer.Load(); p != nil {
		for k, v := range *p {
			env[k] = v
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

				if val, ok := env[key]; ok {
					return val, true
				}
				if val, ok := values[key]; ok {

					expanded := ExpandEnvWithLookup(val, func(k2 string) (string, bool) {
						v2, ok2 := env[k2]
						return v2, ok2
					})
					return expanded, true
				}
				return "", false
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

			for _, match := range envDefaultPattern.FindAllStringSubmatch(val, -1) {
				if len(match) >= 2 {
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

	if len(value) >= 2 && strings.HasPrefix(value, "\"") && strings.HasSuffix(value, "\"") {
		unquoted, err := strconv.Unquote(value)
		if err == nil {
			return name, unquoted, true
		}

		return name, value[1 : len(value)-1], true
	}
	if len(value) >= 2 && strings.HasPrefix(value, "'") && strings.HasSuffix(value, "'") {
		return name, value[1 : len(value)-1], true
	}
	if strings.Contains(value, "'") {
		fmt.Fprintf(os.Stderr, "warning: env variable %q has unmatched single-quote value %q, using raw value\n", name, value)
	}
	if idx := strings.Index(value, " #"); idx >= 0 {
		value = strings.TrimSpace(value[:idx])
	}
	return name, value, true
}
