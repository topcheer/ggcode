package agent

import "testing"

// #5744 unit coverage for the shell write-surface classifier. The reject
// list is the safety net: anything not provably read-only must fall back to
// the UNKNOWN banner, so false negatives here are acceptable but false
// positives are not.

func TestShellLineReadOnlyAccepts(t *testing.T) {
	readOnly := []string{
		"gh api repos/topcheer/ggcode",
		"gh api repos/topcheer/ggcode/pulls/5744",
		"gh api -X GET /repos/topcheer/ggcode/issues",
		"gh api graphql -f query='query { viewer { login } }'",
		"gh pr view 4934 --json state",
		"gh pr checks 4934",
		"gh issue list --state open",
		"gh run list --limit 5",
		"gh auth status",
		"grep -rn 'non-atomic' internal/",
		"rg -n 'S-16' internal/agent",
		"ls -la /tmp",
		"cat README.md | grep install",
		"git status --porcelain",
		"git log --oneline -5",
		"git log --grep='timed out' --oneline",
		"git diff HEAD~1",
		"git show bd0fb3ac6f --stat",
		"git rev-parse HEAD",
		"git blame src/main.go",
		"git branch -a",
		"git tag --list",
		"git config --get user.name",
		"git config user.name",
		"git config --list",
		"git stash list",
		"git worktree list",
		"git remote -v",
		"git symbolic-ref HEAD",
		"cd /tmp && ls",
		"FOO=1 git log --oneline",
		"timeout 30 gh api repos/topcheer/ggcode",
		"timeout --signal=INT 30s grep -rn x .",
		"env | grep PATH",
		"echo hello",
		"printf '%s\n' a b c",
		"ps aux | grep node",
		"curl -s http://localhost:3333/",
		"curl --request GET http://localhost:3333/healthz",
		"sed -n '1,5p' go.mod",
		"awk '{print $1}' list.txt",
		"find . -name '*.go' -not -path './vendor/*'",
		"stat -f %z README.md",
		"wc -l internal/agent/mutate_outcome.go",
		"true && ls | head -3",
		"diff <(sort a.txt) <(sort b.txt)",
		"cat <<'EOF'\nhello world\nEOF",
		"cat <<EOF\ngit push origin main\nEOF",
		"grep foo bar 2>/dev/null || true",
		"ls > /dev/null 2>&1",
		"/usr/bin/git log --oneline",
		"git log --grep='a; rm -rf /' --oneline", // quoted ; is one argument, not a separator
		"command -v git",
		"nice -n 5 grep -rn x .",
		"jq '.state' pr.json",
		"sleep 5",
	}
	for _, cmd := range readOnly {
		if !shellLineReadOnly(cmd, 0) {
			t.Errorf("expected read-only: %q", cmd)
		}
	}
}

func TestShellLineReadOnlyRejects(t *testing.T) {
	notReadOnly := []string{
		// the coordinator incident that motivated #5744
		"gh pr merge 4934 --merge",
		"gh api --method POST /repos/topcheer/ggcode/dispatches",
		"gh api graphql -f query='mutation { updateIssue(input:{}) }'",
		"gh pr edit 4934 --title x",
		"gh release download v1",
		"git push origin main",
		"git commit -m x",
		"git stash",
		"git checkout -b feat/x",
		"git branch new-branch",
		"git branch -d old",
		"git config user.name Someone",
		"git remote add origin https://example.com/repo",
		"git worktree add /tmp/wt",
		"git hash-object -w file",
		"git symbolic-ref --delete HEAD",
		"git archive --output=/tmp/x.tar HEAD",
		"echo hi > out.txt",
		"cat log >> all.log",
		"ps aux 2> err.txt",
		"echo x >&out.txt",
		"tee out.txt",
		"rm -rf /tmp/x",
		"mkdir build",
		"sed -i '' 's/a/b/' f",
		"sed --in-place s/a/b/ f",
		"find . -exec rm {} +",
		"find . -delete",
		"awk '{print > \"out\"}' f",
		"awk 'BEGIN { system(\"rm x\") }' f",
		"npm install",
		"curl -X POST -d '{}' http://api.example.com/",
		"curl -o page.html http://example.com/",
		"curl --upload-file f ftp://example.com/",
		"date -s '2020-01-01'",
		"sysctl -w kern.sysv.shmmax=1",
		"echo \"$(git push)\"",
		"echo backticks `git push`",
		"(cd /tmp && rm x)",
		"(git push)",
		"mystrangetool --read",
		"git commit -m x; rm -rf /",
		"git commit -m 'x; rm -rf /'",
		"cat <<'EOF'\nbody\nEOF\ngit push origin main",
		"ls > /dev/null.txt",
		"bash -c 'ls'",
		"sh -c 'git push'",
		"sudo tee /etc/hosts",
	}
	for _, cmd := range notReadOnly {
		if shellLineReadOnly(cmd, 0) {
			t.Errorf("expected NOT read-only: %q", cmd)
		}
	}
}
