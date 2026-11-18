package agent

import (
	"encoding/json"
	"strings"
)

// classifyShellReadOnly reports whether a shell command line has an empty
// write surface: a re-run can never double-apply anything. It is used to
// downgrade the [non-atomic] side-effects-UNKNOWN advisory on timeouts to a
// safe-to-re-run note (#5744).
//
// Policy: fail-closed. Anything not positively classified as read-only
// (unknown binary, unknown subcommand, write redirection, command
// substitution, xargs/tee/exec-style indirection) returns false and keeps
// the existing UNKNOWN treatment.
func classifyShellReadOnly(command string) bool {
	if strings.TrimSpace(command) == "" {
		return false // nothing to classify: keep the fail-closed advisory
	}
	if strings.ContainsAny(command, "`") {
		return false
	}
	if strings.Contains(command, "$(") {
		return false
	}
	// Reuse splitShellSegments (command_cache.go): splits on newlines, ';',
	// and &&/|| and strips leading `cd` stages. Pipes are handled per segment
	// below; every stage of a pipeline must be read-only.
	for _, seg := range splitShellSegments(command) {
		for _, stage := range strings.Split(seg, "|") {
			stage = strings.TrimSpace(stage)
			if stage == "" {
				continue
			}
			if !segmentIsReadOnly(stage) {
				return false
			}
		}
	}
	return true
}

func segmentIsReadOnly(seg string) bool {
	tokens := strings.Fields(seg)
	if len(tokens) == 0 {
		return true
	}
	// Skip leading env assignments (FOO=bar cmd ...) using the shared helper.
	i := 0
	for i < len(tokens) && isEnvAssignment(tokens[i]) {
		i++
	}
	if i >= len(tokens) {
		return true // pure assignments: writes nothing to the tree
	}
	// Write redirection veto (">", ">>", or a token starting with '>').
	for _, t := range tokens[i:] {
		if t == ">" || t == ">>" || strings.HasPrefix(t, ">") {
			return false
		}
	}
	bin := stripPath(tokens[i])
	args := tokens[i+1:]

	// find with destructive flags is a write.
	if bin == "find" {
		for _, a := range args {
			if a == "-delete" || a == "-exec" || a == "-execdir" || a == "-ok" ||
				a == "-fprintf" || a == "-fprint" || a == "-fls" || strings.HasPrefix(a, "-fprint") {
				return false
			}
		}
		return true
	}

	// git: allowlist of read-only subcommands, some with argument guards.
	if bin == "git" {
		return gitSubcommandReadOnly(args)
	}
	if bin == "gh" {
		return ghSubcommandReadOnly(args)
	}
	if bin == "go" {
		return goSubcommandReadOnly(args)
	}
	return readOnlyBinaries[bin]
}

func stripPath(t string) string {
	if i := strings.LastIndex(t, "/"); i >= 0 {
		return t[i+1:]
	}
	return t
}

// gitSubcommandReadOnly: bare listing forms of otherwise-mutating subcommands
// are allowed only in their read-only argument shapes; everything unknown
// fails closed.
func gitSubcommandReadOnly(args []string) bool {
	if len(args) == 0 {
		return false // `git` alone prints usage; treat unknown for safety
	}
	switch args[0] {
	case "status", "log", "diff", "show", "fetch", "ls-remote", "rev-parse",
		"merge-base", "blame", "shortlog", "reflog", "describe", "grep",
		"cat-file", "ls-files", "ls-tree", "for-each-ref", "name-rev",
		"check-ignore", "check-attr", "var", "version", "cherry", "archive":
		return true
	case "branch", "tag":
		for _, a := range args[1:] {
			if !strings.HasPrefix(a, "-") {
				return false // positional after branch/tag creates/deletes refs
			}
		}
		return true
	case "remote":
		if len(args) == 1 {
			return true
		}
		return args[1] == "-v" || args[1] == "--verbose" || args[1] == "show"
	case "stash":
		return len(args) > 1 && (args[1] == "list" || args[1] == "show")
	case "worktree":
		return len(args) > 1 && args[1] == "list"
	case "config":
		if len(args) == 1 {
			return false
		}
		a := args[1]
		return a == "-l" || a == "--list" || strings.HasPrefix(a, "--get")
	}
	return false
}

func ghSubcommandReadOnly(args []string) bool {
	if len(args) == 0 {
		return false
	}
	switch args[0] {
	case "api":
		for i, a := range args[1:] {
			if a == "-X" || a == "--method" {
				if i+2 < len(args) {
					m := strings.ToUpper(args[i+2])
					if m != "GET" && m != "HEAD" {
						return false
					}
				}
			}
			if a == "--input" || a == "--field" || a == "-F" || a == "--raw-field" {
				return false // body-carrying flags imply a write request
			}
		}
		return true
	case "pr", "issue", "release", "run", "workflow", "repo", "org", "label", "milestone", "project", "search", "codespace", "gist", "api-token":
		if len(args) > 1 {
			switch args[1] {
			case "view", "list", "status", "checks", "diff", "url":
				return true
			}
		}
		return false
	case "auth":
		return len(args) > 1 && args[1] == "status"
	}
	return false
}

func goSubcommandReadOnly(args []string) bool {
	if len(args) == 0 {
		return false
	}
	switch args[0] {
	case "version", "env", "list", "doc", "test", "vet", "build":
		return true // cache/bin writes only; source untouched; re-run safe
	}
	return false
}

// readOnlyBinaries: binaries whose plain invocation cannot write (re-run
// always safe). Anything absent fails closed.
var readOnlyBinaries = map[string]bool{
	"cat": true, "head": true, "tail": true, "wc": true, "ls": true,
	"pwd": true, "echo": true, "printf": true, "stat": true, "file": true,
	"du": true, "df": true, "which": true, "type": true, "command": false,
	"whoami": true, "id": true, "uname": true, "date": true, "hostname": true,
	"printenv": true, "env": false, "basename": true, "dirname": true,
	"grep": true, "egrep": true, "fgrep": true, "rg": true, "jq": true,
	"sort": true, "uniq": true, "cut": true, "tr": true, "column": true,
	"diff": true, "cmp": true, "comm": true, "md5sum": true, "shasum": true,
	"sh1sum": true, "true": true, "false": true, "test": true, "[": true,
	"git": false, "gh": false, "go": false, // handled with subcommand guards
}

// shellCommandFromArgs extracts the command string from a shell-tool call's
// JSON arguments (run_command / start_command). Empty on any mismatch.
func shellCommandFromArgs(args []byte) string {
	var parsed struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal(args, &parsed); err != nil {
		return ""
	}
	return parsed.Command
}
