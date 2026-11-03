package tool

import (
	"fmt"
	"regexp"
	"strings"
)

const (
	maxConsistencyFindings  = 3
	minDiffFilesForCoverage = 5
	minPhantomSymbols       = 2
)

var (
	diffHeaderRe    = regexp.MustCompile(`^diff --git (?:"a/([^"]+)"|a/(\S+)) (?:"b/([^"]+)"|b/(\S+))$`)
	diffSymbolRe    = regexp.MustCompile(`^[-+](?:func|type|const|var)\s+(?:\([^)]*\)\s*)?([A-Za-z_][A-Za-z0-9_]*)`)
	msgFileTokenRe  = regexp.MustCompile(`[\w./-]+\.[A-Za-z][A-Za-z0-9]{0,5}\b`)
	msgIdentTokenRe = regexp.MustCompile(`\b[A-Za-z_][A-Za-z0-9_]*\b`)
	msgUrlRe        = regexp.MustCompile(`\b(?:[a-zA-Z][a-zA-Z0-9+.-]*://|www\.)\S+`)
	msgEmailRe      = regexp.MustCompile(`\b[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}\b`)
)

func AnalyzeMessageDiffConsistency(message, diffOutput string) string {
	if strings.TrimSpace(message) == "" || strings.TrimSpace(diffOutput) == "" {
		return ""
	}

	message = stripUrlsAndEmails(message)
	files, oldFiles, symbols, diffLower := parseDiffFacts(diffOutput)

	var findings []string

	var phantoms []string
	for _, tok := range msgFileTokenRe.FindAllString(message, -1) {
		base := pathBase(tok)
		if _, ok := files[base]; ok {
			continue
		}

		if _, ok := oldFiles[base]; ok {
			continue
		}

		if abbrevLikeFileToken(base) {
			continue
		}

		if domainLikeFileToken(base) {
			continue
		}
		if !consistencyHas(phantoms, base) {
			phantoms = append(phantoms, base)
		}
	}
	if len(phantoms) > 0 {
		shown := phantoms
		if len(shown) > maxConsistencyFindings {
			shown = shown[:maxConsistencyFindings]
		}
		extra := ""
		if len(phantoms) > maxConsistencyFindings {
			extra = fmt.Sprintf(" (+%d more)", len(phantoms)-maxConsistencyFindings)
		}
		findings = append(findings, fmt.Sprintf(
			"commit message mentions files absent from the staged diff: %s%s", strings.Join(shown, ", "), extra))
	}

	var phSym []string
	for _, tok := range msgIdentTokenRe.FindAllString(message, -1) {
		if len(tok) < 5 || looksLikeProse(tok) {
			continue
		}
		if strings.Contains(diffLower, strings.ToLower(tok)) {
			continue
		}
		if _, ok := symbols[tok]; ok {
			continue
		}
		if !consistencyHas(phSym, tok) {
			phSym = append(phSym, tok)
		}
	}
	if len(phSym) >= minPhantomSymbols {
		shown := phSym
		if len(shown) > maxConsistencyFindings {
			shown = shown[:maxConsistencyFindings]
		}
		findings = append(findings, fmt.Sprintf(
			"commit message names symbols that appear nowhere in the staged diff: %s", strings.Join(shown, ", ")))
	}

	allFiles := make(map[string]struct{}, len(files)+len(oldFiles))
	for f := range files {
		allFiles[f] = struct{}{}
	}
	for f := range oldFiles {
		allFiles[f] = struct{}{}
	}
	if len(files) >= minDiffFilesForCoverage && !mentionsAnyFile(message, allFiles) {
		findings = append(findings, fmt.Sprintf(
			"staged diff touches %d files but the commit message references none of them — is the message describing this change?", len(files)))
	}

	if len(findings) == 0 {
		return ""
	}
	return "Warning: commit message and staged diff disagree — " +
		strings.Join(findings, "; ") + ". Consumers (reviewers, git archaeology, reverts) trust the message: align it with the actual diff."
}

func parseDiffFacts(diffOutput string) (files map[string]struct{}, oldFiles map[string]struct{}, symbols map[string]struct{}, lower string) {
	files = map[string]struct{}{}
	oldFiles = map[string]struct{}{}
	symbols = map[string]struct{}{}
	for _, line := range strings.Split(diffOutput, "\n") {
		if m := diffHeaderRe.FindStringSubmatch(line); m != nil {

			oldPath, newPath := m[1], m[3]
			if oldPath == "" {
				oldPath = m[2]
			}
			if newPath == "" {
				newPath = m[4]
			}

			oldB := pathBase(gitUnquotePath(oldPath))
			newB := pathBase(gitUnquotePath(newPath))
			files[newB] = struct{}{}

			if oldB == newB {
				files[oldB] = struct{}{}
			} else {
				oldFiles[oldB] = struct{}{}
			}
			continue
		}
		if m := diffSymbolRe.FindStringSubmatch(line); m != nil {
			symbols[m[1]] = struct{}{}
		}
	}
	return files, oldFiles, symbols, strings.ToLower(diffOutput)
}

func gitUnquotePath(s string) string {
	if !strings.ContainsAny(s, "\\") {
		return s
	}
	var b []byte
	for i := 0; i < len(s); i++ {
		if s[i] != '\\' || i+1 >= len(s) {
			b = append(b, s[i])
			continue
		}
		switch s[i+1] {
		case '"', '\\':
			b = append(b, s[i+1])
			i++
		case '0', '1', '2', '3', '4', '5', '6', '7':
			if i+3 < len(s)+1 && i+3 <= len(s) {
				v := 0
				ok := true
				for j := 1; j <= 3 && i+j < len(s); j++ {
					d := s[i+j]
					if d < '0' || d > '7' {
						ok = false
						break
					}
					v = v*8 + int(d-'0')
				}
				if ok && v <= 255 {
					b = append(b, byte(v))
					i += 3
				} else {
					b = append(b, s[i])
				}
			}
		default:
			b = append(b, s[i])
		}
	}
	return string(b)
}

func mentionsAnyFile(message string, files map[string]struct{}) bool {
	msgLower := strings.ToLower(message)
	for f := range files {
		if strings.Contains(msgLower, strings.ToLower(f)) {
			return true
		}
		stem := f
		if i := strings.LastIndexByte(stem, '.'); i > 0 {
			stem = stem[:i]
		}
		if len(stem) >= 4 && strings.Contains(msgLower, strings.ToLower(stem)) {
			return true
		}
	}
	return false
}

func looksLikeProse(tok string) bool {
	hasUpper, hasLower, hasUnder := false, false, strings.Contains(tok, "_")
	for _, r := range tok {
		switch {
		case r >= 'A' && r <= 'Z':
			hasUpper = true
		case r >= 'a' && r <= 'z':
			hasLower = true
		}
	}
	if hasUnder {
		return false
	}
	if hasUpper && hasLower {

		rest := tok[1:]
		return rest == strings.ToLower(rest)
	}
	return true
}

func pathBase(p string) string {
	p = strings.TrimSpace(p)
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		p = p[i+1:]
	}
	return p
}

func stripUrlsAndEmails(message string) string {
	message = msgUrlRe.ReplaceAllString(message, " ")
	return msgEmailRe.ReplaceAllString(message, " ")
}

func domainLikeFileToken(tok string) bool {
	if strings.ContainsAny(tok, "/\\") {
		return false
	}
	segs := strings.Split(tok, ".")
	if len(segs) != 2 {
		return false
	}
	switch strings.ToLower(segs[1]) {
	case "com", "org", "net", "io", "dev", "co", "me", "ai", "app", "sh", "gg", "cn", "eu", "us", "uk":
		return len(segs[0]) >= 2
	}
	return false
}

func abbrevLikeFileToken(base string) bool {
	if len(base) < 4 {
		return true
	}
	first := strings.SplitN(base, ".", 2)[0]
	return len(first) < 2
}

func consistencyHas(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
