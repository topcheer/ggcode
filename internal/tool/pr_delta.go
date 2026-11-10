package tool

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// PRDelta separates already-absorbed commits from the true delta of a PR
// branch relative to its base, using patch-id equivalence (git patch-id
// --stable). It is read-only and computed entirely with local git; it never
// contacts the network.
type PRDelta struct{ WorkingDir string }

func (t PRDelta) Name() string { return "pr_delta" }

func (t PRDelta) Description() string {
	return "Analyze which commits on a PR branch are the true delta versus already absorbed by the base branch, " +
		"using patch-id equivalence (git patch-id --stable). Read-only, local git only, no network. " +
		"Run before reviewing a PR whose branch may be stale or forked; separates already-absorbed commits " +
		"from the true delta (patch-id equivalence)."
}

func (t PRDelta) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"pr_branch": {
				"type": "string",
				"description": "Required. The PR branch to analyze (local branch name or remote-tracking ref such as origin/feature)."
			},
			"base": {
				"type": "string",
				"description": "Base branch to compare against. Defaults to the remote default branch resolved from origin/HEAD (not hardcoded)."
			},
			"repo_path": {
				"type": "string",
				"description": "Repository path (default: current directory)"
			},
			"description": {
				"type": "string",
				"description": "REQUIRED. Brief activity label shown in the UI, in the user's language."
			}
		},
		"required": [
			"pr_branch",
			"description"
		]
	}`)
}

func (t PRDelta) Execute(ctx context.Context, input json.RawMessage) (Result, error) {
	var args struct {
		PRBranch string `json:"pr_branch"`
		Base     string `json:"base"`
		RepoPath string `json:"repo_path"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("invalid input: %v", err)}, nil
	}
	if strings.TrimSpace(args.PRBranch) == "" {
		return Result{IsError: true, Content: "pr_branch is required"}, nil
	}
	prBranch := strings.TrimSpace(args.PRBranch)

	dir := resolveDir(args.RepoPath, t.WorkingDir)

	base := strings.TrimSpace(args.Base)
	if base == "" {
		resolved, err := defaultBaseBranch(ctx, dir)
		if err != nil {
			return Result{IsError: true, Content: fmt.Sprintf(
				"could not resolve default base branch from origin/HEAD: %v; run `git remote set-head origin --auto` once or pass base explicitly", err)}, nil
		}
		base = resolved
	}

	if err := verifyRef(ctx, dir, base); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("base ref %q not found: %v", base, err)}, nil
	}
	if err := verifyRef(ctx, dir, prBranch); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("pr_branch ref %q not found: %v", prBranch, err)}, nil
	}

	return t.analyze(ctx, dir, prBranch, base)
}

func (t PRDelta) analyze(ctx context.Context, dir, prBranch, base string) (Result, error) {
	mergeBase, err := runGitTrimmed(ctx, dir, "merge-base", base, prBranch)
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("git merge-base %s %s failed: %v", base, prBranch, err)}, nil
	}

	baseIDs, err := patchIDsForRange(ctx, dir, mergeBase+".."+base)
	if err != nil {
		return Result{IsError: true, Content: err.Error()}, nil
	}
	// baseIDs maps sha -> patch-id; comparison needs the patch-id set.
	basePIDs := make(map[string]struct{}, len(baseIDs))
	for _, pid := range baseIDs {
		basePIDs[pid] = struct{}{}
	}

	prRange := base + ".." + prBranch
	prIDs, err := patchIDsForRange(ctx, dir, prRange)
	if err != nil {
		return Result{IsError: true, Content: err.Error()}, nil
	}

	commits, err := commitsWithSubjects(ctx, dir, prRange)
	if err != nil {
		return Result{IsError: true, Content: err.Error()}, nil
	}

	var lines []string
	deltaFiles := map[string]struct{}{}
	equivalent, delta := 0, 0
	var deltaShas []string
	for _, c := range commits {
		id := prIDs[c.sha]
		if _, absorbed := basePIDs[id]; id != "" && absorbed {
			equivalent++
			lines = append(lines, fmt.Sprintf("- [equivalent] %s %s", shortSha(c.sha), c.subject))
			continue
		}
		delta++
		deltaShas = append(deltaShas, c.sha)
		lines = append(lines, fmt.Sprintf("- [delta] %s %s", shortSha(c.sha), c.subject))
	}

	for _, sha := range deltaShas {
		out, err := runGitTrimmed(ctx, dir, "diff-tree", "--no-commit-id", "--name-only", "-r", sha)
		if err != nil {
			return Result{IsError: true, Content: fmt.Sprintf("git diff-tree %s failed: %v", shortSha(sha), err)}, nil
		}
		for _, f := range strings.Split(out, "\n") {
			if f = strings.TrimSpace(f); f != "" {
				deltaFiles[f] = struct{}{}
			}
		}
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Commits on %s not in %s (merge-base %s):\n\n", prBranch, base, shortSha(mergeBase))
	if len(commits) == 0 {
		fmt.Fprintf(&b, "- (none)\n\n")
		fmt.Fprintf(&b, "No commits on %s are outside %s; the branch is behind or up to date with its base.\n", prBranch, base)
		return Result{Content: strings.TrimRight(b.String(), "\n")}, nil
	}
	for _, l := range lines {
		fmt.Fprintln(&b, l)
	}
	fmt.Fprintln(&b)
	fmt.Fprintf(&b, "%d/%d commits already absorbed by %s; true delta = %d commits, files affected by delta only: %d\n",
		equivalent, len(commits), base, delta, len(deltaFiles))
	if delta == 0 {
		fmt.Fprintln(&b, "PR is fully patch-equivalent to base - nothing to review/merge")
	}

	return Result{Content: strings.TrimRight(b.String(), "\n")}, nil
}

func (t PRDelta) Clone() Tool {
	return &PRDelta{WorkingDir: t.WorkingDir}
}

// defaultBaseBranch resolves the remote default branch from origin/HEAD
// without hardcoding a branch name.
func defaultBaseBranch(ctx context.Context, dir string) (string, error) {
	out, err := runGitTrimmed(ctx, dir, "symbolic-ref", "--short", "refs/remotes/origin/HEAD")
	if err != nil {
		return "", err
	}
	if out == "" {
		return "", fmt.Errorf("origin/HEAD is not set")
	}
	return out, nil
}

func verifyRef(ctx context.Context, dir, ref string) error {
	_, err := runGitTrimmed(ctx, dir, "rev-parse", "--verify", "--quiet", ref+"^{commit}")
	if err != nil {
		return err
	}
	return nil
}

func runGitTrimmed(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := gitCommand(ctx, args...)
	cmd.Dir = dir
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(buf.String())
		if msg == "" {
			return "", err
		}
		return "", fmt.Errorf("%s: %v", strings.Join(args, " "), err)
	}
	return strings.TrimSpace(buf.String()), nil
}

type commitInfo struct {
	sha     string
	subject string
}

func commitsWithSubjects(ctx context.Context, dir, revRange string) ([]commitInfo, error) {
	out, err := runGitTrimmed(ctx, dir, "log", "--no-merges", "--format=%H%x09%s", revRange)
	if err != nil {
		return nil, fmt.Errorf("git log %s failed: %v", revRange, err)
	}
	var commits []commitInfo
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 2)
		c := commitInfo{sha: parts[0]}
		if len(parts) == 2 {
			c.subject = parts[1]
		}
		commits = append(commits, c)
	}
	return commits, nil
}

// patchIDsForRange streams `git rev-list -p --no-merges <range>` into
// `git patch-id --stable` and returns commit sha -> patch-id. Merge commits
// are excluded; commits with an empty diff produce no patch-id and are
// simply absent from the map.
func patchIDsForRange(ctx context.Context, dir, revRange string) (map[string]string, error) {
	revList := gitCommand(ctx, "log", "--no-merges", "-p", revRange)
	revList.Dir = dir
	patchID := gitCommand(ctx, "patch-id", "--stable")
	patchID.Dir = dir

	pipeR, pipeW := io.Pipe()
	revList.Stdout = pipeW
	patchID.Stdin = pipeR
	var out bytes.Buffer
	patchID.Stdout = &out
	var errBuf bytes.Buffer
	revList.Stderr = &errBuf
	patchID.Stderr = &errBuf

	if err := revList.Start(); err != nil {
		return nil, fmt.Errorf("git rev-list %s failed to start: %v", revRange, err)
	}
	if err := patchID.Start(); err != nil {
		_ = revList.Process.Kill()
		return nil, fmt.Errorf("git patch-id failed to start: %v", err)
	}
	revListErr := revList.Wait()
	pipeW.Close()
	patchIDErr := patchID.Wait()
	pipeR.Close()

	if revListErr != nil {
		return nil, fmt.Errorf("git rev-list %s failed: %v", revRange, revListErr)
	}
	if patchIDErr != nil {
		return nil, fmt.Errorf("git patch-id failed: %v", patchIDErr)
	}

	ids := map[string]string{}
	sc := bufio.NewScanner(&out)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) == 2 {
			ids[fields[1]] = fields[0]
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("reading patch-id output: %v", err)
	}
	return ids, nil
}

func shortSha(sha string) string {
	if len(sha) > 8 {
		return sha[:8]
	}
	return sha
}
