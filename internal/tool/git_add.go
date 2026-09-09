package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/topcheer/ggcode/internal/vcs"
)

// GitAdd implements the git_add tool.
type GitAdd struct {
	WorkingDir string
}

func (t GitAdd) Name() string { return "git_add" }

func (t GitAdd) Description() string {
	return "Add file contents to the index (staging area). Stage only the intended files; avoid git_add files=[\".\"] unless the user explicitly wants all current changes staged."
}

func (t GitAdd) Parameters() json.RawMessage {
	return json.RawMessage(`{
	"type": "object",
	"properties": {
		"path": {
			"type": "string",
			"description": "Repository path (default: current directory)"
		},
		"files": {
			"type": "array",
			"items": {
				"type": "string"
			},
			"description": "File paths to stage. Prefer explicit paths. Use [\".\"] only when the user explicitly wants all current changes staged."
		},
		"description": {
			"type": "string",
			"description": "REQUIRED. Brief activity label shown in the UI. Write in the user's language (e.g. 'Searching for TODO patterns', '检查构建配置'). You MUST always provide this field."
		}
	},
	"required": [
		"files",
		"description"
	]
}`)
}

func (t GitAdd) Execute(ctx context.Context, input json.RawMessage) (Result, error) {
	var args struct {
		Path  string   `json:"path"`
		Files []string `json:"files"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("invalid input: %v", err)}, nil
	}

	if len(args.Files) == 0 {
		return Result{IsError: true, Content: "files is required"}, nil
	}

	dir := resolveDir(args.Path, t.WorkingDir)

	// Sensitive file detection: warn about files that commonly contain
	// secrets and should not be committed. Non-blocking advisory.
	secretWarning := checkSensitiveFiles(args.Files)

	// Non-git VCS path.
	if v := vcs.Detect(dir); v != nil && v.Name() != "git" {
		out, err := v.Add(ctx, dir, args.Files)
		if err != nil {
			return Result{IsError: true, Content: fmt.Sprintf("%s add failed: %v", v.Name(), err)}, nil
		}
		trimmed := strings.TrimSpace(out)
		// #835: the sensitive-file advisory must fire on hg/svn too.
		if secretWarning != "" {
			trimmed = strings.TrimSpace(trimmed + "\n\n" + secretWarning)
		}
		if trimmed == "" {
			return Result{Content: fmt.Sprintf("Staged %d file(s).", len(args.Files))}, nil
		}
		return Result{Content: trimmed}, nil
	}

	gitArgs := []string{"add", "--"}
	gitArgs = append(gitArgs, args.Files...)

	cmd := gitCommand(ctx, gitArgs...)
	cmd.Dir = dir

	out, err := cmd.CombinedOutput()
	if err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("git add failed: %v\n%s", err, out)}, nil
	}

	// #1687 case 1: directory/dot/glob arguments (".", "config/", "*")
	// carry no literal file name for checkSensitiveFiles to match - the
	// advisory went silent exactly where mass-staging is most likely to
	// sweep in secrets. Scan what actually landed in the index instead.
	if secretWarning == "" {
		dirLike := false
		for _, f := range args.Files {
			tf := strings.TrimSpace(f)
			if tf == "." || tf == ".." || strings.HasSuffix(tf, "/") || strings.ContainsAny(tf, "*?[") {
				dirLike = true
				break
			}
		}
		if dirLike {
			if staged := stagedSensitiveFiles(ctx, dir); len(staged) > 0 {
				secretWarning = checkSensitiveFiles(staged)
			}
		}
	}

	trimmed := strings.TrimSpace(string(out))
	if trimmed == "" {
		msg := fmt.Sprintf("Staged %d file(s).", len(args.Files))
		if secretWarning != "" {
			msg += "\n\n" + secretWarning
		}
		return Result{Content: msg}, nil
	}

	if secretWarning != "" {
		trimmed += "\n\n" + secretWarning
	}
	return Result{Content: trimmed}, nil
}

// sensitiveFilePatterns are file names/suffixes that commonly contain secrets.
var sensitiveFilePatterns = []string{
	".env", ".env.local", ".env.production", ".env.staging", ".env.development",
	".aws/credentials", ".npmrc", ".pypirc",
	"id_rsa", "id_dsa", "id_ecdsa", "id_ed25519",
	".pem", ".key", ".pfx", ".p12",
	"credentials.json", "service-account.json",
	".htpasswd", ".netrc",
}

// checkSensitiveFiles returns a warning if any staged file matches known
// sensitive file patterns. Non-blocking advisory.
func checkSensitiveFiles(files []string) string {
	var flagged []string
	for _, f := range files {
		if matchSensitivePath(f) {
			flagged = append(flagged, f)

		}
	}
	if len(flagged) == 0 {
		return ""
	}
	return fmt.Sprintf(
		"Warning: the following staged file(s) may contain secrets: %s.\n"+
			"Verify these do NOT contain API keys, passwords, or private keys before committing.\n"+
			"If they do, unstage them with 'git reset HEAD <file>' and add them to .gitignore.",
		strings.Join(flagged, ", "),
	)
}

// matchSensitivePath reports whether a single path matches any sensitive
// pattern. #1687 case 3: the #835 anchoring used SUBSTRING forms - "/.env"
// hit foo/.envrc and ".env/" hit dir.env/x. Exact matching now compares
// path SEGMENTS: a pattern's own "/" splits must align with the path's
// segment boundaries (so ".aws/credentials" matches exactly that
// sub-path), and a slash-less pattern matches only a whole basename.
func matchSensitivePath(path string) bool {
	lf := strings.ToLower(strings.TrimSpace(path))
	if lf == "" {
		return false
	}
	segs := strings.Split(lf, "/")
	base := segs[len(segs)-1]
	for _, pattern := range sensitiveFilePatterns {
		if !strings.Contains(pattern, "/") {
			// Slash-less patterns are basename SUFFIXES: .pem matches
			// server.pem and .env matches prod.env. Segment-splitting
			// first kills the #835 regressions: foo/.envrc does not
			// end in .env, and dir.env/x has basename x.
			if strings.HasSuffix(base, pattern) {
				return true
			}
			continue
		}
		// Slash patterns (.aws/credentials) match an exact segment run
		// ending at the last segment.
		p := strings.Split(pattern, "/")
		if len(segs) >= len(p) {
			match := true
			for j, ps := range p {
				if segs[len(segs)-len(p)+j] != ps {
					match = false
					break
				}
			}
			if match {
				return true
			}
		}
	}
	return false
}

// stagedSensitiveFiles lists sensitive files currently staged (index vs
// HEAD). #1687 case 1: `files: ["."]` (explicitly allowed by the schema)
// or a directory argument carries no literal name for the literal-argument
// check to match - the advisory went silent exactly where mass-staging is
// most likely to sweep in .env files. Called on the git path after add.
func stagedSensitiveFiles(ctx context.Context, dir string) []string {
	cmd := gitCommand(ctx, "diff", "--cached", "--name-only")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil
	}
	var flagged []string
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line != "" && matchSensitivePath(line) {
			flagged = append(flagged, line)
		}
	}
	return flagged
}

// Clone returns an independent copy of this tool for use by a different agent.
func (t GitAdd) Clone() Tool {
	return &GitAdd{WorkingDir: t.WorkingDir}
}
