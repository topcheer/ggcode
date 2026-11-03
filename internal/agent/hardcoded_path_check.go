package agent

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

type pathPattern struct {
	re    *regexp.Regexp
	label string
}

var hardcodedPathPatterns = []pathPattern{
	{

		re:    regexp.MustCompile(`/Users/[a-zA-Z0-9._-]{2,}(?:/[a-zA-Z0-9._-]+)*`),
		label: "macOS home path",
	},
	{

		re:    regexp.MustCompile(`/home/[a-zA-Z0-9._-]{2,}(?:/[a-zA-Z0-9._-]+)*`),
		label: "Linux home path",
	},
	{

		re:    regexp.MustCompile(`/root/[a-zA-Z0-9._-]{2,}(?:/[a-zA-Z0-9._-]+)*`),
		label: "Linux root home path",
	},
	{

		re:    regexp.MustCompile(`(?i)[a-z]:\x5c{1,2}Users\x5c{1,2}[a-zA-Z0-9._-]{2,}(?:\x5c{1,2}[a-zA-Z0-9._-]+)*`),
		label: "Windows user path",
	},
	{

		re:    regexp.MustCompile(`(?i)[a-z]:/Users/[a-zA-Z0-9._-]{2,}(?:/[a-zA-Z0-9._-]+)*`),
		label: "Windows user path",
	},
}

var pathExemptExts = map[string]bool{
	".md":    true,
	".txt":   true,
	".rst":   true,
	".adoc":  true,
	".env":   true,
	".envrc": true,
	".pem":   true,
	".key":   true,
	".crt":   true,
	".lock":  true,
	".mod":   true,
	".sum":   true,
}

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

func checkHardcodedPaths(filePath, oldContent, newContent string) []string {
	if strings.TrimSpace(newContent) == "" {
		return nil
	}

	ext := strings.ToLower(filepath.Ext(filePath))
	if pathExemptExts[ext] {
		return nil
	}

	base := strings.ToLower(filepath.Base(filePath))
	if pathExemptBasenames[base] {
		return nil
	}

	lowerPath := strings.ToLower(filePath)
	for _, dir := range secretExemptDirs {
		if pathHasSegment(lowerPath, dir) {
			return nil
		}
	}

	if isTestFile(filePath) {
		return nil
	}

	var warnings []string

	scanNew := hpStripCommentLines(newContent)
	scanOld := hpStripCommentLines(oldContent)

	for _, pp := range hardcodedPathPatterns {

		oldMatches := pp.re.FindAllString(scanOld, -1)
		newMatches := countNonExemptPathMatches(scanNew, pp.re)

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

var hpRouteContextWords = []string{
	"handlefunc",
	"handle(",
	"route",
	"router",
	"mux",
	"endpoint",
	"http.",
	"https.",

	".get(", ".post(", ".put(", ".delete(", ".patch(", ".head(", ".options(",
}

func isRoutePathLiteral(line, match string, matchStart int) bool {

	if !strings.HasPrefix(match, "/") {
		return false
	}

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
