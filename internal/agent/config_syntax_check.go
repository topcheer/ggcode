package agent

// Structured config file syntax validation for post-write integrity checks.
//
// Research basis: AI agents frequently produce malformed configuration files
// when editing JSON, YAML, TOML, or XML. These errors are silent at write time
// and only surface at runtime — causing application crashes, failed deployments,
// or mysterious behaviour that wastes debugging cycles.
//
// Competitive landscape:
//   - Claude Code: relies on LSP if available (most config files lack LSP)
//   - Cursor: lint-on-save for some formats, but not all
//   - Cline/OpenHands: no inline validation; caught by build/test if at all
//   - Aider: no config syntax validation
//
// ggcode's approach: extend the existing post-write integrity pipeline to parse
// structured config files using available parsers. This is zero-LLM-cost, runs
// in <1ms for typical config files, and catches the error inline so the agent
// can fix it immediately.
//
// Supported formats (using existing project dependencies):
//   - JSON  (encoding/json — stdlib)
//   - YAML  (gopkg.in/yaml.v3 — already in go.mod)
//   - TOML  (github.com/BurntSushi/toml — already in go.mod)
//   - XML   (encoding/xml — stdlib)
//   - JSONC / JSON5 (stripped comments before JSON parse)
//
// Threshold: only files smaller than 512KB are validated to avoid pathological
// parse times on very large auto-generated configs.

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"strings"

	"github.com/BurntSushi/toml"
	"gopkg.in/yaml.v3"
)

// maxConfigFileSize caps validation to avoid slow parses on huge generated files.
const maxConfigFileSize = 512 * 1024 // 512KB

// configSyntaxCheck validates structured config file content after a write.
// Returns a non-empty warning string if the content is syntactically invalid.
// Returns "" for valid content, unrecognized extensions, or oversized files.
func configSyntaxCheck(filePath, content string) string {
	content = strings.TrimSpace(content)
	if content == "" {
		return "" // empty file is handled by content-loss check
	}
	if len(content) > maxConfigFileSize {
		return "" // skip oversized files
	}

	ext := strings.ToLower(filepath_Ext(filePath))
	switch ext {
	case ".json":
		return validateJSON(filePath, content)
	case ".jsonc", ".json5":
		return validateJSONC(filePath, content)
	case ".yaml", ".yml":
		return validateYAML(filePath, content)
	case ".toml":
		return validateTOML(filePath, content)
	case ".xml", ".svg", ".xsd", ".xsl", ".xslt", ".rss":
		return validateXML(filePath, content)
	case ".plist":
		// #527 Bug E: binary plists ("bplist" magic) are exactly as legal as
		// XML plists and are what Xcode/defaults(1) routinely write. Routing
		// them into the XML parser produced confident nonsense ("illegal
		// character code U+0000") for perfectly valid files, with a message
		// that (wrongly) asserted runtime failures.
		if strings.HasPrefix(content, "bplist") {
			return ""
		}
		return validateXML(filePath, content)
	default:
		return ""
	}
}

// validateJSON parses JSON content and returns a warning on syntax errors.
func validateJSON(filePath, content string) string {
	var raw json.RawMessage
	if err := json.Unmarshal([]byte(content), &raw); err != nil {
		return formatConfigError(filePath, "JSON", err)
	}
	return ""
}

// validateJSONC parses JSON-with-comments by stripping comment lines before
// standard JSON parsing. This handles the common .jsonc/.json5 pattern of
// inline // comments and block /* */ comments.
func validateJSONC(filePath, content string) string {
	stripped, err := stripJSONComments(content)
	if err != nil {
		return formatConfigError(filePath, "JSONC", err)
	}
	return validateJSON(filePath, stripped)
}

// stripJSONComments removes // line comments and /* block comments */ from
// JSONC/JSON5 content, producing standard JSON suitable for encoding/json.
// Returns the stripped JSON and an error if an unclosed block comment is found.
func stripJSONComments(s string) (string, error) {
	var b strings.Builder
	b.Grow(len(s))

	inString := false
	escaped := false
	i := 0
	for i < len(s) {
		ch := s[i]

		// Inside a string: pass through verbatim (but track escape sequences).
		if inString {
			b.WriteByte(ch)
			if escaped {
				escaped = false
			} else if ch == '\\' {
				escaped = true
			} else if ch == '"' {
				inString = false
			}
			i++
			continue
		}

		// Outside a string: check for comment starts.
		if ch == '"' {
			inString = true
			b.WriteByte(ch)
			i++
			continue
		}

		// Line comment: skip to end of line.
		if ch == '/' && i+1 < len(s) && s[i+1] == '/' {
			for i < len(s) && s[i] != '\n' {
				i++
			}
			continue
		}

		// Block comment: skip to closing */.
		if ch == '/' && i+1 < len(s) && s[i+1] == '*' {
			i += 2
			foundEnd := false
			for i+1 < len(s) {
				if s[i] == '*' && s[i+1] == '/' {
					i += 2
					foundEnd = true
					break
				}
				i++
			}
			if !foundEnd {
				// Unclosed block comment - signal error by returning a marker
				return "", fmt.Errorf("unclosed block comment")
			}
			continue
		}

		b.WriteByte(ch)
		i++
	}
	return b.String(), nil
}

// validateYAML parses YAML content and returns a warning on syntax errors.
// Also checks for duplicate keys in mapping nodes.
func validateYAML(filePath, content string) string {
	// First check for duplicate keys by parsing the raw YAML text
	// yaml.Unmarshal silently merges duplicates, so we need a different approach
	if dupKeys := findYAMLDuplicateKeys(content); len(dupKeys) > 0 {
		return fmt.Sprintf("YAML duplicate key(s) in %s: %s — fix before proceeding, "+
			"this causes data loss (later keys overwrite earlier ones).",
			filePath, strings.Join(dupKeys, ", "))
	}

	var node yaml.Node
	if err := yaml.Unmarshal([]byte(content), &node); err != nil {
		return formatConfigError(filePath, "YAML", err)
	}

	return ""
}

// findYAMLDuplicateKeys scans YAML text for duplicate keys at the same context level.
// Returns a slice of duplicate key names found.
// Uses a stack-based approach to track nesting context.
func findYAMLDuplicateKeys(content string) []string {
	lines := strings.Split(content, "\n")

	// Stack of maps to track keys at each nesting level
	// Each level has its own set of keys
	var stack []map[string]struct{}
	stack = append(stack, make(map[string]struct{}))

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue // Skip empty lines and comments
		}

		// Count leading spaces to determine indentation level
		indent := 0
		for i := 0; i < len(line) && line[i] == ' '; i++ {
			indent++
		}

		// Adjust stack depth based on indentation
		// Assuming 2 spaces per indent level (YAML convention)
		depth := indent / 2
		for len(stack) > depth+1 {
			stack = stack[:len(stack)-1] // Pop to go up
		}
		for len(stack) < depth+1 {
			stack = append(stack, make(map[string]struct{})) // Push to go down
		}

		// Skip list items
		if strings.HasPrefix(trimmed, "-") || strings.HasPrefix(trimmed, "[") {
			continue
		}

		// Find the first colon
		colonIdx := strings.Index(trimmed, ":")
		if colonIdx <= 0 {
			continue // No key:value pair on this line
		}

		// Extract the key (everything before the first colon)
		key := strings.TrimSpace(trimmed[:colonIdx])

		// Skip if key starts with special chars (not a valid YAML key)
		if key == "" || strings.HasPrefix(key, "\"") || strings.HasPrefix(key, "'") {
			continue
		}

		// Check for duplicate at the current nesting level
		currentLevel := stack[len(stack)-1]
		if _, exists := currentLevel[key]; exists {
			return []string{key}
		}
		currentLevel[key] = struct{}{}
	}

	return nil
}

// validateTOML parses TOML content and returns a warning on syntax errors.
func validateTOML(filePath, content string) string {
	var raw map[string]interface{}
	if _, err := toml.Decode(content, &raw); err != nil {
		return formatConfigError(filePath, "TOML", err)
	}
	return ""
}

// validateXML parses XML content and returns a warning on syntax errors.
func validateXML(filePath, content string) string {
	dec := xml.NewDecoder(strings.NewReader(content))
	for {
		_, err := dec.Token()
		if err != nil {
			if err.Error() == "EOF" {
				break // valid: reached end cleanly
			}
			return formatConfigError(filePath, "XML", err)
		}
	}
	return ""
}

// formatConfigError produces a user/agent-facing warning for a config parse error.
func formatConfigError(filePath, format string, err error) string {
	msg := err.Error()
	// Truncate very long error messages.
	if len(msg) > 200 {
		msg = msg[:200] + "..."
	}
	return fmt.Sprintf("%s syntax error in %s: %s — fix before proceeding, "+
		"this will cause failures at runtime.", format, filePath, msg)
}

// filepath_Ext is a thin wrapper to keep imports clean. We use filepath.Ext
// but alias it to allow easy testing/mocking.
func filepath_Ext(path string) string {
	// Inline implementation to avoid importing filepath in this file
	// (keep the module focused on parsing, not path handling).
	for i := len(path) - 1; i >= 0 && path[i] != '/'; i-- {
		if path[i] == '.' {
			return path[i:]
		}
	}
	return ""
}
