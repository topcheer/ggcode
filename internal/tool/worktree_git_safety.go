package tool

import (
	"context"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// worktreeGitSafetyProbeTimeout bounds every git probe used by the
// worktree/push safety hints below. These probes ride the run_command
// result AFTER the user's command completed; they must never turn a
// fast successful command into a slow one on a wedged repository.
const worktreeGitSafetyProbeTimeout = 3 * time.Second

// worktreeSafetyMarker is the footer tag prefixing the detached-HEAD and
// push-failure advisories so they are greppable in tests and recognizable
// in agent transcripts.
const worktreeSafetyMarker = "[worktree safety]"

// pushDetachedPatterns match the exact stderr phrase family git emits when
// `git push` runs from a detached HEAD (or any abbreviated-ref source that
// cannot resolve to refs/heads/*): git refuses the destination refspec and
// prints "The destination you provided is not a full refname". That message
// does not appear for any ordinary push failure (auth, non-fast-forward,
// connectivity), so it is a precise detached-HEAD fingerprint at push time.
var pushDetachedPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)destination .{0,40}is not a full refname`),
	regexp.MustCompile(`(?i)fully qualify the ref`),
	regexp.MustCompile(`(?i)hint: .{0,80}<src> part of the refspec`),
}

// splitCommandSegments splits a shell command string on unquoted command
// separators (`;`, `&&`, `||`, `|`, and newlines). Segments that lie inside
// single or double quotes are kept intact; an unterminated quote degrades to
// "rest of string is one segment". This is deliberately lightweight: the
// callers only need per-segment bin+args classification, not full shell
// parsing (substitutions and redirections inside a segment are irrelevant
// to whether that segment is a `git worktree add` or `git push` call).
func splitCommandSegments(cmd string) []string {
	var segs []string
	var cur strings.Builder
	var quote rune
	for _, r := range cmd {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			}
			cur.WriteRune(r)
		case r == '\'' || r == '"':
			quote = r
			cur.WriteRune(r)
		case r == ';' || r == '\n' || r == '|' || r == '&':
			// `&&`/`||`/`|`/`&` are separators for our purposes: the
			// classification only needs "is there a git worktree add / git
			// push segment".
			segs = append(segs, cur.String())
			cur.Reset()
		default:
			cur.WriteRune(r)
		}
	}
	segs = append(segs, cur.String())
	out := segs[:0]
	for _, s := range segs {
		if strings.TrimSpace(s) != "" {
			out = append(out, s)
		}
	}
	return out
}

// shellQuoteSplit splits a single command segment into words, honoring
// single/double quotes and backslash escapes. Unterminated quotes yield the
// remainder as one word. Mirrors util-level shell splitting conventions used
// by the gate's env-prefix handling without pulling in a full parser.
func shellQuoteSplit(s string) []string {
	var words []string
	var cur strings.Builder
	hasWord := false
	var quote rune
	flush := func() {
		if hasWord {
			words = append(words, cur.String())
			cur.Reset()
			hasWord = false
		}
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case quote != 0:
			if c == '\\' && quote == '"' && i+1 < len(s) {
				i++
				cur.WriteByte(s[i])
				continue
			}
			if c == byte(quote) {
				quote = 0
				continue
			}
			cur.WriteByte(c)
		case c == '\'' || c == '"':
			quote = rune(c)
			hasWord = true
		case c == '\\' && i+1 < len(s):
			i++
			cur.WriteByte(s[i])
			hasWord = true
		case c == ' ' || c == '\t' || c == '\n':
			flush()
		default:
			cur.WriteByte(c)
			hasWord = true
		}
	}
	flush()
	return words
}

// resolveGitSubcommand returns the git subcommand for one command segment
// ("git -c x=y worktree add ..." -> "worktree"), skipping global flags that
// take a value and env-var prefixes. Empty string when the segment is not a
// git invocation.
func resolveGitSubcommand(segment string) string {
	words := shellQuoteSplit(segment)
	i := 0
	for i < len(words) && isShellEnvAssignment(words[i]) {
		i++
	}
	if i >= len(words) || words[i] != "git" {
		return ""
	}
	i++
	// Global flags before the subcommand: --git-dir=X / --work-tree=X are
	// value-attached; -C takes a separate value; -c and --exec-path take a
	// separate value; joined short forms (-cfoo=bar) are one word.
	for i < len(words) {
		w := words[i]
		switch {
		case w == "-C" || w == "-c" || w == "--exec-path" || w == "--namespace":
			i += 2
		case strings.HasPrefix(w, "--") && strings.Contains(w, "="):
			i++
		case strings.HasPrefix(w, "-"):
			i++
		default:
			return w
		}
	}
	return ""
}

// isShellEnvAssignment reports whether tok looks like KEY=VALUE (a leading
// shell environment assignment). It reuses the same shape check as the
// agent-side isEnvAssignment without depending on the agent package.
func isShellEnvAssignment(tok string) bool {
	eq := strings.Index(tok, "=")
	if eq <= 0 {
		return false
	}
	for j := 0; j < eq; j++ {
		c := tok[j]
		if !(c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || (j > 0 && c >= '0' && c <= '9')) {
			return false
		}
	}
	return true
}

// worktreeAddTarget analyzes one `git worktree add` command segment and
// reports whether it is worth probing for a detached HEAD. Per #4995 the
// managed paths (enter_worktree's own -b flow, and `git worktree add
// <path>` with no commit-ish) are safe; the trap is any explicit
// commit-ish target without -b/-B. Empirically (git 2.56) EVERY such form
// detaches except a SHORT LOCAL BRANCH NAME: SHAs, remote-tracking refs,
// `refs/heads/<branch>` (!), tags, and rev expressions all land detached.
// Since the shape alone cannot distinguish "short local branch name" from
// "tag name", every two-positional form is probe-worthy and the post-run
// symbolic-ref probe (not the string shape) decides whether the advisory
// fires. Explicit --detach is deliberate (read-only review worktrees) and
// is deliberately NOT flagged.
type worktreeAddAnalysis struct {
	prone bool
	path  string
}

func analyzeWorktreeAdd(segment string) worktreeAddAnalysis {
	words := shellQuoteSplit(segment)
	i := 0
	for i < len(words) && isShellEnvAssignment(words[i]) {
		i++
	}
	if i >= len(words) || words[i] != "git" {
		return worktreeAddAnalysis{}
	}
	i++
	// Skip global flags (see resolveGitSubcommand).
	for i < len(words) {
		w := words[i]
		if w == "-C" || w == "-c" || w == "--exec-path" || w == "--namespace" {
			i += 2
			continue
		}
		if strings.HasPrefix(w, "-") {
			i++
			continue
		}
		break
	}
	if i >= len(words) || words[i] != "worktree" {
		return worktreeAddAnalysis{}
	}
	i++
	if i >= len(words) || words[i] != "add" {
		return worktreeAddAnalysis{}
	}
	i++

	hasB := false
	explicitDetach := false
	positionals := []string{}
	for ; i < len(words); i++ {
		w := words[i]
		switch {
		case w == "-b" || w == "-B" || strings.HasPrefix(w, "-b") || strings.HasPrefix(w, "-B"):
			hasB = true
		case w == "--detach" || strings.HasPrefix(w, "--detach="):
			explicitDetach = true
		case strings.HasPrefix(w, "--") && strings.Contains(w, "="):
			// value-attached long flag (--checkout=... ), consumes nothing extra
		case strings.HasPrefix(w, "-"):
		default:
			positionals = append(positionals, w)
		}
	}
	if hasB || explicitDetach {
		return worktreeAddAnalysis{}
	}
	// Detach decision per #4995 (verified against git 2.56):
	// - explicit --detach: deliberate, never flagged.
	// - one positional (path only): git checks out the current branch - safe.
	// - two+ positionals (path + commit-ish): only a SHORT LOCAL BRANCH NAME
	//   attaches; SHAs, remote-tracking refs, tags, rev expressions, and even
	//   fully-qualified `refs/heads/<branch>` (!) detach. The string shape
	//   cannot tell a local branch from a tag, so probe every two-positional
	//   form and let the symbolic-ref result decide.
	path := ""
	if len(positionals) > 0 {
		path = positionals[0]
	}
	if len(positionals) < 2 {
		return worktreeAddAnalysis{path: path}
	}
	return worktreeAddAnalysis{prone: true, path: path}
}

// detectWorktreeAddSegments returns the segments of cmd that are `git
// worktree add` invocations with a detach-prone target shape.
func detectWorktreeAddSegments(cmd string) []worktreeAddAnalysis {
	var out []worktreeAddAnalysis
	for _, seg := range splitCommandSegments(cmd) {
		if resolveGitSubcommand(seg) != "worktree" {
			continue
		}
		if a := analyzeWorktreeAdd(seg); a.prone {
			out = append(out, a)
		}
	}
	return out
}

// isGitPushSegment reports whether cmd contains a `git push` segment.
func isGitPushSegment(cmd string) bool {
	for _, seg := range splitCommandSegments(cmd) {
		if resolveGitSubcommand(seg) == "push" {
			return true
		}
	}
	return false
}

// isDetachedHead reports whether dir is a git checkout whose HEAD is
// detached (symbolic-ref fails). False on any probe failure - hints must
// never fabricate a warning on a broken repository.
func isDetachedHead(ctx context.Context, dir string) bool {
	if dir == "" {
		return false
	}
	cctx, cancel := context.WithTimeout(ctx, worktreeGitSafetyProbeTimeout)
	defer cancel()
	cmd := exec.CommandContext(cctx, "git", "symbolic-ref", "-q", "HEAD")
	cmd.Dir = dir
	return cmd.Run() != nil
}

// detectDetachedWorktreePush analyzes a failed `git push` result: when the
// failure carries git's detached-HEAD fingerprint AND the push really ran
// from a detached HEAD checkout, it returns an actionable recovery hint
// (create branch + push a fully-qualified refspec). Empty string otherwise.
func detectDetachedWorktreePush(ctx context.Context, command, output, errOutput string, workingDir string) string {
	if !isGitPushSegment(command) {
		return ""
	}
	combined := output + "\n" + errOutput
	matched := false
	for _, p := range pushDetachedPatterns {
		if p.MatchString(combined) {
			matched = true
			break
		}
	}
	if !matched {
		return ""
	}
	// Confirm with the filesystem before claiming detached HEAD: the same
	// stderr family fires for any abbreviated-ref source, and hinting
	// "you are detached" on an attached checkout would mislead recovery.
	if !isDetachedHead(ctx, workingDir) {
		return ""
	}
	return worktreeSafetyMarker + " push failed from a detached HEAD: commits here are not on any branch, so `git push origin HEAD` has no branch destination to update.\n" +
		"Recover with:\n" +
		"  1. git switch -c <branch>              (keep the commits on a new branch)\n" +
		"  2. git push -u origin <branch>         (push the named branch)\n" +
		"Or without switching: git push origin HEAD:refs/heads/<branch> to push the detached commits to a remote branch of that name (#4995)."
}

// verifyWorktreeAddResult probes each detach-prone `git worktree add`
// target after the command succeeded and returns an advisory footer naming
// the worktrees that actually landed detached. Pure advisory: read-only
// review worktrees are legitimate; the notice tells commit+push flows to
// create a branch before committing.
func verifyWorktreeAddResult(ctx context.Context, command, workingDir string) string {
	analyses := detectWorktreeAddSegments(command)
	if len(analyses) == 0 {
		return ""
	}
	var detached []string
	for _, a := range analyses {
		path := a.path
		if path == "" {
			continue
		}
		if !filepath.IsAbs(path) {
			if workingDir != "" {
				path = filepath.Join(workingDir, path)
			}
		}
		if isDetachedHead(ctx, path) {
			detached = append(detached, a.path)
		}
	}
	if len(detached) == 0 {
		return ""
	}
	return worktreeSafetyMarker + " detached HEAD: " + strings.Join(detached, ", ") + "\n" +
		"This worktree has no branch - `git push origin HEAD` will fail with \"destination ... not a full refname\".\n" +
		"Read-only inspection is fine. Before committing/pushing, create a branch:\n" +
		"  git -C <worktree> switch -c <branch>\n" +
		"  git -C <worktree> push -u origin <branch>\n" +
		"Or push detached commits directly: git -C <worktree> push origin HEAD:refs/heads/<branch> (#4995)."
}
