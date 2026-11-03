package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/topcheer/ggcode/internal/vcs"
)

func gitAddArgsDirLike(dir string, files []string) bool {
	for _, f := range files {
		tf := strings.TrimSpace(f)
		if tf == "." || tf == ".." || strings.HasSuffix(tf, "/") || strings.ContainsAny(tf, "*?[") {
			return true
		}
		if fi, err := os.Stat(f); err == nil && fi.IsDir() {
			return true
		}
		if dir != "" {
			if fi, err := os.Stat(filepath.Join(dir, f)); err == nil && fi.IsDir() {
				return true
			}
		}
	}
	return false
}

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
			"description": "REQUIRED. Brief activity label shown in the UI, in the user's language."
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

	secretWarning := checkSensitiveFiles(args.Files)

	if v := vcs.Detect(dir); v != nil && v.Name() != "git" {
		out, err := v.Add(ctx, dir, args.Files)
		if err != nil {
			return Result{IsError: true, Content: fmt.Sprintf("%s add failed: %v", v.Name(), err)}, nil
		}
		trimmed := strings.TrimSpace(out)

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

	if secretWarning == "" {
		if gitAddArgsDirLike(dir, args.Files) {
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

var sensitiveFilePatterns = []string{
	".env", ".env.local", ".env.production", ".env.staging", ".env.development",
	".aws/credentials", ".npmrc", ".pypirc",
	"id_rsa", "id_dsa", "id_ecdsa", "id_ed25519",
	".pem", ".key", ".pfx", ".p12",
	"credentials.json", "service-account.json",
	".htpasswd", ".netrc",
}

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

func matchSensitivePath(path string) bool {
	lf := strings.ToLower(strings.TrimSpace(path))
	if lf == "" {
		return false
	}
	segs := strings.Split(lf, "/")
	base := segs[len(segs)-1]
	for _, pattern := range sensitiveFilePatterns {
		if !strings.Contains(pattern, "/") {

			if strings.HasSuffix(base, pattern) {
				return true
			}
			continue
		}

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

func (t GitAdd) Clone() Tool {
	return &GitAdd{WorkingDir: t.WorkingDir}
}
