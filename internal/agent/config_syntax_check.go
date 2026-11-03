package agent

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"strings"

	"github.com/BurntSushi/toml"
	"gopkg.in/yaml.v3"
)

const maxConfigFileSize = 512 * 1024

func configSyntaxCheck(filePath, content string) string {
	content = strings.TrimSpace(content)
	if content == "" {
		return ""
	}
	if len(content) > maxConfigFileSize {
		return ""
	}

	ext := strings.ToLower(filepath_Ext(filePath))
	switch ext {
	case ".json":
		return validateJSON(filePath, content)
	case ".jsonc":
		return validateJSONC(filePath, content)
	case ".json5":

		return ""
	case ".yaml", ".yml":
		return validateYAML(filePath, content)
	case ".toml":
		return validateTOML(filePath, content)
	case ".xml", ".svg", ".xsd", ".xsl", ".xslt", ".rss":
		return validateXML(filePath, content)
	case ".plist":

		if strings.HasPrefix(content, "bplist") {
			return ""
		}
		return validateXML(filePath, content)
	default:
		return ""
	}
}

func validateJSON(filePath, content string) string {
	var raw json.RawMessage
	if err := json.Unmarshal([]byte(content), &raw); err != nil {
		return formatConfigError(filePath, "JSON", err)
	}
	return ""
}

func validateJSONC(filePath, content string) string {
	stripped, err := stripJSONComments(content)
	if err != nil {
		return formatConfigError(filePath, "JSONC", err)
	}
	return validateJSON(filePath, stripped)
}

func stripJSONComments(s string) (string, error) {
	var b strings.Builder
	b.Grow(len(s))

	inString := false
	escaped := false
	i := 0
	for i < len(s) {
		ch := s[i]

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

		if ch == '"' {
			inString = true
			b.WriteByte(ch)
			i++
			continue
		}

		if ch == '/' && i+1 < len(s) && s[i+1] == '/' {
			for i < len(s) && s[i] != '\n' {
				i++
			}
			continue
		}

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

				return "", fmt.Errorf("unclosed block comment")
			}
			continue
		}

		b.WriteByte(ch)
		i++
	}
	return b.String(), nil
}

func validateYAML(filePath, content string) string {
	var node yaml.Node
	if err := yaml.Unmarshal([]byte(content), &node); err != nil {
		return formatConfigError(filePath, "YAML", err)
	}

	if len(node.Content) > 0 && node.Content[0].Kind == yaml.MappingNode {
		var into map[string]interface{}
		if err := node.Decode(&into); err != nil {

			if strings.Contains(err.Error(), "already defined") {
				return fmt.Sprintf("YAML duplicate key in %s: %v — this causes data loss (later keys overwrite earlier ones), fix before proceeding", filePath, err)
			}
			return formatConfigError(filePath, "YAML", err)
		}
	}

	return ""
}

func validateTOML(filePath, content string) string {
	var raw map[string]interface{}
	if _, err := toml.Decode(content, &raw); err != nil {
		return formatConfigError(filePath, "TOML", err)
	}
	return ""
}

func validateXML(filePath, content string) string {
	dec := xml.NewDecoder(strings.NewReader(content))
	for {
		_, err := dec.Token()
		if err != nil {
			if err.Error() == "EOF" {
				break
			}
			return formatConfigError(filePath, "XML", err)
		}
	}
	return ""
}

func formatConfigError(filePath, format string, err error) string {
	msg := err.Error()

	if len(msg) > 200 {
		msg = msg[:200] + "..."
	}
	return fmt.Sprintf("%s syntax error in %s: %s — fix before proceeding, "+
		"this will cause failures at runtime.", format, filePath, msg)
}

func filepath_Ext(path string) string {

	for i := len(path) - 1; i >= 0 && path[i] != '/'; i-- {
		if path[i] == '.' {
			return path[i:]
		}
	}
	return ""
}
