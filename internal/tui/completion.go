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
	maxMentionFileSize = 100 * 1024
	maxMentions        = 5
)

type Mention struct {
	Path  string
	IsDir bool
}

func isSpaceByte(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\r'
}

func ParseMentions(input string, workDir string) (string, []Mention, error) {

	realWorkDir := workDir
	if resolved, err := filepath.EvalSymlinks(workDir); err == nil {
		realWorkDir = resolved
	}
	var mentions []Mention
	cleaned := input

	searchFrom := 0
	for {
		idx := strings.Index(cleaned[searchFrom:], mentionPrefix)
		if idx < 0 {
			break
		}

		abs := searchFrom + idx
		if abs > 0 && !isSpaceByte(cleaned[abs-1]) {
			searchFrom = abs + 1
			continue
		}
		idx += searchFrom

		rest := cleaned[idx+1:]
		end := strings.IndexAny(rest, " \t\n\r")
		if end < 0 {
			end = len(rest)
		}
		token := rest[:end]
		if token == "" {

			searchFrom = idx + 1
			continue
		}

		if len(mentions) >= maxMentions {

			searchFrom = idx + 1 + len(token)
			continue
		}

		fullPath := filepath.Join(workDir, token)
		absPath, err := filepath.Abs(fullPath)
		if err != nil {
			searchFrom = idx + 1 + len(token)
			continue
		}

		realPath := absPath
		if resolved, rerr := filepath.EvalSymlinks(absPath); rerr == nil {
			realPath = resolved
		} else {
			debug.Log("completion", "@mention path unresolvable (dangling link?): %s", token)
			searchFrom = idx + 1 + len(token)
			continue
		}
		rel, err := filepath.Rel(realWorkDir, realPath)

		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			debug.Log("completion", "@mention path traversal blocked (symlink escape): %s -> %s", token, realPath)
			searchFrom = idx + 1 + len(token)
			continue
		}

		info, err := os.Stat(absPath)
		if err != nil {
			searchFrom = idx + 1 + len(token)
			continue
		}

		removeLen := 1 + len(token)
		if end < len(rest) {
			removeLen++
		}
		cleaned = cleaned[:idx] + cleaned[idx+removeLen:]
		searchFrom = idx

		mentions = append(mentions, Mention{
			Path:  absPath,
			IsDir: info.IsDir(),
		})
	}

	cleaned = strings.TrimSpace(cleaned)
	return cleaned, mentions, nil
}

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

func CompleteMention(prefix string, workDir string, fuzzyFallback FuzzyFileSearcher) []string {

	if resolved, err := filepath.EvalSymlinks(workDir); err == nil {
		workDir = resolved
	}
	var dir string
	var partial string

	if prefix == "" {

		dir = workDir
		partial = ""
	} else if strings.HasSuffix(prefix, "/") {

		dir = filepath.Join(workDir, prefix)
		partial = ""
	} else {
		fullPath := filepath.Join(workDir, prefix)
		dir = filepath.Dir(fullPath)
		partial = filepath.Base(fullPath)
	}

	realDir := dir
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		realDir = resolved
	} else {
		return nil
	}
	if rel, err := filepath.Rel(workDir, realDir); err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return nil
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

	if len(completions) == 0 && prefix != "" && !strings.Contains(prefix, "/") {
		if fuzzyFallback != nil {
			fuzzyResults := fuzzyFallback(prefix, 30)
			completions = append(completions, fuzzyResults...)
		}

		if len(completions) == 0 {
			fuzzyResults := recursiveFuzzySearch(workDir, prefix, 4, 30)
			completions = append(completions, fuzzyResults...)
		}
	}

	return completions
}

type FuzzyFileSearcher func(query string, maxResults int) []string

func recursiveFuzzySearch(root, query string, maxDepth, maxResults int) []string {
	query = strings.ToLower(query)
	var results []string
	count := 0

	filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || count >= maxResults {
			return nil
		}

		rel, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}

		base := d.Name()
		if d.IsDir() {
			if base == ".git" || base == "node_modules" || base == "vendor" ||
				base == ".ggcode" || base == "dist" || base == "build" ||
				base == "target" || base == ".next" || base == "__pycache__" {
				return filepath.SkipDir
			}

			depth := strings.Count(rel, string(filepath.Separator))
			if depth >= maxDepth {
				return filepath.SkipDir
			}
			return nil
		}

		if fuzzySubsequenceMatch(strings.ToLower(base), query) {
			results = append(results, rel)
			count++
		}
		return nil
	})

	return results
}

func fuzzySubsequenceMatch(s, query string) bool {
	if query == "" {
		return true
	}
	si, qi := 0, 0
	for si < len(s) && qi < len(query) {
		if s[si] == query[qi] {
			qi++
		}
		si++
	}
	return qi == len(query)
}

var SlashCommands = []string{
	"/help", "/?", "/sessions", "/resume", "/model", "/provider", "/impersonate",
	"/clear", "/im", "/qq", "/telegram", "/tg", "/pc", "/discord", "/feishu", "/lark", "/slack", "/dingtalk", "/ding", "/wechat", "/wecom", "/mattermost", "/mm", "/matrix", "/signal", "/irc", "/nostr", "/twitch", "/whatsapp", "/wa",
	"/continue-task", "/mcp", "/memory", "/undo", "/undo-run", "/files", "/tools", "/checkpoints", "/plugins",
	"/inspector", "/image", "/mode", "/allow", "/export", "/bug", "/config", "/reflect", "/rules",
	"/chat", "/nick", "/init", "/exit", "/quit",
	"/compact", "/todo", "/status", "/stats", "/knight", "/tmux", "/update", "/restart", "/lang", "/skills", "/stream", "/share", "/tunnel", "/unshare",
	"/diff", "/hooks", "/cost", "/usage", "/commit", "/review", "/copy", "/context", "/debug",
	"/retry",
	"/edit",
	"/regenerate", "/regen", "/branch", "/fork", "/branches", "/branch-switch", "/rewind",
	"/cron",
	"/title",
	"/pin-session", "/unpin-session", "/tag", "/untag", "/tags",
	"/search",

	"/redo", "/notify", "/export-trace", "/runreport", "/traj", "/export-training", "/why", "/evidence", "/guidance",

	"/pin", "/style", "/goal",

	"/interventions", "/refusals",
}

var SlashCommandDescriptions = map[string]string{
	"/help":            "Show help message",
	"/retry":           "Retry last submission after error",
	"/edit":            "Edit and resend last submission",
	"/?":               "Show help message",
	"/sessions":        "List saved sessions",
	"/search":          "Search message content across all sessions",
	"/resume":          "Resume a previous session",
	"/continue-task":   "Resume the task interrupted by a crash",
	"/model":           "Open model panel",
	"/impersonate":     "Set client identity (impersonate CLI tools)",
	"/provider":        "Open provider manager",
	"/clear":           "Clear conversation",
	"/im":              "Open unified IM channels panel",
	"/qq":              "Manage QQ channel binding",
	"/telegram":        "Manage Telegram channel binding",
	"/tg":              "Manage Telegram channel binding",
	"/pc":              "Manage PC channel binding",
	"/discord":         "Manage Discord channel binding",
	"/feishu":          "Manage Feishu channel binding",
	"/lark":            "Manage Feishu channel binding",
	"/slack":           "Manage Slack channel binding",
	"/dingtalk":        "Manage DingTalk channel binding",
	"/wechat":          "Manage WeChat (iLink) channel binding",
	"/ding":            "Manage DingTalk channel binding",
	"/wecom":           "Manage WeCom (Enterprise WeChat) channel binding",
	"/mattermost":      "Manage Mattermost channel binding",
	"/mm":              "Manage Mattermost channel binding",
	"/matrix":          "Manage Matrix channel binding",
	"/signal":          "Manage Signal channel binding",
	"/irc":             "Manage IRC channel binding",
	"/nostr":           "Manage Nostr channel binding",
	"/twitch":          "Manage Twitch channel binding",
	"/whatsapp":        "Manage WhatsApp channel binding",
	"/wa":              "Alias for /whatsapp",
	"/mcp":             "Show MCP servers",
	"/memory":          "Manage memory",
	"/undo":            "Undo last file edit",
	"/undo-run":        "Revert ALL file changes from the last agent run",
	"/redo":            "Redo the last undone file edit (re-apply checkpoint)",
	"/runreport":       "Evaluate this session's trajectory (offline scorecard)",
	"/traj":            "List / purge past-run learnings injected into your prompt",
	"/export-training": "Export this session as a redacted RLVR/SFT training sample (JSONL)",
	"/files":           "Show files modified by agent",
	"/tools":           "List available tools",
	"/checkpoints":     "List checkpoints",
	"/plugins":         "List loaded plugins",
	"/inspector":       "Open inspector panel (sessions|checkpoints|memory|plugins|config|status)",
	"/interventions":   "Show user-takeover history (/interventions clear wipes it)",
	"/refusals":        "Show persisted user refusals enforced on write tools (/refusals clear wipes them)",
	"/chat":            "Open LAN chat panel",
	"/nick":            "Set LAN chat nickname, role, and team",
	"/image":           "Attach an image",
	"/init":            "Create AGENTS.md",
	"/exit":            "Exit ggcode",
	"/quit":            "Exit ggcode",
	"/compact":         "Compress conversation history",
	"/todo":            "View/manage todo list",
	"/status":          "Show current status",
	"/stats":           "Open session stats panel",
	"/knight":          "Knight auto-evolution commands",
	"/tmux":            "Manage tmux session and panes",
	"/update":          "Update ggcode to the latest release",
	"/restart":         "Restart ggcode (picks up latest binary)",
	"/lang":            "Switch interface language",
	"/skills":          "Browse available skills",
	"/stream":          "Live stream TUI to platforms (YouTube, Bilibili, etc.)",
	"/share":           "Share session to mobile (QR code tunnel)",
	"/unshare":         "Stop sharing (disconnect mobile)",
	"/tunnel":          "Share session to mobile (QR code tunnel)",
	"/diff":            "Show git diff in chat (supports --cached, <file>, --stat)",
	"/hooks":           "Show configured hooks (all events, types, match patterns)",
	"/cost":            "Show session token usage and estimated cost",
	"/usage":           "Show active vendor balance and usage windows",
	"/debug":           "Export debug logs to file (e.g. /debug export agent)",
	"/commit":          "Stage changes and generate an AI commit message from the diff",
	"/review":          "AI code review of current git diff (bugs, security, races)",
	"/copy":            "Copy last assistant response to clipboard",
	"/context":         "Show context usage + provenance (external/injection-flagged)",
	"/notify":          "View or set notification preferences (mode, bell, desktop)",
	"/regenerate":      "Regenerate last AI response (discard and re-run)",
	"/regen":           "Regenerate last AI response (discard and re-run)",
	"/cron":            "Manage scheduled jobs (list, pause, resume)",
	"/branch":          "Fork current conversation into a new session",
	"/rewind":          "Rewind last N turns: revert their file edits and continue in a forked session",
	"/branches":        "List the branch tree around this session (parent, siblings, children)",
	"/branch-switch":   "Switch to a related session in the current branch tree",
	"/title":           "Set or show the session title (e.g. /title My new title)",
	"/pin-session":     "Pin session (protected from cleanup, lists first in /sessions)",
	"/pin":             "Show or manage pinned context blocks (survive compaction)",
	"/style":           "Show, set, or list output style presets (/style list)",
	"/goal":            "Show, set, or clear persistent autopilot goal",
	"/unpin-session":   "Unpin the current session",
	"/tag":             "Add tags to the session (e.g. /tag rust perf)",
	"/untag":           "Remove tags from the session (e.g. /untag rust)",
	"/tags":            "Show the current session's tags",
	"/fork":            "Fork current conversation into a new session",
	"/mode":            "Set permission mode (supervised|bypass)",
	"/allow":           "Always allow a specific tool",
	"/export":          "Export session to markdown file",
	"/export-trace":    "Export session execution trace as JSON (--otel for OTLP/GenAI format)",
	"/why":             "Explain recent tool-call decisions: paired reasoning, results, error anchors",
	"/guidance":        "Guidance-detector stats, <tag> drill-down, and suppress/reset <tag> overrides (from guidance-stats.jsonl)",
	"/evidence":        "Cite the last answer's claims against recorded tool evidence (claim to source)",
	"/bug":             "Report a bug with diagnostics",
	"/config":          "View/modify configuration",
	"/reflect":         "Show recent run reflections",
	"/rules":           "Manage ratchet rules",
}

var SlashCommandPlaceholders = map[string]string{
	"/model":         "<model-name>",
	"/provider":      "<vendor> [endpoint]",
	"/impersonate":   "<cli-tool>",
	"/inspector":     "<sessions|checkpoints|memory|plugins|config|status>",
	"/chat":          "(@mention message | /nick <name>[@role])",
	"/nick":          "<name>[@role][@team]",
	"/knight":        "<on|off|status|run|skills|budget|...>",
	"/tmux":          "<enter [session] [--setup [layout]]|status|split|test|build|verify|popup|list|logs|layouts|layout|setup|save-layout|delete-layout|rename-layout|refresh|restore|rerun|prune|capture|stop|close|focus>",
	"/resume":        "<session-id>",
	"/continue-task": "",
	"/lang":          "<en|zh-CN>",
	"/memory":        "<subcommand>",
	"/reflect":       "",
	"/rules":         "",
	"/image":         "<path>",
	"/skills":        "<skill-name>",
	"/init":          "[path]",
	"/im":            "<subcommand>",
	"/wechat":        "<subcommand>",
	"/stream":        "<start|stop|status|config>",
	"/share":         "<start|stop|status>",
	"/unshare":       "(no args)",
	"/tunnel":        "<start|stop|status>",
	"/wecom":         "<subcommand>",
	"/mattermost":    "<subcommand>",
	"/mm":            "<subcommand>",
	"/matrix":        "<subcommand>",
	"/signal":        "<subcommand>",
	"/irc":           "<subcommand>",
	"/nostr":         "<subcommand>",
	"/twitch":        "<subcommand>",
	"/whatsapp":      "",
	"/wa":            "",
	"/restart":       "[debug]",
	"/checkpoints":   "[list|restore]",
	"/clear":         "",
	"/compact":       "",
	"/ding":          "<subcommand>",
	"/dingtalk":      "<subcommand>",
	"/discord":       "<subcommand>",
	"/exit":          "",
	"/feishu":        "<subcommand>",
	"/help":          "[command]",
	"/lark":          "<subcommand>",
	"/mcp":           "<subcommand>",
	"/cron":          "<list|get|pause|resume|pauseall|resumeall>",
	"/pc":            "<subcommand>",
	"/plugins":       "<subcommand>",
	"/qq":            "<subcommand>",
	"/quit":          "",
	"/sessions":      "[filter]",
	"/slack":         "<subcommand>",
	"/status":        "",
	"/stats":         "",
	"/redo":          "",
	"/telegram":      "<subcommand>",
	"/tg":            "<subcommand>",
	"/todo":          "<subcommand>",
	"/undo":          "",
	"/undo-run":      "",
	"/update":        "[check|force]",
	"/diff":          "[--cached|--stat|<file>]",
	"/review":        "[--cached|--staged]",
	"/retry":         "",
	"/edit":          "",
	"/hooks":         "",
	"/cost":          "[all]",
	"/copy":          "",
	"/context":       "",
	"/notify":        "[mode <all|long|errors|off>] [bell <on|off>] [desktop <on|off>]",
	"/regenerate":    "",
	"/regen":         "",
	"/branch":        "",
	"/branches":      "",
	"/branch-switch": "<session-id>",
	"/title":         "<new title>",
	"/pin-session":   "",
	"/pin":           "[<subcommand>]",
	"/style":         "<style-name|list>",
	"/goal":          "[<goal text>|clear]",
	"/unpin-session": "",
	"/tag":           "<tag>...",
	"/untag":         "<tag>...",
	"/tags":          "",
	"/fork":          "",
}

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

func DetectSlashCommand(value string, cursor int) (active bool, prefix string) {
	if cursor < 1 || cursor > len(value) {
		return false, ""
	}

	cursor = clampUTF8Cursor(value, cursor)
	if cursor < 1 {
		return false, ""
	}

	wordStart := cursor
	for wordStart > 0 {
		b := value[wordStart-1]
		if b == ' ' || b == '\t' {
			break
		}

		if b&0xC0 == 0x80 {
			wordStart--
			continue
		}
		wordStart--
	}

	if wordStart >= len(value) || value[wordStart] != '/' {
		return false, ""
	}

	if wordStart > 0 {
		prev := value[wordStart-1]
		if prev != ' ' && prev != '\t' {
			return false, ""
		}
	}

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

func DetectMention(value string, cursor int) (active bool, prefix string) {
	cursor = clampUTF8Cursor(value, cursor)
	if cursor <= 0 {
		return false, ""
	}

	for i := cursor - 1; i >= 0; i-- {
		if value[i] == '@' {

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
