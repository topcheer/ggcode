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
	relRoots := []string{"cmd/", "internal/", "pkg/", "src/", "app/"}
	for _, root := range relRoots {
		idx := strings.Index(body, root)
		if idx < 0 {
			continue
		}
		// Heuristic: only flag if a path segment follows (e.g. internal/knight, cmd/foo).
		rest := body[idx+len(root):]
		if rest == "" {
			continue
		}
		next := rest[0]
		if isPathChar(next) {
			return "skill body references project-relative path under " + root
		}
	}

	// 4. Custom command tokens that look project-specific (e.g. `make foo`, `./script.sh`).
	if regexp.MustCompile(`(?m)\bmake\s+[a-z][a-z0-9_-]{2,}`).FindString(lower) != "" {
		return "skill body invokes a project-specific make target"
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

// stripFrontmatterForScopeCheck removes the leading YAML frontmatter so that
// name/description fields (which often duplicate the basename) don't cause
// false positives in the scope arbiter.
func stripFrontmatterForScopeCheck(content string) string {
	trimmed := strings.TrimLeft(content, " \t\r\n")
	if !strings.HasPrefix(trimmed, "---") {
		return content
	}
	rest := trimmed[3:]
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return content
	}
	return rest[end+4:]
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
