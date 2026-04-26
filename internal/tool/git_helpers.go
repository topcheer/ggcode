package tool

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"strings"
)

const coAuthorTrailer = "Co-Authored-By: ggcode <noreply@ggcode.dev>"

// resolveDir returns the first non-empty path from the given values.
// Used by git tools to fall back from explicit path arg to WorkingDir.
func resolveDir(paths ...string) string {
	for _, p := range paths {
		if p != "" {
			return p
		}
	}
	return ""
}

// gitCommand creates a git command with GIT_PAGER=cat to prevent pager hangs.
func gitCommand(ctx context.Context, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Env = append(os.Environ(), "GIT_PAGER=cat")
	return cmd
}

// gitTrackedFiles returns the set of files tracked by git (respects .gitignore).
// Returns nil if the directory is not inside a git repository.
func gitTrackedFiles(ctx context.Context, dir string) map[string]struct{} {
	cmd := gitCommand(ctx, "ls-files", "-z", "--cached", "--others", "--exclude-standard")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	if len(out) == 0 {
		return map[string]struct{}{}
	}
	files := bytes.Split(out, []byte{0})
	result := make(map[string]struct{}, len(files))
	for _, f := range files {
		s := string(f)
		if s != "" {
			result[s] = struct{}{}
		}
	}
	return result
}

// isGitCommitCommand detects whether a shell command is a git commit.
func isGitCommitCommand(cmd string) bool {
	// Strip env var prefixes like "GIT_PAGER=cat "
	s := cmd
	for {
		idx := strings.Index(s, "=")
		if idx < 0 {
			break
		}
		spaceIdx := strings.Index(s[:idx], " ")
		if spaceIdx >= 0 {
			break
		}
		// Skip past "KEY=VALUE " pattern
		after := s[idx+1:]
		if spIdx := strings.Index(after, " "); spIdx >= 0 {
			s = after[spIdx+1:]
		} else {
			break
		}
	}
	s = strings.TrimSpace(s)
	// Match "git commit" with optional flags/args before the subcommand
	if !strings.HasPrefix(s, "git ") && s != "git" {
		return false
	}
	rest := strings.TrimSpace(s[3:])
	// Handle "git -c key=val commit" pattern — skip flag arguments
	for {
		rest = strings.TrimSpace(rest)
		if !strings.HasPrefix(rest, "-") {
			break
		}
		// Skip the flag token
		spIdx := strings.Index(rest, " ")
		if spIdx < 0 {
			return false
		}
		rest = strings.TrimSpace(rest[spIdx+1:])
		// If the flag value contains = (e.g. -c user.name=x), skip past it too
		// But only if current token is not another flag or commit subcommand
		if rest != "" && !strings.HasPrefix(rest, "-") && !strings.HasPrefix(rest, "commit") {
			// This token is the flag's value, skip it
			spIdx = strings.Index(rest, " ")
			if spIdx < 0 {
				return false
			}
			rest = strings.TrimSpace(rest[spIdx+1:])
		}
	}
	return strings.HasPrefix(rest, "commit")
}

// injectCoAuthorTrailer injects Co-Authored-By into a git commit command.
// If the command already contains the trailer, it is not duplicated.
func injectCoAuthorTrailer(cmd string) string {
	if strings.Contains(cmd, coAuthorTrailer) {
		return cmd
	}

	// Try injecting into -m "message" — append trailer to the message
	// Find the last -m flag and append trailer to its quoted string
	if idx := findLastMFlag(cmd); idx >= 0 {
		return injectIntoMFlag(cmd, idx)
	}

	// No -m flag: append --trailer
	return cmd + " --trailer \"" + coAuthorTrailer + "\""
}

// findLastMFlag finds the start index of the last -m flag in the command.
// Returns -1 if not found.
func findLastMFlag(cmd string) int {
	idx := -1
	searchFrom := 0
	for {
		found := strings.Index(cmd[searchFrom:], "-m")
		if found < 0 {
			break
		}
		absIdx := searchFrom + found
		// Make sure it's a standalone -m flag (not part of another flag like --message)
		before := ""
		if absIdx > 0 {
			before = string(cmd[absIdx-1])
		}
		after := ""
		if absIdx+2 < len(cmd) {
			after = string(cmd[absIdx+2])
		}
		if (before == " " || before == "" || before == "'") && (after == " " || after == "'" || after == "\"") {
			idx = absIdx
		}
		searchFrom = absIdx + 2
		if searchFrom >= len(cmd) {
			break
		}
	}
	return idx
}

// injectIntoMFlag appends the Co-Authored-By trailer to the message after -m.
func injectIntoMFlag(cmd string, mIdx int) string {
	// Skip past "-m"
	pos := mIdx + 2
	// Skip whitespace
	for pos < len(cmd) && cmd[pos] == ' ' {
		pos++
	}
	if pos >= len(cmd) {
		return cmd + " --trailer \"" + coAuthorTrailer + "\""
	}

	quote := cmd[pos]
	if quote != '"' && quote != '\'' {
		// Unquoted message — find end of message (next flag or end of string)
		end := strings.IndexByte(cmd[pos:], ' ')
		if end < 0 {
			return cmd + "\n\n" + coAuthorTrailer
		}
		absEnd := pos + end
		return cmd[:absEnd] + "\n\n" + coAuthorTrailer + cmd[absEnd:]
	}

	// Quoted message — find closing quote
	pos++ // skip opening quote
	closeIdx := strings.IndexByte(cmd[pos:], quote)
	if closeIdx < 0 {
		return cmd + " --trailer \"" + coAuthorTrailer + "\""
	}
	absClose := pos + closeIdx
	msg := cmd[pos:absClose]
	newMsg := msg + "\n\n" + coAuthorTrailer
	return cmd[:pos] + newMsg + cmd[absClose:]
}

// isBinaryFile checks if a file appears to be binary by reading the first 8KB
// and looking for null bytes.
func isBinaryFile(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return true
	}
	defer f.Close()
	buf := make([]byte, 8192)
	n, err := f.Read(buf)
	if err != nil {
		return true
	}
	for i := 0; i < n; i++ {
		if buf[i] == 0 {
			return true
		}
	}
	return false
}

// skipDirs are directories always skipped during file walks.
var skipDirs = map[string]bool{
	".git":         true,
	"node_modules": true,
	"vendor":       true,
	"__pycache__":  true,
}

// isGitCommand checks whether a shell command string starts with "git".
func isGitCommand(cmd string) bool {
	s := strings.TrimSpace(cmd)
	// Skip env var prefixes like "VAR=val "
	for strings.Contains(s, "=") && strings.Index(s, "=") < strings.Index(s, " ") {
		if spIdx := strings.Index(s, " "); spIdx >= 0 {
			s = strings.TrimSpace(s[spIdx+1:])
		} else {
			break
		}
	}
	return strings.HasPrefix(strings.TrimSpace(s), "git ")
}
