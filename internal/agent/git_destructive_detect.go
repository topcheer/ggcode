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

// #5315: rm must appear at a command position - a chain segment start
// (after ; && || | & or newline) with optional env-prefix assignments
// (VAR=val ...). Text mentioning rm anywhere else (echo, grep args,
// comments) is not an invocation.
//
// #5342: wrapper prefixes (sudo/nohup/xargs/env/timeout ...) hide the rm
// word behind a leading token, so the fields[0]=="rm" check missed
// invocations like "sudo rm -rf x" - the gate went blind exactly when
// privilege or batching was involved. Strip known wrapper words (same
// family as heredocPrefixWords plus xargs/timeout exec-style launchers)
// after env-prefix assignments before matching.
// #5342 increment (#5345): doas joins the table (permission layer's
// rmCmdAnchor precedent), and wrapper/command-position words are
// normalized to their final path component before lookup so path forms
// (`/usr/bin/sudo rm -rf`, `sudo /bin/rm -rf`) cannot hide behind the
// prefix - same normalization philosophy as heredocReceiverKeepsBody.
var rmWrapperWords = map[string]bool{
	"sudo": true, "nohup": true, "env": true, "nice": true, "time": true,
	"command": true, "builtin": true, "xargs": true, "timeout": true,
	"stdbuf": true, "setsid": true, "doas": true,
}

// wrapperValuelessFlags lists wrapper flags that take NO value (#5456):
// xargs -0/--null (NUL separator), env -i (empty environment). Tokens not
// in this table keep the legacy value-consuming behavior, so -u root and
// nice -n 5 still work.
var wrapperValuelessFlags = map[string]bool{
	"-0": true, "--null": true, "-i": true,
}

// isNumericToken reports whether s is a pure number (timeout 30 style
// wrapper arguments).
func isNumericToken(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// commandWordBase normalizes a path-form token (/usr/bin/sudo, ./rm,
// C:\tools\rm) to its final component so wrapper-table lookups and the
// command-position rm check see the command NAME, not its location
// (#5342 increment, #5345 path-form classes).
func commandWordBase(tok string) string {
	if i := strings.LastIndexAny(tok, `/\`); i >= 0 {
		return tok[i+1:]
	}
	return tok
}

func firstRmCommandSegment(cmd string) string {
	cleaned := stripHeredocBodies(cmd)
	for _, seg := range reChainSep.Split(cleaned, -1) {
		fields := strings.Fields(strings.TrimSpace(seg))
		for len(fields) > 0 && strings.Contains(fields[0], "=") && !strings.HasPrefix(fields[0], "-") && !strings.HasPrefix(fields[0], "$") {
			fields = fields[1:]
		}
		// #5342: strip wrapper words AND their arguments (sudo -u root,
		// timeout 30, xargs -0 ...) until the head word is reached. A
		// wrapper flag, a flag's VALUE (sudo -u root), or a numeric
		// argument (timeout 30) only appears after a wrapper word, so
		// non-wrapper commands (echo/git/...) break out here and keep
		// their tokens intact (#5315 negative pins stay safe).
		// #5342 increment (#5345): wrapper words and the head word are
		// both normalized to their final path component first, so
		// /usr/bin/sudo and /bin/rm classify like their base names.
		seenWrapper := false
		prevWasFlag := false
		for len(fields) > 0 {
			h := commandWordBase(fields[0])
			if rmWrapperWords[h] {
				seenWrapper = true
				prevWasFlag = false
				fields = fields[1:]
				continue
			}
			if seenWrapper {
				// #5456: three token shapes after a wrapper word do NOT
				// consume a following value:
				//   self-valued long flags (--preserve-env=PATH) - the
				//     '=' carries the value inside the token itself;
				//   known valueless flags (xargs -0/--null, env -i);
				//   env-assignment tokens (env VAR=1 nice -n 5 rm ...)
				//     - assignments may interleave anywhere after
				//       wrapper words, and skipping them keeps the real
				//       command head reachable. Over-consuming a pseudo
				//       assignment here is fail-safe: this loop only
				//       locates the rm head, and a false assignment
				//       never looks like "rm".
				if strings.HasPrefix(h, "-") && strings.Contains(fields[0], "=") {
					fields = fields[1:]
					continue
				}
				if wrapperValuelessFlags[h] {
					fields = fields[1:]
					continue
				}
				if !strings.HasPrefix(h, "-") && !isNumericToken(h) && !strings.HasPrefix(h, "$") && strings.Contains(fields[0], "=") {
					fields = fields[1:]
					continue
				}
				if strings.HasPrefix(h, "-") || isNumericToken(h) || prevWasFlag {
					prevWasFlag = strings.HasPrefix(h, "-") && !isNumericToken(h)
					fields = fields[1:]
					continue
				}
			}
			break
		}
		if len(fields) > 0 && commandWordBase(fields[0]) == "rm" {
			seg2 := strings.Join(fields, " ")
			// #5342 increment (carrier core): a plain rm segment is not
			// the answer. `rm x && rm -rf d` previously returned "rm x"
			// from the FIRST rm segment and the caller's reRmRf check
			// then went blind on the recursive segment; keep scanning
			// and return only the first segment that actually carries
			// recursive force flags. Advisory semantics stay one rm_rf
			// per command (pinned in zz_issue5342_increment_test.go).
			if reRmRf.MatchString(seg2) {
				return seg2
			}
			continue
		}
	}
	return ""
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
	// mentioning the pattern does not.
	if rmSeg := firstRmCommandSegment(cmd); rmSeg != "" && reRmRf.MatchString(rmSeg) {
		found = append(found, destructivePattern{
			name:        "rm_rf",
			severity:    "critical",
			description: "rm -rf recursively force-deletes files without confirmation. This is extremely dangerous and can destroy entire project directories.",
			suggestion:  "Be very specific about paths. Consider moving to a temp directory instead of deleting. Never use rm -rf on root, home, or project root paths.",
		})
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
