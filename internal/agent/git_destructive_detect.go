package agent

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/topcheer/ggcode/internal/debug"
)

type destructivePattern struct {
	name        string
	severity    string
	description string
	suggestion  string
}

type gitDestructiveState struct {
	warned map[string]bool
}

func newGitDestructiveState() *gitDestructiveState {
	return &gitDestructiveState{
		warned: make(map[string]bool),
	}
}

func (s *gitDestructiveState) reset() {
	s.warned = make(map[string]bool)
}

var (
	reGitResetHard       = regexp.MustCompile(`\bgit\s+reset\s+(-[a-zA-Z]+\s+)*--hard\b`)
	reGitForceWithLease  = regexp.MustCompile(`\bgit\s+push\s+.*--force-with-lease\b`)
	reGitBranchDelete    = regexp.MustCompile(`\bgit\s+branch\s+(-D\b|--delete\s+--force\b)`)
	reGitBranchForceMove = regexp.MustCompile(`\bgit\s+branch\s+(-f\b|--force\b)`)
	reGitDiscardAll      = regexp.MustCompile(`\bgit\s+(?:(?:checkout|switch|restore)\s+(?:(?:-[a-zA-Z]+\s+|--source(?:[= ]\S+)\s+|(?:--staged\s+)?--worktree\s+|[A-Za-z0-9_][\w./=-]*\s+|--\s+)*(?:\.\s*(?:$|&&|--|;)|--\s*\.))|switch\s+-[CcFf]\b)`)
	reGitBranchCreate    = regexp.MustCompile(`\s-b\s`)
	reChainSep           = regexp.MustCompile(`\|\||&&|\||&|;|\n`)
	reGitCheckoutForce   = regexp.MustCompile(`\bgit\s+(checkout|switch)\s+(-[a-zA-Z]*[fB][a-zA-Z]*|--force)\b`)
	reGitStashDrop       = regexp.MustCompile(`\bgit\s+stash\s+(drop|clear)\b`)
	reGitRebase          = regexp.MustCompile(`\bgit\s+rebase\b`)
	reGitFilterBranch    = regexp.MustCompile(`\bgit\s+(filter-branch|filter-repo)\b`)
	reRmRf               = regexp.MustCompile(`\brm\s+(-[a-zA-Z]*[rR][a-zA-Z]*f\b|-[a-zA-Z]*f[a-zA-Z]*[rR]\b|-[rR]f\b|-[rR]\s+-f\b|-f\s+-[rR]\b|--recursive\s+--force\b|--force\s+--recursive\b|--recursive\s+-f\b|-f\s+--recursive\b)`)
	reHeredocStart       = regexp.MustCompile(`<<-?[ \t]*['"]?([A-Za-z_][A-Za-z0-9_]*)['"]?[ \t]*(#.*)?$`)
)

func normalizeGitGlobalFlags(cmd string) string {
	lines := strings.Split(cmd, "\n")
	for li, line := range lines {
		toks := strings.Fields(line)

		for i := 0; i < len(toks); i++ {
			if strings.Trim(toks[i], "\"'") != "git" {
				continue
			}
			j := i + 1
			for j < len(toks) {
				ft := strings.Trim(toks[j], "\"'")
				if !isGitGlobalFlag(ft) {
					break
				}

				j++
				if !strings.Contains(ft, "=") && j < len(toks) {
					j++
				}
			}
			if j > i+1 && j <= len(toks) {

				out := append(append([]string{}, toks[:i+1]...), toks[j:]...)
				lines[li] = strings.Join(out, " ")
				toks = out
				i = j - 1
			}
		}
	}
	return strings.Join(lines, "\n")
}

// heredocTextReceivers lists commands whose heredoc payload is inert DATA
// (file content, filtered text) rather than executed code. Only these get
// their bodies stripped from the rm scan.
var heredocTextReceivers = map[string]bool{
	"cat": true, "tee": true, "wc": true, "md5sum": true, "sha1sum": true,
	"sha256sum": true, "head": true, "tail": true, "less": true, "more": true,
	"grep": true, "egrep": true, "fgrep": true, "echo": true, "printf": true,
	"sort": true, "uniq": true,
}

// heredocPrefixWords are command wrappers that precede the real receiver
// (sudo/env/time/... bash). They are skipped when locating the receiver
// word; env-style VAR=val assignments before the command are skipped the
// same way.
var heredocPrefixWords = map[string]bool{
	"sudo": true, "env": true, "time": true, "nice": true, "nohup": true,
	"command": true, "builtin": true,
}

// heredocReceiverKeepsBody reports whether the heredoc body introduced on
// this start line must stay visible to the destructive-command scan.
//
// #5315 stripped heredoc bodies unconditionally, which muted real `rm -rf`
// invocations executed by interpreter heredocs (#5328). The fail-safe
// default is now KEEP (#5329 review): a body is treated as inert text
// (stripped) only when the receiver is a known text consumer
// (cat/tee/grep/... — see heredocTextReceivers) or the line is a bare
// output redirection. Everything else — interpreters (bash/sh/python3/...),
// path forms (/bin/bash), wrapped forms (sudo/env/time ... bash), container
// execs (docker exec ... sh), and ANY unrecognized command — keeps its
// body in the scanned text. Rationale: an unclassified receiver that
// executes the body is precisely the silent-bypass case; we accept
// #5315-style false positives on unrecognized text receivers rather than
// false negatives on unrecognized executors.
func heredocReceiverKeepsBody(line string) bool {
	stripped := strings.TrimSpace(line)
	// Line starts with `<<`: bare stdin redirection with no command word —
	// payload is data, keep stripping.
	if m := reHeredocStart.FindStringIndex(stripped); m != nil && m[0] == 0 {
		return false
	}
	fields := strings.Fields(stripped)
	if len(fields) == 0 {
		return false
	}
	// Leading output redirection (`>> file <<EOF`) without a command word:
	// payload becomes file content — data.
	if f := strings.Trim(fields[0], "\"'"); strings.HasPrefix(f, ">") {
		return false
	}
	receiver := heredocReceiverWord(fields)
	if receiver == "" {
		return false
	}
	// Normalize path forms (`/bin/bash`, `./tool`) to their base name so
	// whitelist misses cannot hide behind a path prefix.
	if i := strings.LastIndexAny(receiver, `/\`); i >= 0 {
		receiver = receiver[i+1:]
	}
	return !heredocTextReceivers[receiver]
}

// heredocReceiverWord walks the fields of a heredoc start line and returns
// the first word that can be the receiver: leading VAR=val assignments and
// wrapper words (sudo/env/time/...) are skipped. Walking stops at the first
// non-wrapper token, so flag-carrying forms like `sudo -u x bash` yield
// "-u" — not a text receiver, so the body is kept (fail-safe direction).
func heredocReceiverWord(fields []string) string {
	for _, f := range fields {
		f = strings.Trim(f, "\"'")
		if strings.Contains(f, "=") && !strings.HasPrefix(f, "-") && !strings.HasPrefix(f, "$") {
			continue
		}
		if heredocPrefixWords[f] {
			continue
		}
		return f
	}
	return ""
}

func stripHeredocBodies(cmd string) string {
	lines := strings.Split(cmd, "\n")
	out := make([]string, 0, len(lines))
	inHeredoc := false
	terminator := ""
	for _, line := range lines {
		if inHeredoc {
			if strings.TrimSpace(line) == terminator {
				inHeredoc = false
			}
			continue
		}
		out = append(out, line)
		if m := reHeredocStart.FindStringSubmatch(line); m != nil && !heredocReceiverKeepsBody(line) {
			inHeredoc = true
			terminator = m[1]
		}
	}
	return strings.Join(out, "\n")
}

// rmCommandWrapperWords are the words that may sit between a chain
// segment start and rm without demoting rm from command position. The
// base set reuses heredocPrefixWords (this file's wrapper lexicon) and
// adds xargs/doas so the agent-side gate is not weaker than the
// permission layer's rmCmdAnchor, which anchors all of
// sudo/doas/xargs/nohup/env/nice ahead of rm (internal/permission).
var rmCommandWrapperWords = func() map[string]bool {
	m := make(map[string]bool, len(heredocPrefixWords)+2)
	for w := range heredocPrefixWords {
		m[w] = true
	}
	m["xargs"] = true
	m["doas"] = true
	return m
}()

// normalizeCommandWord reduces a path-invoked binary (/bin/rm, ./rm,
// /usr/bin/sudo) to its base name and drops surrounding quotes, so
// whitelist misses cannot hide behind a path prefix. Mirrors the
// normalization in heredocReceiverKeepsBody.
func normalizeCommandWord(w string) string {
	w = strings.Trim(w, "\"'")
	if i := strings.LastIndexAny(w, `/\`); i >= 0 {
		w = w[i+1:]
	}
	return w
}

// #5315: rm must appear at a command position - a chain segment start
// (after ; && || | & or newline) with optional env-prefix assignments
// (VAR=val ...). Text mentioning rm anywhere else (echo, grep args,
// comments) is not an invocation.
//
// #5342: command-position also survives wrapper prefixes
// (sudo/nohup/xargs/command/..., path forms normalized, interleaved with
// env assignments), and EVERY rm segment of the chain is returned, not
// just the first - `rm file && rm -rf dir` must not hide its recursive
// force-delete behind the plain leading segment.
func rmCommandSegments(cmd string) []string {
	cleaned := stripHeredocBodies(cmd)
	var segs []string
	for _, seg := range reChainSep.Split(cleaned, -1) {
		fields := strings.Fields(strings.TrimSpace(seg))
		i := 0
		for i < len(fields) {
			if strings.Contains(fields[i], "=") && !strings.HasPrefix(fields[i], "-") && !strings.HasPrefix(fields[i], "$") {
				i++
				continue
			}
			if rmCommandWrapperWords[normalizeCommandWord(fields[i])] {
				i++
				continue
			}
			break
		}
		fields = fields[i:]
		if len(fields) > 0 && normalizeCommandWord(fields[0]) == "rm" {
			segs = append(segs, strings.Join(fields, " "))
		}
	}
	return segs
}

func detectDestructiveInShellCommand(cmd string) []destructivePattern {
	if cmd == "" {
		return nil
	}
	cmd = normalizeGitGlobalFlags(cmd)
	var found []destructivePattern

	type patternCheck struct {
		re     *regexp.Regexp
		result destructivePattern
	}

	if reGitForceWithLease.MatchString(cmd) {
		found = append(found, destructivePattern{
			name:        "force_with_lease",
			severity:    "warning",
			description: "git push --force-with-lease rewrites remote history. While safer than --force, it can still overwrite collaborators' work if they've pushed since your last fetch.",
			suggestion:  "Consider `git push` normally, or coordinate with collaborators first.",
		})
	} else if isForcePushCommand(cmd) {
		found = append(found, destructivePattern{
			name:        "force_push",
			severity:    "critical",
			description: "git push --force overwrites remote history, permanently destroying any commits that collaborators may have pushed. This is one of the most dangerous git operations.",
			suggestion:  "Use `git push --force-with-lease` instead, or better yet, create a new commit rather than rewriting history.",
		})
	}

	if reGitResetHard.MatchString(cmd) {
		found = append(found, destructivePattern{
			name:        "reset_hard",
			severity:    "critical",
			description: "git reset --hard permanently discards all uncommitted changes (staged and unstaged). This cannot be undone.",
			suggestion:  "Use `git stash` to temporarily save changes, or `git reset --soft` to unstage without losing work.",
		})
	}

	if isCleanForceCommand(cmd) {

		found = append(found, destructivePattern{
			name:        "clean_force",
			severity:    "critical",
			description: "git clean -f permanently deletes untracked files. Combined with -d, it removes entire untracked directories. This cannot be undone.",
			suggestion:  "Use `git clean -n` (dry run) first to preview what will be deleted, or `git stash -u` to stash untracked files.",
		})
	}

	// #5217: argv-grounded - only fire when a shell segment actually
	// INVOKES git; prose quoting "git branch -D" inside a gh comment body
	// or review text must not trigger a CRITICAL.
	if branchForceDeleteInGitSegment(cmd) {
		found = append(found, destructivePattern{
			name:        "branch_force_delete",
			severity:    "critical",
			description: "git branch -D force-deletes a branch, permanently losing any unmerged commits on that branch.",
			suggestion:  "Use `git branch -d` (safe delete) which only succeeds if the branch is fully merged.",
		})
	}

	if branchForceMoveInGitSegment(cmd) { // #5217 argv-grounding, same family
		found = append(found, destructivePattern{
			name:     "branch_force_move",
			severity: "warning",
			description: "git branch -f moves a branch pointer, abandoning commits that were on it " +
				"(they remain reachable via reflog for ~90 days, unlike reset --hard's uncommitted losses).",
			suggestion: "Confirm the target branch really should be re-pointed; the old tip stays in the reflog (git reflog show <branch>).",
		})
	}

	if discardAllFires(cmd) {
		found = append(found, destructivePattern{
			name:        "discard_all",
			severity:    "critical",
			description: "This command discards ALL uncommitted changes in the working tree. This cannot be undone.",
			suggestion:  "Use `git stash` to save changes before discarding, or discard specific files with `git checkout -- <file>`.",
		})
	}

	if reGitStashDrop.MatchString(cmd) {
		found = append(found, destructivePattern{
			name:        "stash_drop",
			severity:    "warning",
			description: "git stash drop/clear permanently removes stashed changes. If the stashed work is needed later, it cannot be recovered (except via fsck within ~30 days).",
			suggestion:  "Consider keeping stashes until you're certain they're no longer needed.",
		})
	}

	if reGitRebase.MatchString(cmd) {
		found = append(found, destructivePattern{
			name:        "rebase",
			severity:    "warning",
			description: "git rebase rewrites commit history. If these commits have been pushed, collaborators will need to reset their local branches. Conflicts may require manual resolution.",
			suggestion:  "Consider `git merge` instead, which preserves history. If rebasing, ensure you're on a local (unpushed) branch.",
		})
	}

	if reGitFilterBranch.MatchString(cmd) {
		found = append(found, destructivePattern{
			name:        "filter_branch",
			severity:    "critical",
			description: "git filter-branch/filter-repo rewrites entire repository history. This is extremely dangerous on shared repositories and can corrupt collaborator clones.",
			suggestion:  "Only use on local/personal repositories. Communicate with all collaborators before rewriting shared history.",
		})
	}

	// #5315: only an actual rm invocation (command position, heredoc
	// bodies stripped) triggers the critical rm_rf pattern; text
	// mentioning the pattern does not. #5342: every rm segment of the
	// chain is checked, so a later `rm -rf` cannot hide behind a leading
	// plain `rm`, and wrapper-prefixed forms (sudo/nohup/xargs/command ...)
	// count as invocations too.
	for _, rmSeg := range rmCommandSegments(cmd) {
		if reRmRf.MatchString(rmSeg) {
			found = append(found, destructivePattern{
				name:        "rm_rf",
				severity:    "critical",
				description: "rm -rf recursively force-deletes files without confirmation. This is extremely dangerous and can destroy entire project directories.",
				suggestion:  "Be very specific about paths. Consider moving to a temp directory instead of deleting. Never use rm -rf on root, home, or project root paths.",
			})
			break
		}
	}

	return found
}

func detectDestructiveInGitTool(toolName string, args json.RawMessage) []destructivePattern {
	if len(args) == 0 {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal(args, &m); err != nil {
		return nil
	}

	var found []destructivePattern

	switch toolName {
	case "git_reset":
		mode, _ := m["mode"].(string)
		if mode == "hard" {
			found = append(found, destructivePattern{
				name:        "reset_hard",
				severity:    "critical",
				description: "git reset --hard permanently discards all uncommitted changes (staged and unstaged). This cannot be undone.",
				suggestion:  "Use mode 'soft' to unstage without losing work, or mode 'mixed' (default) to unstage and keep changes.",
			})
		}
	case "git_stash":
		action, _ := m["action"].(string)
		if action == "drop" || action == "clear" {
			found = append(found, destructivePattern{
				name:        "stash_drop",
				severity:    "warning",
				description: fmt.Sprintf("git stash %s permanently removes stashed changes.", action),
				suggestion:  "Consider keeping stashes until you're certain they're no longer needed.",
			})
		}
	case "git_checkout":

	}

	return found
}

func (a *Agent) checkGitDestructive(toolName string, args json.RawMessage) string {
	if a.destructiveGuard == nil {
		return ""
	}

	var patterns []destructivePattern
	shellCmd := ""

	switch toolName {
	case "run_command", "start_command":
		var m map[string]any
		if err := json.Unmarshal(args, &m); err == nil {
			if cmd, ok := m["command"].(string); ok {
				shellCmd = cmd
				patterns = detectDestructiveInShellCommand(cmd)
			}
		}
	case "git_reset", "git_stash", "git_revert", "git_checkout":
		patterns = detectDestructiveInGitTool(toolName, args)
	case "file_ops":
		var m struct {
			Operations []struct {
				Action    string `json:"action"`
				Recursive bool   `json:"recursive"`
			} `json:"operations"`
		}
		if err := json.Unmarshal(args, &m); err == nil {
			for _, op := range m.Operations {
				if op.Action == "delete" && op.Recursive {
					patterns = append(patterns, destructivePattern{
						name:        "rm_rf",
						severity:    "critical",
						description: "file_ops recursive delete force-deletes a directory tree without confirmation (os.RemoveAll). This is extremely dangerous and can destroy entire project directories.",
						suggestion:  "Be very specific about paths. Consider moving to a temp directory instead of deleting. Never recursively delete root, home, or project root paths.",
					})
				}
			}
		}
	}

	if len(patterns) == 0 {
		return ""
	}

	var newPatterns []destructivePattern
	for _, p := range patterns {
		key := p.name + "|" + shellCmd
		if !a.destructiveGuard.warned[key] {
			newPatterns = append(newPatterns, p)
			a.destructiveGuard.warned[key] = true
		}
	}

	if len(newPatterns) == 0 {
		return ""
	}

	debug.Log("git_destructive", "detected %d destructive pattern(s) in tool %s", len(newPatterns), toolName)

	var sb strings.Builder
	sb.WriteString("\n")
	hasCritical := false
	for _, p := range newPatterns {
		if p.severity == "critical" {
			hasCritical = true
		}
	}

	if hasCritical {
		sb.WriteString("DESTRUCTIVE GIT OPERATION DETECTED\n")
	} else {
		sb.WriteString("Git operation caution\n")
	}

	for _, p := range newPatterns {
		icon := "Warning:"
		if p.severity == "critical" {
			icon = "CRITICAL:"
		}
		sb.WriteString(fmt.Sprintf("\n%s [%s]\n", icon, p.name))
		sb.WriteString(fmt.Sprintf("  Risk: %s\n", p.description))
		sb.WriteString(fmt.Sprintf("  Safer alternative: %s\n", p.suggestion))
	}

	sb.WriteString("\nIf this operation is intentional and necessary, proceed. Otherwise, use a safer alternative.\n")

	return sb.String()
}

func isForcePushCommand(cmd string) bool {

	cmd = normalizeGitGlobalFlags(cmd)
	for _, line := range strings.Split(cmd, "\n") {
		if forcePushSingleLine(line) {
			return true
		}
	}
	return false
}

func forcePushSingleLine(cmd string) bool {
	toks := strings.Fields(cmd)
	for i, t := range toks {

		st := strings.Trim(t, "\"'")
		if st != "push" || i == 0 {
			continue
		}
		if strings.Trim(toks[i-1], "\"'") != "git" {
			continue
		}
		for _, tok := range toks[i+1:] {

			switch {
			case tok == "&&" || tok == "||" || tok == "|" || tok == "&":
				return false
			case strings.ContainsAny(tok, ";\n"):

				return false
			case strings.HasPrefix(tok, ";"):
				return false
			}
			switch {
			case tok == "--force", tok == "--force-with-lease=false":
				return true
			case tok == "--force-with-lease", tok == "--force-with-lease=true":

				continue
			case strings.HasPrefix(tok, "--"):
				continue
			case strings.HasPrefix(tok, "+"):
				return true
			case strings.HasPrefix(tok, "-"):
				if strings.ContainsAny(tok, "f") {
					return true
				}
			}
		}
		return false
	}
	return false
}

func isCleanForceCommand(cmd string) bool {

	for _, line := range strings.Split(cmd, "\n") {
		if cleanForceSingleLine(line) {
			return true
		}
	}
	return false
}

func isGitGlobalFlag(tok string) bool {
	if !strings.HasPrefix(tok, "-") {
		return false
	}
	switch tok {
	case "-C", "--no-pager", "--no-optional-locks", "--literal-pathspecs",
		"--glob-pathspecs", "--noglob-pathspecs", "--icase-pathspecs",
		"--no-replace-objects", "--bare", "--no-renames":
		return true
	}

	return strings.HasPrefix(tok, "-c") || strings.HasPrefix(tok, "--git-dir") ||
		strings.HasPrefix(tok, "--work-tree") || strings.HasPrefix(tok, "--namespace")
}

func cleanForceSingleLine(cmd string) bool {
	toks := strings.Fields(cmd)
	for i, t := range toks {
		st := strings.Trim(t, "\"'")
		if st != "clean" || i == 0 {
			continue
		}

		if !gitChainPrecedes(toks, i) {
			continue
		}
		hasF, hasN := false, false
		for _, tok := range toks[i+1:] {

			if tok == "&&" || tok == "||" || tok == "|" || tok == "&" ||
				strings.ContainsAny(tok, ";\n") || strings.HasPrefix(tok, ";") {
				break
			}
			switch tok {
			case "--dry-run":
				hasN = true
				continue
			case "--force":
				hasF = true
				continue
			}
			if strings.HasPrefix(tok, "-") && !strings.HasPrefix(tok, "--") {
				if strings.ContainsAny(tok, "f") {
					hasF = true
				}
				if strings.ContainsAny(tok, "n") {
					hasN = true
				}
			}
		}
		return hasF && !hasN
	}
	return false
}

func gitChainPrecedes(toks []string, i int) bool {
	if i == 0 {
		return false
	}
	if strings.Trim(toks[i-1], "\"'") == "git" {
		return true
	}

	j := i - 1
	for j >= 1 {
		fl := strings.Trim(toks[j], "\"'")
		if fl == "git" {
			return true
		}
		if !isGitGlobalFlag(fl) {

			prev := strings.Trim(toks[j-1], "\"'")
			if isGitGlobalFlag(prev) && flagTakesValue(prev) && !strings.Contains(toks[j-1], "=") {
				j -= 2
				continue
			}
			return false
		}

		j--
	}
	return j >= 0 && strings.Trim(toks[j], "\"'") == "git"
}

func flagTakesValue(fl string) bool {
	switch {
	case fl == "-C", strings.HasPrefix(fl, "--git-dir"),
		strings.HasPrefix(fl, "--work-tree"), strings.HasPrefix(fl, "--namespace"):
		return true
	case fl == "-c":
		return true
	}
	return false
}

func discardAllFires(cmd string) bool {
	for _, seg := range reChainSep.Split(cmd, -1) {
		if seg == "" {
			continue
		}
		if (reGitDiscardAll.MatchString(seg) || reGitCheckoutForce.MatchString(seg)) &&
			!reGitBranchCreate.MatchString(seg) {
			return true
		}
	}
	return false
}
