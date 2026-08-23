package knight

import (
	"path/filepath"
	"regexp"
	"strings"
)

// scopeDowngradeReason inspects a generated SKILL.md body and returns a non-empty
// reason if a "global"-claimed skill should be downgraded to "project" because the
// body references project-specific identifiers (paths, basenames, custom commands).
//
// projDir is the absolute project directory; when empty no downgrade is suggested.
// content is the full SKILL.md text (including frontmatter — frontmatter is stripped
// before analysis so name/description fields don't trigger false positives).
//
// Returns "" when no project-specific signal is detected.
func scopeDowngradeReason(projDir, content string) string {
	projDir = strings.TrimSpace(projDir)
	if projDir == "" {
		return ""
	}
	body := stripFrontmatterForScopeCheck(content)
	if body == "" {
		return ""
	}
	lower := strings.ToLower(body)

	// 2. Absolute path inside project directory (check first — most specific).
	if strings.Contains(body, projDir+"/") || strings.HasSuffix(body, projDir) {
		return "skill body contains absolute path inside project directory"
	}

	// 1. Project basename (e.g. "ggcode") referenced as a bare word.
	base := strings.ToLower(strings.TrimSpace(filepath.Base(projDir)))
	if base != "" && base != "/" && base != "." && len(base) >= 3 {
		if !genericProjectBasename(base) {
			pattern := regexp.MustCompile(`(?i)\b` + regexp.QuoteMeta(base) + `\b`)
			if pattern.MatchString(lower) {
				return "skill body references project basename " + base
			}
		}
	}

	// 3. Project-relative paths under common roots (cmd/, internal/, pkg/, src/, app/).
	// #982: check EVERY occurrence of the root, not just the first one — a body
	// can mention "cmd/" mid-sentence (no path char after) and later reference
	// "cmd/foo" as a real path; the first-only check silently missed that.
	relRoots := []string{"cmd/", "internal/", "pkg/", "src/", "app/"}
	for _, root := range relRoots {
		search := 0
		for {
			idx := strings.Index(lower[search:], root)
			if idx < 0 {
				break
			}
			idx += search
			// Heuristic: only flag if a path segment follows (e.g. internal/knight, cmd/foo).
			rest := lower[idx+len(root):]
			if rest != "" && isPathChar(rest[0]) {
				return "skill body references project-relative path under " + root
			}
			search = idx + len(root)
		}
	}

	// 4. Custom command tokens that look project-specific (e.g. `make foo`, `./script.sh`).
	// #982: the old pattern `(?m)\bmake\s+...` was unanchored, so prose like
	// "make sure", "make the result" or "I just have a URL - make something"
	// matched anywhere in a sentence and wrongly downgraded global skills.
	// Now the target must start a line (allowing leading whitespace, which is
	// how the command appears in fenced blocks), and common English words that
	// follow "make" in imperative prose are whitelisted out.
	for _, m := range makeCmdTargetRe.FindAllStringSubmatch(lower, -1) {
		if !makeProseStopword(m[1]) {
			return "skill body invokes a project-specific make target"
		}
	}
	// Project-local scripts: must look like a real script path (contain a slash after ./
	// and end in a script-like suffix or have multiple path segments).
	if regexp.MustCompile("(?m)(?:^|[\\s`'\"(])\\./[A-Za-z0-9._-]+/[A-Za-z0-9._/-]+").FindString(body) != "" {
		return "skill body invokes a project-local script"
	}
	if regexp.MustCompile("(?m)(?:^|[\\s`'\"(])\\./[A-Za-z0-9._-]+\\.(?:sh|py|js|ts|rb|pl)\\b").FindString(body) != "" {
		return "skill body invokes a project-local script"
	}

	return ""
}

// makeCmdTargetRe matches a make invocation that starts a line (optionally
// indented, as inside a fenced code block). Capturing group 1 is the target.
var makeCmdTargetRe = regexp.MustCompile(`(?m)^[ \t]*make[ \t]+([a-z][a-z0-9_-]*)`)

// makeProseStopwords are words that commonly follow "make" in imperative
// English prose ("make sure", "make the result", "make something", ...).
// A line-initial "make <stopword>" is treated as prose, not a build command.
var makeProseStopwords = map[string]bool{
	"sure": true, "the": true, "this": true, "that": true, "it": true,
	"sense": true, "changes": true, "something": true, "note": true, "use": true,
}

func makeProseStopword(target string) bool {
	return makeProseStopwords[target]
}

// stripFrontmatterForScopeCheck removes the leading YAML frontmatter so that
// name/description fields (which often duplicate the basename) don't cause
// false positives in the scope arbiter.
func stripFrontmatterForScopeCheck(content string) string {
	trimmed := strings.TrimLeft(content, " \t\r\n")
	if !strings.HasPrefix(trimmed, "---") {
		return content
	}
	// Skip the opening delimiter line, then scan for a standalone "---" line.
	// #982: the old `strings.Index(rest, "\n---")` truncated early when a
	// frontmatter VALUE contained "---" as a prefix (e.g. "note: ---text");
	// the delimiter must be a whole line, not a substring.
	nl := strings.IndexByte(trimmed, '\n')
	if nl < 0 {
		return content
	}
	rest := trimmed[nl+1:]
	offset := 0
	for offset <= len(rest) {
		lineEnd := strings.IndexByte(rest[offset:], '\n')
		var line string
		if lineEnd < 0 {
			line = rest[offset:]
		} else {
			line = rest[offset : offset+lineEnd]
		}
		if strings.TrimRight(line, " \t\r") == "---" {
			if lineEnd < 0 {
				return ""
			}
			return rest[offset+lineEnd+1:]
		}
		if lineEnd < 0 {
			return content
		}
		offset += lineEnd + 1
	}
	return content
}

// genericProjectBasename returns true when the basename is too generic to be a
// reliable project signal (e.g. "src", "app", "test").
func genericProjectBasename(name string) bool {
	switch name {
	case "src", "app", "test", "tests", "main", "code", "project", "workspace", "tmp", "go", "node", "py":
		return true
	}
	return false
}

func isPathChar(b byte) bool {
	switch {
	case b >= 'a' && b <= 'z':
		return true
	case b >= 'A' && b <= 'Z':
		return true
	case b >= '0' && b <= '9':
		return true
	case b == '_' || b == '-' || b == '.' || b == '/':
		return true
	}
	return false
}
