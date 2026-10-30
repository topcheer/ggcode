package tool

import (
	"fmt"
	"regexp"
	"strings"
)

// Message-diff consistency sentinel (message-code inconsistency audit).
//
// "Analyzing Message-Code Inconsistency in AI Coding Agent-Authored Pull
// Requests" shows that agent-authored change descriptions (commit messages,
// PR bodies) frequently drift from the actual diff, while every downstream
// consumer — human reviewers, git archaeology, automated revert tooling —
// trusts those descriptions. The existing commit pipeline checks message
// quality in isolation (checkCommitMessageQuality, AnalyzeCommitMessage)
// and diff quality in isolation (ScanStagedDiffForIssues, AnalyzeCommitScope)
// but never crosses the two streams. This sentinel closes that loop:
// it cross-checks the claims in the commit message against the staged diff
// and warns when they disagree.
//
// Pure advisory: the commit proceeds, the warning lets the agent self-correct
// before the misleading description enters history.

const (
	// maxConsistencyFindings caps how many phantom names a single warning
	// lists, keeping the advisory readable.
	maxConsistencyFindings = 3
	// minDiffFilesForCoverage requires this many changed files before a
	// "message mentions none of them" coverage warning fires — small diffs
	// routinely have generic subjects.
	minDiffFilesForCoverage = 5
	// minPhantomSymbols requires two unseen identifiers before warning:
	// one borderline token is frequently legitimate prose, two is a pattern.
	minPhantomSymbols = 2
)

var (
	// diffHeaderRe matches "diff --git a/<old> b/<new>" headers.
	// #4433: git quotes paths containing spaces (always) and non-ASCII
	// bytes (core.quotePath) as "a/\344\270\255.go"; a whole-token capture
	// with an explicit quoted alternative is the only ambiguity-free form
	// (optional-quote suffixes let the lazy group absorb the closing quote).
	// Quoted side -> capture pair (1,3); bare side -> (2,4).
	diffHeaderRe = regexp.MustCompile(`^diff --git (?:"a/([^"]+)"|a/(\S+)) (?:"b/([^"]+)"|b/(\S+))$`)
	// diffSymbolRe matches added/removed top-level Go-style declarations
	// ("+func Foo", "-type bar") whose name is capture 2.
	diffSymbolRe = regexp.MustCompile(`^[-+](?:func|type|const|var)\s+(?:\([^)]*\)\s*)?([A-Za-z_][A-Za-z0-9_]*)`)
	// msgFileTokenRe matches file-looking tokens in the message body:
	// a path-ish word carrying a short alphabetic extension. Version
	// numbers (v1.2.3) and bare hosts are excluded by requiring the
	// extension to start with a letter and be all-letter.
	msgFileTokenRe = regexp.MustCompile(`[\w./-]+\.[A-Za-z][A-Za-z0-9]{0,5}\b`)
	// msgIdentTokenRe matches identifier-looking words in the message.
	msgIdentTokenRe = regexp.MustCompile(`\b[A-Za-z_][A-Za-z0-9_]*\b`)
)

// AnalyzeMessageDiffConsistency cross-checks the commit message against the
// staged diff text. Returns "" when the message is consistent (or there is
// nothing to check against); otherwise an advisory warning string.
func AnalyzeMessageDiffConsistency(message, diffOutput string) string {
	if strings.TrimSpace(message) == "" || strings.TrimSpace(diffOutput) == "" {
		return ""
	}
	files, oldFiles, symbols, diffLower := parseDiffFacts(diffOutput)

	var findings []string

	// Rule 1: phantom files — the message names files the diff never touches.
	var phantoms []string
	for _, tok := range msgFileTokenRe.FindAllString(message, -1) {
		base := pathBase(tok)
		if _, ok := files[base]; ok {
			continue
		}
		// #4436: rename source names are legitimate message references.
		if _, ok := oldFiles[base]; ok {
			continue
		}
		// #3448: filter abbreviation-shaped tokens ("e.g", "i.e", "a.m",
		// "U.S", "x.y") - the highest-frequency English abbreviations all
		// match msgFileTokenRe and produced constant false-positive
		// phantom-file warnings. A real file basename virtually never has
		// a single-character first segment or a sub-4-char total length;
		// Rule 1 has no min-count gate (unlike Rule 2), so precision here
		// is the only defense.
		if abbrevLikeFileToken(base) {
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

	// Rule 2: phantom symbols — >=2 declared-looking identifiers in the
	// message that appear nowhere in the diff text or its paths.
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

	// Rule 3: coverage — a large multi-file diff whose message references
	// none of the changed files reads like a description of different work.
	// #4436: rename source names count as references too (union).
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

// parseDiffFacts extracts the changed-file basenames, changed top-level
// symbol names, and a lowercased blob of the whole diff for substring probes.
// files counts each changed file once (renames count as the new name, #4433);
// oldFiles holds rename source basenames so message references to the OLD
// name ("rename handler.go to api.go") do not read as phantoms (#4436).
func parseDiffFacts(diffOutput string) (files map[string]struct{}, oldFiles map[string]struct{}, symbols map[string]struct{}, lower string) {
	files = map[string]struct{}{}
	oldFiles = map[string]struct{}{}
	symbols = map[string]struct{}{}
	for _, line := range strings.Split(diffOutput, "\n") {
		if m := diffHeaderRe.FindStringSubmatch(line); m != nil {
			// #4433: quoted alternative is pair (1,3), bare is (2,4).
			oldPath, newPath := m[1], m[3]
			if oldPath == "" {
				oldPath = m[2]
			}
			if newPath == "" {
				newPath = m[4]
			}
			// #4433: strip git quoting ("a/\344\270\255.go" -> a/中.go) so
			// basename matching sees the real name, not octal debris.
			oldB := pathBase(gitUnquotePath(oldPath))
			newB := pathBase(gitUnquotePath(newPath))
			files[newB] = struct{}{}
			// #4433: a rename header (old side != new side) counts the file
			// ONCE - both sides used to be added, so 3 pure renames inflated
			// len(files) to 6 and tripped the minDiffFilesForCoverage gate.
			// #4436: the OLD name still goes to oldFiles so "rename handler.go
			// to api.go" does not flag handler.go as a phantom file.
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

// gitUnquotePath reverses git's diff-header quoting for the escapes that
// appear in practice: octal byte escapes (\344) from core.quotePath for
// non-ASCII bytes, plus \" and \\ for embedded quotes/backslashes. Paths
// without escapes round-trip unchanged.
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

// mentionsAnyFile reports whether the message references any changed file
// by basename or by its extension-stripped stem.
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

// looksLikeProse filters out ordinary words so rule 2 only fires on
// identifier-shaped tokens: mixed case like NewHandler, snake_case like
// context_manager, or ALL_CAPS like MAX_RETRIES. TitleCase single words
// (The, Warning) and all-lowercase words are prose.
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
		return false // snake_case is identifier-shaped
	}
	if hasUpper && hasLower {
		// Mixed case: TitleCase (first upper, rest lower) is prose;
		// anything else (NewHandler, parseJSON) is an identifier.
		rest := tok[1:]
		return rest == strings.ToLower(rest)
	}
	return true // all-upper len>=5 (TODO-ish) or all-lower: prose
}

func pathBase(p string) string {
	p = strings.TrimSpace(p)
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		p = p[i+1:]
	}
	return p
}

// abbrevLikeFileToken reports whether a msgFileTokenRe match shaped like an
// English abbreviation ("e.g", "i.e", "a.m", "U.S", "x.y") rather than a
// file basename: single-character first segment, or total length under 4.
// Real basenames ("ab.go", "f_test.go") pass; single-letter filename stems
// with short extensions are vanishingly rare and this check is advisory.
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
