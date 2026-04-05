package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"

	"github.com/topcheer/ggcode/internal/debug"
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

		// Prevent path traversal: resolved path must stay within workDir
		rel, err := filepath.Rel(workDir, absPath)
		if err != nil || strings.HasPrefix(rel, "..") {
			debug.Log("completion", "@mention path traversal blocked: %s -> %s", token, absPath)
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

// SlashCommands is the list of all available slash commands.
var SlashCommands = []string{
	"/help", "/?", "/sessions", "/resume", "/export", "/model", "/provider",
	"/clear", "/mcp", "/memory", "/undo", "/checkpoints", "/allow", "/plugins",
	"/image", "/fullscreen", "/mode", "/exit", "/quit", "/agents", "/agent",
	"/compact", "/todo", "/bug", "/config", "/status", "/lang",
}

// SlashCommandDescriptions provides short descriptions for slash commands.
var SlashCommandDescriptions = map[string]string{
	"/help":        "Show help message",
	"/?":           "Show help message",
	"/sessions":    "List saved sessions",
	"/resume":      "Resume a previous session",
	"/export":      "Export session to markdown",
	"/model":       "Switch model",
	"/provider":    "Open provider manager",
	"/clear":       "Clear conversation",
	"/mcp":         "Show MCP servers",
	"/memory":      "Manage memory",
	"/undo":        "Undo last file edit",
	"/checkpoints": "List checkpoints",
	"/allow":       "Always allow a tool",
	"/plugins":     "List loaded plugins",
	"/image":       "Attach an image",
	"/fullscreen":  "Toggle fullscreen",
	"/mode":        "Set agent mode",
	"/exit":        "Exit ggcode",
	"/quit":        "Exit ggcode",
	"/agents":      "List sub-agents",
	"/agent":       "Sub-agent details",
	"/compact":     "Compress conversation history",
	"/todo":        "View/manage todo list",
	"/bug":         "Report a bug",
	"/config":      "View/modify configuration",
	"/status":      "Show current status",
	"/lang":        "Switch interface language",
}

// CompleteSlashCommand returns matching slash commands for a given prefix.
func CompleteSlashCommand(prefix string) []string {
	var matches []string
	for _, cmd := range SlashCommands {
		if strings.HasPrefix(cmd, prefix) {
			matches = append(matches, cmd)
		}
	}
	return matches
}

// DetectSlashCommand returns true if the cursor is at a slash command position.
// It returns the command fragment after "/" for completion.
func DetectSlashCommand(ti textinput.Model) (active bool, prefix string) {
	value := ti.Value()
	cursor := ti.Position()

	if cursor < 1 {
		return false, ""
	}

	// Must start with "/" at position 0 (or after a space at position 0)
	// Find the start of the current word
	wordStart := cursor
	for wordStart > 0 && value[wordStart-1] != ' ' && value[wordStart-1] != '\t' {
		wordStart--
	}

	if wordStart >= len(value) || value[wordStart] != '/' {
		return false, ""
	}

	// Ensure "/" is at the start of input or after whitespace
	if wordStart > 0 && value[wordStart-1] != ' ' && value[wordStart-1] != '\t' {
		return false, ""
	}

	prefix = value[wordStart+1 : cursor]
	return true, prefix
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
