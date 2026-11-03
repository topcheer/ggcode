package knight

import (
	"path/filepath"
	"regexp"
	"strings"
)

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

	if strings.Contains(body, projDir+"/") || strings.HasSuffix(body, projDir) {
		return "skill body contains absolute path inside project directory"
	}

	base := strings.ToLower(strings.TrimSpace(filepath.Base(projDir)))
	if base != "" && base != "/" && base != "." && len(base) >= 3 {
		if !genericProjectBasename(base) {
			pattern := regexp.MustCompile(`(?i)\b` + regexp.QuoteMeta(base) + `\b`)
			if pattern.MatchString(lower) {
				return "skill body references project basename " + base
			}
		}
	}

	relRoots := []string{"cmd/", "internal/", "pkg/", "src/", "app/"}
	for _, root := range relRoots {
		search := 0
		for {
			idx := strings.Index(lower[search:], root)
			if idx < 0 {
				break
			}
			idx += search

			rest := lower[idx+len(root):]
			if rest != "" && isPathChar(rest[0]) {
				return "skill body references project-relative path under " + root
			}
			search = idx + len(root)
		}
	}

	for _, m := range makeCmdTargetRe.FindAllStringSubmatch(lower, -1) {
		if !makeProseStopword(m[1]) {
			return "skill body invokes a project-specific make target"
		}
	}

	if regexp.MustCompile("(?m)(?:^|[\\s`'\"(])\\./[A-Za-z0-9._-]+/[A-Za-z0-9._/-]+").FindString(body) != "" {
		return "skill body invokes a project-local script"
	}
	if regexp.MustCompile("(?m)(?:^|[\\s`'\"(])\\./[A-Za-z0-9._-]+\\.(?:sh|py|js|ts|rb|pl)\\b").FindString(body) != "" {
		return "skill body invokes a project-local script"
	}

	return ""
}

var makeCmdTargetRe = regexp.MustCompile(`(?m)^[ \t]*make[ \t]+([a-z][a-z0-9_-]*)`)

var makeProseStopwords = map[string]bool{
	"sure": true, "the": true, "this": true, "that": true, "it": true,
	"sense": true, "changes": true, "something": true, "note": true, "use": true,
}

func makeProseStopword(target string) bool {
	return makeProseStopwords[target]
}

func stripFrontmatterForScopeCheck(content string) string {
	trimmed := strings.TrimLeft(content, " \t\r\n")
	if !strings.HasPrefix(trimmed, "---") {
		return content
	}

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
