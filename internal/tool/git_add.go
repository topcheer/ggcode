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
		lf := strings.ToLower(f)
		for _, pattern := range sensitiveFilePatterns {
			if strings.HasSuffix(lf, pattern) || strings.Contains(lf, pattern) {
				flagged = append(flagged, f)
				break
			}
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

// Clone returns an independent copy of this tool for use by a different agent.
func (t GitAdd) Clone() Tool {
	return &GitAdd{WorkingDir: t.WorkingDir}
}
