package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
)

const (
	mentionPrefix      = "@"
	maxMentionFileSize = 100 * 1024 // 100KB
	maxMentions        = 5
)

// Mention represents a parsed @mention reference.
type Mention struct {
	Path  string // resolved file/directory path
	IsDir bool
}

// ParseMentions extracts @path references from user input.
// Returns the cleaned message (with @path removed) and list of mentions.
func ParseMentions(input string, workDir string) (string, []Mention, error) {
	var mentions []Mention
	cleaned := input

	// Find all @path tokens
	for {
		idx := strings.Index(cleaned, mentionPrefix)
		if idx < 0 {
			break
		}

		// Extract the path token (until whitespace or end)
		rest := cleaned[idx+1:]
		end := strings.IndexAny(rest, " \t\n\r")
		if end < 0 {
			end = len(rest)
		}
		token := rest[:end]
		if token == "" {
			break
		}

		// Remove from cleaned input
		removeLen := 1 + len(token)
		if end < len(rest) {
			removeLen++ // trailing whitespace
		}
		cleaned = cleaned[:idx] + cleaned[idx+removeLen:]

		if len(mentions) >= maxMentions {
			// Keep the remaining @mention text in the message
			continue
		}

		// Resolve path
		fullPath := filepath.Join(workDir, token)
		absPath, err := filepath.Abs(fullPath)
		if err != nil {
			continue
		}

		info, err := os.Stat(absPath)
		if err != nil {
			continue
		}

		mentions = append(mentions, Mention{
			Path:  absPath,
			IsDir: info.IsDir(),
		})
	}

	cleaned = strings.TrimSpace(cleaned)
	return cleaned, mentions, nil
}

// ExpandMentions reads file contents and directory listings for mentions,
// returning the expanded message text.
func ExpandMentions(input string, workDir string) (string, error) {
	cleaned, mentions, err := ParseMentions(input, workDir)
	if err != nil {
		return input, err
	}

	if len(mentions) == 0 {
		return input, nil
	}

	var expanded strings.Builder
	expanded.WriteString(cleaned)
	expanded.WriteString("\n\n[Referenced files]\n")

	for _, m := range mentions {
		if m.IsDir {
			// List directory contents
			entries, err := os.ReadDir(m.Path)
			if err != nil {
				expanded.WriteString(fmt.Sprintf("\n@%s (error reading directory: %v)\n", m.Path, err))
				continue
			}
			relPath, _ := filepath.Rel(workDir, m.Path)
			expanded.WriteString(fmt.Sprintf("\n@%s/ (directory):\n", relPath))
			for _, e := range entries {
				expanded.WriteString(fmt.Sprintf("  %s\n", e.Name()))
			}
		} else {
			// Read file contents
			info, err := os.Stat(m.Path)
			if err != nil {
				expanded.WriteString(fmt.Sprintf("\n@%s (error: %v)\n", m.Path, err))
				continue
			}
			if info.Size() > maxMentionFileSize {
				expanded.WriteString(fmt.Sprintf("\n@%s (skipped: file exceeds %d bytes)\n", m.Path, maxMentionFileSize))
				continue
			}
			data, err := os.ReadFile(m.Path)
			if err != nil {
				expanded.WriteString(fmt.Sprintf("\n@%s (error reading: %v)\n", m.Path, err))
				continue
			}
			relPath, _ := filepath.Rel(workDir, m.Path)
			expanded.WriteString(fmt.Sprintf("\n@%s:\n%s\n", relPath, string(data)))
		}
	}

	return expanded.String(), nil
}

// CompleteMention returns file/directory completions for an @mention prefix.
// prefix is the text after "@" (e.g., "internal/t" from "@internal/t").
func CompleteMention(prefix string, workDir string) []string {
	if prefix == "" {
		prefix = "."
	}
	fullPath := filepath.Join(workDir, prefix)
	dir := filepath.Dir(fullPath)
	partial := filepath.Base(fullPath)

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}

	var completions []string
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, partial) {
			// Skip hidden files (except . files explicitly referenced)
			if strings.HasPrefix(name, ".") && !strings.HasPrefix(partial, ".") {
				continue
			}
			displayPath := filepath.Join(filepath.Dir(prefix), name)
			if prefix == "." {
				displayPath = name
			}
			if e.IsDir() {
				displayPath += "/"
			}
			completions = append(completions, displayPath)
		}
	}
	return completions
}

// DetectMention returns true if the cursor is immediately after a "@" with a path fragment.
// It returns the path fragment after "@" for completion.
func DetectMention(ti textinput.Model) (active bool, prefix string) {
	value := ti.Value()
	cursor := ti.Position()

	// Find "@" before cursor
	for i := cursor - 1; i >= 0; i-- {
		if value[i] == '@' {
			// Check there's a whitespace or start-of-string before @
			if i == 0 || value[i-1] == ' ' || value[i-1] == '\t' || value[i-1] == '\n' {
				return true, value[i+1 : cursor]
			}
			break
		}
		if value[i] == ' ' || value[i] == '\t' || value[i] == '\n' {
			break
		}
	}
	return false, ""
}
