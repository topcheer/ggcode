package agent

// Hardcoded Absolute Path Detection in File Writes
//
// Research basis: AI coding agents frequently introduce machine-specific
// absolute paths into source code and configuration files, e.g.,
// writing `configPath := "/Users/john/project/config.yaml"` or
// `binaryPath := "/home/dev/go/bin/custom-go"` as a "working example" or
// to reference the agent's own working directory. These paths are
// machine-specific: they break portability, CI/CD pipelines, Docker
// containers, and collaboration with teammates on different machines.
//
// This is DIFFERENT from hardcoded_secret_check.go (which detects
// credentials): here we detect absolute filesystem paths embedded in
// source code that should instead use relative paths, environment
// variables, or runtime-discovered paths (os.UserHomeDir(), etc.).
//
// Competitor analysis:
//   - Claude Code: relies on agent self-judgment (unreliable)
//   - Cursor: no write-time path detection
//   - GitHub Copilot: no path detection in suggestions
//   - Cline/OpenHands: no write-time path detection
//   - Aider: diff review may catch these, but no automated detection
//
// ggcode's approach: detect machine-specific absolute paths at write time
// by comparing occurrence counts before vs. after the edit. Only NEW paths
// (introduced by this edit) are flagged - pre-existing ones are left alone.
// This is zero-LLM-cost, language-aware, and has near-zero false positives
// because we target only UNAMBIGUOUS machine-specific path patterns
// (/Users/<name>/, /home/<name>/, /root/<name>/, C:\Users\<name>\).

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

// pathPattern represents a machine-specific absolute path pattern.
type pathPattern struct {
	re    *regexp.Regexp
	label string
}

// hardcodedPathPatterns are compiled regexes that detect machine-specific
// absolute home-directory paths across macOS, Linux, and Windows.
//
// Design decisions:
//   - Username segment is [a-zA-Z0-9._-]{2,} (2+ chars, no $ or ~ which
//     indicate shell variables or home shortcuts that are safe).
//   - /root/ requires a following path segment (bare /root/ alone is too
//     common in legitimate code, e.g. "user: root" in Dockerfiles).
//   - Windows backslash paths are matched as literal text in the file.
var hardcodedPathPatterns = []pathPattern{
	{
		// macOS: /Users/<username>/...
		re:    regexp.MustCompile(`/Users/[a-zA-Z0-9._-]{2,}(?:/[a-zA-Z0-9._-]+)*`),
		label: "macOS home path",
	},
	{
		// Linux: /home/<username>/...
		re:    regexp.MustCompile(`/home/[a-zA-Z0-9._-]{2,}(?:/[a-zA-Z0-9._-]+)*`),
		label: "Linux home path",
	},
	{
		// Linux root: /root/<segment>/... (requires a real path after /root/)
		re:    regexp.MustCompile(`/root/[a-zA-Z0-9._-]{2,}(?:/[a-zA-Z0-9._-]+)*`),
		label: "Linux root home path",
	},
	{
		// Windows backslash: C:\Users\<username>\...
		// Matches 1-2 backslashes to handle both literal paths (C:\Users\...)
		// and escaped forms in source code (C:\\Users\\...).
		// Uses \x5c (hex escape for backslash) to avoid raw-string escaping issues.
		re:    regexp.MustCompile(`(?i)[a-z]:\x5c{1,2}Users\x5c{1,2}[a-zA-Z0-9._-]{2,}(?:\x5c{1,2}[a-zA-Z0-9._-]+)*`),
		label: "Windows user path",
	},
	{
		// Windows forward-slash: C:/Users/<username>/...
		re:    regexp.MustCompile(`(?i)[a-z]:/Users/[a-zA-Z0-9._-]{2,}(?:/[a-zA-Z0-9._-]+)*`),
		label: "Windows user path",
	},
}

// pathExemptExts lists file extensions where absolute paths are expected or
// harmless and should NOT trigger warnings.
var pathExemptExts = map[string]bool{
	".md":    true, // markdown documentation (paths are examples)
	".txt":   true, // plain text
	".rst":   true, // reStructuredText
	".adoc":  true, // AsciiDoc
	".env":   true, // env files (paths are legitimate machine config)
	".envrc": true, // direnv config
	".pem":   true, // certificates
	".key":   true, // keys
	".crt":   true, // certificates
	".lock":  true, // lock files
	".mod":   true, // go.mod (module paths, not filesystem paths)
	".sum":   true, // checksums
}

// pathExemptBasenames lists file basenames where absolute paths are expected.
var pathExemptBasenames = map[string]bool{
	".bashrc":       true,
	".bash_profile": true,
	".zshrc":        true,
	".zprofile":     true,
	".profile":      true,
	".fishrc":       true,
	"makefile":      true,
	"dockerfile":    true,
	".dockerignore": true,
	".gitignore":    true,
	".gitconfig":    true,
}

// checkHardcodedPaths detects machine-specific absolute paths that were
// INTRODUCED by this edit (present in newContent but not in oldContent).
// Returns warning strings for each newly-introduced path type.
//
// Key design decisions:
//   - Only flags NEW paths: compares occurrence counts. Pre-existing paths
//     in oldContent are not flagged (the agent didn't introduce them).
//   - Skips test files: paths in tests are often intentional fixtures.
//   - Skips documentation and shell config files where paths are expected.
//   - Targets only unambiguous home-directory patterns (/Users/, /home/,
//     /root/, C:\Users\) - not generic absolute paths like /usr/bin or
//     /tmp/ which are system-standard.
func checkHardcodedPaths(filePath, oldContent, newContent string) []string {
	if strings.TrimSpace(newContent) == "" {
		return nil
	}

	// Skip files where absolute paths are expected.
	ext := strings.ToLower(filepath.Ext(filePath))
	if pathExemptExts[ext] {
		return nil
	}

	base := strings.ToLower(filepath.Base(filePath))
	if pathExemptBasenames[base] {
		return nil
	}

	// Skip test fixture directories. Segment-exact match (same semantics
	// as the #247 fix in hardcoded_secret_check): substring matching both
	// missed trailing segments without a slash and exempted unrelated
	// paths like myfixturesnote/ (#733).
	lowerPath := strings.ToLower(filePath)
	for _, dir := range secretExemptDirs {
		if pathHasSegment(lowerPath, dir) {
			return nil
		}
	}

	// Skip test files — paths in tests are often intentional fixtures.
	if isTestFile(filePath) {
		return nil
	}

	var warnings []string

	// #556: strip comment lines first so paths mentioned in prose (// or #
	// comments) cannot trigger warnings — same pattern as #544/#527
	// (premature_refactor.prStripCommentLines).
	scanNew := hpStripCommentLines(newContent)
	scanOld := hpStripCommentLines(oldContent)

	for _, pp := range hardcodedPathPatterns {
		// #556: count occurrences line-by-line, exempting URL route literals
		// (http.HandleFunc("/home/dashboard", h)) that the bare regex mistakes
		// for machine-specific home paths (issue #556 vs #516 zero-FP claim).
		oldMatches := pp.re.FindAllString(scanOld, -1)
		newMatches := countNonExemptPathMatches(scanNew, pp.re)

		// Per-instance set comparison (fix #171 — count-diff is blind to
		// remove-N-add-N; see hardcoded_secret_check.go).
		oldSet := make(map[string]int)
		for _, m := range oldMatches {
			oldSet[m]++
		}
		introduced := 0
		for _, m := range newMatches {
			oldSet[m]--
			if oldSet[m] < 0 {
				introduced++
			}
		}
		if introduced > 0 {
			warnings = append(warnings, formatPathWarning(pp.label, introduced))
		}
	}

	return warnings
}

// formatPathWarning renders a concise warning for a newly-introduced
// machine-specific absolute path.
func formatPathWarning(pathType string, count int) string {
	noun := "occurrence"
	if count > 1 {
		noun = "occurrences"
	}
	return fmt.Sprintf(
		"Introduced %d %s of a machine-specific %s. Absolute paths like "+
			"\"/Users/...\", \"/home/...\", or \"C:\\Users\\...\" are specific to "+
			"one machine and will break portability, CI/CD, Docker containers, "+
			"and other developers. Use a relative path, os.UserHomeDir(), "+
			"filepath.Join(), or an environment variable instead.",
		count, noun, pathType)
}

// hpStripCommentLines removes // and # comment lines and /* */ blocks so
// that paths mentioned in prose cannot trigger path warnings — only code
// counts. Mirrors premature_refactor.prStripCommentLines (#544/#527 pattern;
// local copy to avoid widening that file's API).
func hpStripCommentLines(s string) string {
	lines := strings.Split(s, "\n")
	var keep []string
	inBlock := false
	for _, ln := range lines {
		trimmed := strings.TrimSpace(ln)
		if inBlock {
			if strings.Contains(trimmed, "*/") {
				inBlock = false
			}
			continue
		}
		if strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if strings.HasPrefix(trimmed, "/*") {
			if !strings.Contains(trimmed, "*/") {
				inBlock = true
			}
			continue
		}
		keep = append(keep, ln)
	}
	return strings.Join(keep, "\n")
}

// hpRouteContextWords are tokens that, when they appear in the line PREFIX
// before a "/..." string literal, indicate a route-registration context
// rather than a filesystem path (#556).
var hpRouteContextWords = []string{
	"handlefunc", // net/http, gin, echo, fiber...
	"handle(",    // mux.Handle, chi Router.Handle
	"route",      // r.Route, router.Route(...)
	"router",     // gin.Default() assigned to var router
	"mux",        // gorilla mux
	"endpoint",   // endpoint registration
	"http.",      // http.HandleX / http server config
	"https.",
	// HTTP verb methods (router.GET/POST/...). Dot-prefixed so that prose-like
	// helper names ("parseInput(", which contains "put(") do not match.
	".get(", ".post(", ".put(", ".delete(", ".patch(", ".head(", ".options(",
}

// isRoutePathLiteral reports whether the match at column matchStart in line
// is a URL route literal (e.g. the first argument of
// http.HandleFunc("/home/dashboard", h)) rather than a filesystem path.
// Structural rule: the match must start immediately after a quote AND the
// prefix before it must contain a routing-context word. This keeps real
// filesystem paths like os.Open("/home/u/http-cache/file") reportable —
// "http" inside the path value or after the match does not exempt it.
func isRoutePathLiteral(line, match string, matchStart int) bool {
	// Only "/"-rooted patterns can be URL routes (Windows C:\ paths cannot).
	if !strings.HasPrefix(match, "/") {
		return false
	}
	// Match must begin immediately after a string-literal opening quote.
	if matchStart == 0 || (line[matchStart-1] != '"' && line[matchStart-1] != '\'') {
		return false
	}
	prefix := strings.ToLower(line[:matchStart])
	for _, w := range hpRouteContextWords {
		if strings.Contains(prefix, w) {
			return true
		}
	}
	return false
}

// countNonExemptPathMatches finds all regex matches per line, skipping URL
// route literals exempt by isRoutePathLiteral (#556).
func countNonExemptPathMatches(content string, re *regexp.Regexp) []string {
	var out []string
	for _, ln := range strings.Split(content, "\n") {
		for _, loc := range re.FindAllStringIndex(ln, -1) {
			m := ln[loc[0]:loc[1]]
			if !isRoutePathLiteral(ln, m, loc[0]) {
				out = append(out, m)
			}
		}
	}
	return out
}
