// Package docaudit audits project prompt bootstrap documents
// (GGCODE.md, AGENTS.md, CLAUDE.md, COPILOT.md, .cursorrules, ...) for
// stale references that leak into every model turn.
//
// Motivation (2026 frontier): prompt bootstrap documents are injected
// into the system prompt of every session. References that drifted out
// of existence — deleted files, renamed slash commands, moved skills —
// silently waste model turns (the agent opens dead paths or calls
// non-existent commands) and erode trust in the whole document.
// Claude Code shipped the same idea as "/doctor prompt-audit"
// (2026-09-25 changelog: "audit your CLAUDE.md files, skills, agents and
// commands for prompting patterns written for older models", with
// "stale paths, stale commands ... lead the report").
//
// This package implements the deterministic, zero-LLM subset of a
// prompt audit: stale path references and stale slash-command
// references, reported with file:line so a human (or the agent itself)
// can fix them.
//
// Design constraints:
//   - pure stdlib, no model calls, no runtime detector: /docaudit is a
//     user-invoked command, not an inline agent detector
//   - conservative on purpose: ambiguous tokens (bare identifiers,
//     absolute/system paths, anything inside code fences or indented
//     code blocks) are skipped rather than guessed at
package docaudit

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/topcheer/ggcode/internal/memory"
)

// FindingKind classifies a stale reference.
type FindingKind string

const (
	// KindStalePath: a referenced project path does not exist.
	KindStalePath FindingKind = "stale-path"
	// KindStaleCommand: a referenced slash command / skill is not
	// registered (neither a built-in command nor a user/project command
	// or skill).
	KindStaleCommand FindingKind = "stale-command"
)

// Severity levels. Stale commands are errors (calling them fails
// outright); stale paths are warnings (the path may be planned, outside
// the repo, or a false positive of the conservative matcher).
const (
	SeverityError = "error"
	SeverityWarn  = "warn"
)

// Finding is one stale reference, located by file and 1-based line.
type Finding struct {
	File     string      `json:"file"`
	Line     int         `json:"line"`
	Kind     FindingKind `json:"kind"`
	Severity string      `json:"severity"`
	Quote    string      `json:"quote"`
	Detail   string      `json:"detail"`
}

// Report aggregates audit results.
type Report struct {
	Scanned  []string  `json:"scanned"`
	Findings []Finding `json:"findings"`
}

// auditDocs mirrors what actually gets injected into the model context:
// the project memory bootstrap files loaded by internal/memory (kept in
// sync by importing the same list) plus the copilot-instructions
// compatibility file from CompatibilitySubdirRules.
func auditDocs() []string {
	docs := make([]string, 0, len(memory.ProjectMemoryFilenames)+1)
	docs = append(docs, ".github/copilot-instructions.md")
	docs = append(docs, memory.ProjectMemoryFilenames...)
	return docs
}

var (
	mdLinkRe   = regexp.MustCompile(`\[[^\]]*\]\(\s*([^)\s]+)`)
	backtickRe = regexp.MustCompile("`([^`\n]+)`")
	slashCmdRe = regexp.MustCompile(`^/([a-z][a-z0-9-]{0,31})$`)
)

// pathExts lists extensions that make a bare token look like an
// intentional file reference rather than an inline code identifier.
var pathExts = map[string]bool{
	".go": true, ".ts": true, ".tsx": true, ".js": true, ".jsx": true,
	".py": true, ".rs": true, ".java": true, ".kt": true, ".rb": true,
	".c": true, ".h": true, ".cpp": true, ".cc": true, ".m": true,
	".md": true, ".json": true, ".yaml": true, ".yml": true,
	".toml": true, ".sh": true, ".bash": true, ".zsh": true,
	".sql": true, ".html": true, ".css": true, ".scss": true,
	".txt": true, ".proto": true, ".graphql": true, ".mod": true,
}

// slashSystemWords are common absolute-path first segments. A backticked
// `/tmp` or `/usr` is a path, not a command; without this list every
// system path written as a bare single segment would be flagged.
var slashSystemWords = map[string]bool{
	"tmp": true, "usr": true, "etc": true, "var": true, "home": true,
	"root": true, "bin": true, "sbin": true, "opt": true, "dev": true,
	"proc": true, "sys": true, "mnt": true, "media": true, "srv": true,
	"run": true, "lib": true, "lib64": true, "share": true, "private": true,
	"volumes": true, "applications": true, "users": true, "windows": true,
	"program": true, "data": true, "workspace": true, "workspaces": true,
}

// Audit scans the project prompt bootstrap documents under projectDir
// for stale references. knownCommands maps slash-command names WITHOUT
// the leading slash (built-ins merged with user/project commands and
// skills) to true; referenced names missing from the map are flagged.
func Audit(projectDir string, knownCommands map[string]bool) *Report {
	rep := &Report{}
	for _, rel := range auditDocs() {
		abs := filepath.Join(projectDir, rel)
		data, err := os.ReadFile(abs)
		if err != nil {
			continue
		}
		rep.Scanned = append(rep.Scanned, rel)
		auditDoc(rep, projectDir, filepath.Dir(rel), rel, string(data), knownCommands)
	}
	sort.SliceStable(rep.Findings, func(i, j int) bool {
		if rep.Findings[i].File != rep.Findings[j].File {
			return rep.Findings[i].File < rep.Findings[j].File
		}
		return rep.Findings[i].Line < rep.Findings[j].Line
	})
	return rep
}

func auditDoc(rep *Report, projectDir, docDir, rel, content string, knownCommands map[string]bool) {
	lines := strings.Split(content, "\n")
	inFence := false
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			inFence = !inFence
			continue
		}
		// Skip fenced content and indented code blocks: code samples
		// reference hypothetical paths and are not documentation claims.
		if inFence || strings.HasPrefix(line, "    ") || strings.HasPrefix(line, "\t") {
			continue
		}
		lineNo := i + 1
		for _, m := range mdLinkRe.FindAllStringSubmatch(line, -1) {
			checkLinkTarget(rep, projectDir, docDir, rel, lineNo, m[1])
		}
		for _, m := range backtickRe.FindAllStringSubmatch(line, -1) {
			token := strings.TrimSpace(m[1])
			if checkSlashCommand(rep, rel, lineNo, token, knownCommands) {
				continue
			}
			checkPathToken(rep, projectDir, docDir, rel, lineNo, token)
		}
	}
}

// checkSlashCommand reports a backticked `/name` token that is not a
// registered command/skill. Returns true when the token was consumed as
// a command reference (matched or deliberately skipped).
func checkSlashCommand(rep *Report, rel string, lineNo int, token string, knownCommands map[string]bool) bool {
	m := slashCmdRe.FindStringSubmatch(token)
	if m == nil {
		return false
	}
	name := strings.ToLower(m[1])
	if knownCommands[name] {
		return true
	}
	if slashSystemWords[name] {
		return true
	}
	rep.Findings = append(rep.Findings, Finding{
		File: rel, Line: lineNo, Kind: KindStaleCommand,
		Severity: SeverityError, Quote: token,
		Detail: fmt.Sprintf("`%s` is not a registered slash command or skill", token),
	})
	return true
}

// checkLinkTarget validates a markdown link target against the project.
func checkLinkTarget(rep *Report, projectDir, docDir, rel string, lineNo int, target string) {
	target = strings.TrimSpace(target)
	if target == "" {
		return
	}
	// Strip fragment/query; skip external schemes, anchors, absolute paths.
	if i := strings.IndexAny(target, "#?"); i >= 0 {
		target = target[:i]
	}
	if target == "" || strings.Contains(target, "://") || strings.HasPrefix(target, "/") {
		return
	}
	if !projectRefExists(projectDir, docDir, target) {
		rep.Findings = append(rep.Findings, Finding{
			File: rel, Line: lineNo, Kind: KindStalePath,
			Severity: SeverityWarn, Quote: target,
			Detail: fmt.Sprintf("linked target `%s` does not exist", target),
		})
	}
}

// checkPathToken validates a backticked token that looks like an
// intentional project-path reference.
func checkPathToken(rep *Report, projectDir, docDir, rel string, lineNo int, token string) {
	if token == "" || len(token) > 200 {
		return
	}
	if strings.HasPrefix(token, "/") || strings.HasPrefix(token, "~") {
		return // absolute / home paths: not project-relative, unverifiable
	}
	if strings.ContainsAny(token, " \t*?{}$()=<>,;|!\"") {
		return
	}
	if strings.HasSuffix(token, "-") || strings.HasSuffix(token, ".") || strings.Contains(token, "..") {
		return
	}
	if strings.Contains(token, "://") {
		return
	}
	if !strings.Contains(token, "/") && !pathExts[strings.ToLower(filepath.Ext(token))] {
		return // bare identifier like `pattern`, not a path claim
	}
	if !projectRefExists(projectDir, docDir, token) {
		rep.Findings = append(rep.Findings, Finding{
			File: rel, Line: lineNo, Kind: KindStalePath,
			Severity: SeverityWarn, Quote: token,
			Detail: fmt.Sprintf("referenced path `%s` does not exist", token),
		})
	}
}

// projectRefExists resolves a reference first against the project root
// (the common convention in bootstrap docs) and then against the
// document's own directory (markdown-relative links).
func projectRefExists(projectDir, docDir, ref string) bool {
	for _, base := range []string{projectDir, filepath.Join(projectDir, docDir)} {
		if _, err := os.Stat(filepath.Join(base, filepath.FromSlash(ref))); err == nil {
			return true
		}
	}
	return false
}

// String renders a compact, human- and model-readable report.
func (r *Report) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "/docaudit: scanned %d document(s), %d finding(s)\n", len(r.Scanned), len(r.Findings))
	if len(r.Findings) == 0 {
		if len(r.Scanned) == 0 {
			b.WriteString("no prompt bootstrap documents found in this project.\n")
			return b.String()
		}
		b.WriteString("no stale references found.\n")
		return b.String()
	}
	last := ""
	for _, f := range r.Findings {
		if f.File != last {
			fmt.Fprintf(&b, "\n%s\n", f.File)
			last = f.File
		}
		fmt.Fprintf(&b, "  %d: [%s] %s: %s\n", f.Line, f.Severity, f.Kind, f.Detail)
	}
	b.WriteString("\nStale references are injected into every model turn; fix or remove them.\n")
	return b.String()
}
