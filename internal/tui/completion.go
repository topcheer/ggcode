package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/topcheer/ggcode/internal/commands"
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
	var dir string
	var partial string

	if prefix == "" {
		// No prefix: list contents of workDir itself
		dir = workDir
		partial = ""
	} else if strings.HasSuffix(prefix, "/") {
		// Trailing slash: user wants to browse INTO this directory
		dir = filepath.Join(workDir, prefix)
		partial = ""
	} else {
		fullPath := filepath.Join(workDir, prefix)
		dir = filepath.Dir(fullPath)
		partial = filepath.Base(fullPath)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}

	var completions []string
	for _, e := range entries {
		name := e.Name()
		if partial != "" && !strings.HasPrefix(name, partial) {
			continue
		}
		// Skip hidden files (except . files explicitly referenced)
		if strings.HasPrefix(name, ".") && !strings.HasPrefix(partial, ".") {
			continue
		}
		var displayPath string
		if prefix == "" {
			displayPath = name
		} else {
			displayPath = filepath.Join(filepath.Dir(prefix), name)
		}
		if e.IsDir() {
			displayPath += "/"
		}
		completions = append(completions, displayPath)
	}
	return completions
}

// SlashCommands is the list of all available slash commands.
var SlashCommands = []string{
	"/help", "/?", "/sessions", "/resume", "/model", "/provider", "/impersonate",
	"/clear", "/im", "/qq", "/telegram", "/tg", "/pc", "/discord", "/feishu", "/lark", "/slack", "/dingtalk", "/ding", "/wechat", "/wecom", "/mattermost", "/mm", "/matrix", "/signal", "/irc", "/nostr", "/twitch", "/whatsapp", "/wa",
	"/mcp", "/memory", "/undo", "/checkpoints", "/plugins",
	"/inspector", "/image",
	"/chat", "/nick", "/init", "/harness", "/exit", "/quit",
	"/compact", "/todo", "/status", "/stats", "/knight", "/tmux", "/update", "/restart", "/lang", "/skills", "/stream", "/share", "/tunnel", "/unshare",
	"/diff", "/hooks", "/cost", "/review",
}

// SlashCommandDescriptions provides short descriptions for slash commands.
var SlashCommandDescriptions = map[string]string{
	"/help":        "Show help message",
	"/?":           "Show help message",
	"/sessions":    "List saved sessions",
	"/resume":      "Resume a previous session",
	"/model":       "Open model panel",
	"/impersonate": "Set client identity (impersonate CLI tools)",
	"/provider":    "Open provider manager",
	"/clear":       "Clear conversation",
	"/im":          "Open unified IM channels panel",
	"/qq":          "Manage QQ channel binding",
	"/telegram":    "Manage Telegram channel binding",
	"/tg":          "Manage Telegram channel binding",
	"/pc":          "Manage PC channel binding",
	"/discord":     "Manage Discord channel binding",
	"/feishu":      "Manage Feishu channel binding",
	"/lark":        "Manage Feishu channel binding",
	"/slack":       "Manage Slack channel binding",
	"/dingtalk":    "Manage DingTalk channel binding",
	"/wechat":      "Manage WeChat (iLink) channel binding",
	"/ding":        "Manage DingTalk channel binding",
	"/wecom":       "Manage WeCom (Enterprise WeChat) channel binding",
	"/mattermost":  "Manage Mattermost channel binding",
	"/mm":          "Manage Mattermost channel binding",
	"/matrix":      "Manage Matrix channel binding",
	"/signal":      "Manage Signal channel binding",
	"/irc":         "Manage IRC channel binding",
	"/nostr":       "Manage Nostr channel binding",
	"/twitch":      "Manage Twitch channel binding",
	"/whatsapp":    "Manage WhatsApp channel binding",
	"/wa":          "Alias for /whatsapp",
	"/mcp":         "Show MCP servers",
	"/memory":      "Manage memory",
	"/undo":        "Undo last file edit",
	"/checkpoints": "List checkpoints",
	"/plugins":     "List loaded plugins",
	"/inspector":   "Open inspector panel (sessions|checkpoints|memory|plugins|config|status)",
	"/chat":        "Open LAN chat panel",
	"/nick":        "Set LAN chat nickname, role, and team",
	"/image":       "Attach an image",
	"/init":        "Create GGCODE.md",
	"/harness":     "Run harness workflow commands",
	"/exit":        "Exit ggcode",
	"/quit":        "Exit ggcode",
	"/compact":     "Compress conversation history",
	"/todo":        "View/manage todo list",
	"/status":      "Show current status",
	"/stats":       "Open session stats panel",
	"/knight":      "Knight auto-evolution commands",
	"/tmux":        "Manage tmux session and panes",
	"/update":      "Update ggcode to the latest release",
	"/restart":     "Restart ggcode (picks up latest binary)",
	"/lang":        "Switch interface language",
	"/skills":      "Browse available skills",
	"/stream":      "Live stream TUI to platforms (YouTube, Bilibili, etc.)",
	"/share":       "Share session to mobile (QR code tunnel)",
	"/unshare":     "Stop sharing (disconnect mobile)",
	"/tunnel":      "Share session to mobile (QR code tunnel)",
	"/diff":        "Show git diff in chat (supports --cached, <file>, --stat)",
	"/hooks":       "Show configured hooks (all events, types, match patterns)",
	"/cost":        "Show session token usage and estimated cost",
	"/review":      "AI code review of current git diff (bugs, security, races)",
}

// SlashCommandPlaceholders maps commands that accept optional arguments.
// When Tab-completing these commands, the input is filled with the command
// plus a trailing space and the placeholder is shown as a hint.
// Commands NOT in this map are executed immediately on Tab completion.
var SlashCommandPlaceholders = map[string]string{
	"/model":       "<model-name>",
	"/provider":    "<vendor> [endpoint]",
	"/impersonate": "<cli-tool>",
	"/harness":     "<subcommand>",
	"/inspector":   "<sessions|checkpoints|memory|plugins|config|status>",
	"/chat":        "(@mention message | /nick <name>[@role])",
	"/nick":        "<name>[@role][@team]",
	"/knight":      "<on|off|status|run|skills|budget|...>",
	"/tmux":        "<enter [session] [--setup [layout]]|status|split|test|build|verify|popup|list|logs|layouts|layout|setup|save-layout|delete-layout|rename-layout|refresh|restore|rerun|prune|capture|stop|close|focus>",
	"/resume":      "<session-id>",
	"/lang":        "<en|zh-CN>",
	"/memory":      "<subcommand>",
	"/image":       "<path>",
	"/skills":      "<skill-name>",
	"/init":        "[path]",
	"/im":          "<subcommand>",
	"/wechat":      "<subcommand>",
	"/stream":      "<start|stop|status|config>",
	"/share":       "<start|stop|status>",
	"/unshare":     "(no args)",
	"/tunnel":      "<start|stop|status>",
	"/wecom":       "<subcommand>",
	"/mattermost":  "<subcommand>",
	"/mm":          "<subcommand>",
	"/matrix":      "<subcommand>",
	"/signal":      "<subcommand>",
	"/irc":         "<subcommand>",
	"/nostr":       "<subcommand>",
	"/twitch":      "<subcommand>",
	"/whatsapp":    "",
	"/wa":          "",
	"/restart":     "[debug]",
	"/checkpoints": "[list|restore]",
	"/clear":       "",
	"/compact":     "",
	"/ding":        "<subcommand>",
	"/dingtalk":    "<subcommand>",
	"/discord":     "<subcommand>",
	"/exit":        "",
	"/feishu":      "<subcommand>",
	"/help":        "[command]",
	"/lark":        "<subcommand>",
	"/mcp":         "<subcommand>",
	"/pc":          "<subcommand>",
	"/plugins":     "<subcommand>",
	"/qq":          "<subcommand>",
	"/quit":        "",
	"/sessions":    "[filter]",
	"/slack":       "<subcommand>",
	"/status":      "",
	"/stats":       "",
	"/telegram":    "<subcommand>",
	"/tg":          "<subcommand>",
	"/todo":        "<subcommand>",
	"/undo":        "",
	"/update":      "[check|force]",
	"/diff":        "[--cached|--stat|<file>]",
	"/review":      "[--cached|--staged]",
	"/hooks":       "",
	"/cost":        "",
}

// CompleteSlashCommand returns matching slash commands for a given prefix.
func CompleteSlashCommand(prefix string, customCmds map[string]*commands.Command) []string {
	var matches []string
	for _, cmd := range SlashCommands {
		if strings.HasPrefix(cmd, prefix) {
			matches = append(matches, cmd)
		}
	}
	for _, cmd := range customCmds {
		if !cmd.UserSlashVisible() {
			continue
		}
		name := cmd.SlashName()
		if strings.HasPrefix(name, prefix) {
			matches = append(matches, name)
		}
	}
	slices.Sort(matches)
	matches = slices.Compact(matches)
	return matches
}

// DetectSlashCommand returns true if the cursor is at a slash command position.
// It returns the command fragment after "/" for completion.
func DetectSlashCommand(value string, cursor int) (active bool, prefix string) {
	if cursor < 1 || cursor > len(value) {
		return false, ""
	}

	cursor = clampUTF8Cursor(value, cursor)
	if cursor < 1 {
		return false, ""
	}

	// Find the start of the current word by scanning backwards for whitespace.
	// Since whitespace chars (space, tab) are single-byte ASCII, we can safely
	// compare individual bytes — but we must not stop mid-UTF8 sequence.
	wordStart := cursor
	for wordStart > 0 {
		b := value[wordStart-1]
		if b == ' ' || b == '\t' {
			break
		}
		// Skip over UTF-8 continuation bytes (0x80-0xBF) so we don't
		// misidentify a continuation byte as a non-whitespace char.
		if b&0xC0 == 0x80 {
			wordStart--
			continue
		}
		wordStart--
	}

	if wordStart >= len(value) || value[wordStart] != '/' {
		return false, ""
	}

	// Ensure "/" is at the start of input or after whitespace.
	if wordStart > 0 {
		prev := value[wordStart-1]
		if prev != ' ' && prev != '\t' {
			return false, ""
		}
	}

	// Extract the prefix after "/".
	end := cursor
	if end > len(value) {
		end = len(value)
	}
	if wordStart+1 > end {
		return false, ""
	}
	prefix = value[wordStart+1 : end]
	return true, prefix
}

// DetectMention returns true if the cursor is immediately after a "@" with a path fragment.
// It returns the path fragment after "@" for completion.
func DetectMention(value string, cursor int) (active bool, prefix string) {
	cursor = clampUTF8Cursor(value, cursor)
	if cursor <= 0 {
		return false, ""
	}
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

func clampUTF8Cursor(value string, cursor int) int {
	if cursor < 0 {
		return 0
	}
	if cursor > len(value) {
		return len(value)
	}
	for cursor < len(value) && value[cursor]&0xC0 == 0x80 {
		cursor++
	}
	return cursor
}
