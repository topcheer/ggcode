package commands

// Command represents a user-defined slash command loaded from a .md file.
type Command struct {
	Name        string // e.g. "review-pr" (from review-pr.md)
	Template    string // file content, the prompt template
	Description string // first line or empty
}

// Expand replaces template variables in the command template.
// Supported: $FILE (current file), $DIR (current directory).
func (c *Command) Expand(vars map[string]string) string {
	result := c.Template
	for k, v := range vars {
		result = replaceVar(result, "$"+k, v)
	}
	return result
}

func replaceVar(s, key, value string) string {
	// Simple string replacement
	result := ""
	for i := 0; i < len(s); {
		if i+len(key) <= len(s) && s[i:i+len(key)] == key {
			result += value
			i += len(key)
		} else {
			result += string(s[i])
			i++
		}
	}
	return result
}
