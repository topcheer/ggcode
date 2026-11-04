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
// reports whether it is prone to leaving the new worktree on a detached
// HEAD. Per #4995 the managed paths (enter_worktree's own -b flow, and
// `git worktree add <path>` with no commit-ish) are safe; the trap is the
// commit-ish forms without -b/-B: SHA targets, remote-tracking refs
// (origin/<branch>), and any path-qualified ref that is not a local branch.
// Explicit --detach is deliberate (read-only review worktrees) and is
// deliberately NOT flagged.
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
		case w == "--detach":
			explicitDetach = true
		case strings.HasPrefix(w, "--") && strings.Contains(w, "="):
			// value-attached long flag (--checkout=... ), consumes nothing extra
		case strings.HasPrefix(w, "-"):
		default:
			positionals = append(positionals, w)
		}
	}
	if hasB {
		return worktreeAddAnalysis{}
	}
	// Detach forms per #4995:
	// - explicit --detach: deliberate, never flagged.
	// - one positional (path only): git checks out the current branch - safe.
	// - two+ positionals: path + commit-ish. A local branch name attaches;
	//   SHAs and remote-tracking refs detach. We cannot know every local
	//   branch from the command string alone, so treat commit-ish targets
	//   that are clearly NOT local-branch-shaped as prone, and let the
	//   probe (post-run verification) settle ambiguous bare names.
	_ = explicitDetach
	path := ""
	if len(positionals) > 0 {
		path = positionals[0]
	}
	if len(positionals) < 2 {
		return worktreeAddAnalysis{path: path}
	}
	target := positionals[len(positionals)-1]
	if isDetachingWorktreeTarget(target) {
		return worktreeAddAnalysis{prone: true, path: path}
	}
	return worktreeAddAnalysis{path: path}
}

// isDetachingWorktreeTarget reports whether a `git worktree add <path>
// <commit-ish>` target is expected to check out detached. Local branch names
// attach; everything git treats as "not a branch" detaches: raw SHAs,
// remote-tracking refs (origin/x), tag names, and ref expressions.
func isDetachingWorktreeTarget(target string) bool {
	// refs/heads/<branch> is the only ref form that attaches.
	if strings.HasPrefix(target, "refs/heads/") {
		return false
	}
	// refs/remotes/... (origin/main), refs/tags/..., HEAD~1, <sha>^0,
	// and plain 7-40 hex SHAs all resolve to commits, not local branches.
	if strings.HasPrefix(target, "refs/") {
		return true
	}
	if isHexCommitSHA(target) {
		return true
	}
	// Annotated/plain tag names and <rev>^<n> / <rev>~<n> expressions detach.
	if strings.ContainsAny(target, "~^") {
		return true
	}
	// Bare names are ambiguous (could be a local branch or a tag). Heuristic
	// split: remote-tracking-shaped names (contains '/') like origin/main
	// detach; a plain word could be a local branch and attaches - the
	// post-run probe covers residual ambiguity for those.
	if strings.Contains(target, "/") {
		return true
	}
	return false
}

// isHexCommitSHA reports whether s is a 7-40 char lowercase/uppercase hex
// object name (full or abbreviated commit SHA).
func isHexCommitSHA(s string) bool {
	if len(s) < 4 || len(s) > 40 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
			return false
		}
	}
	return true
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
