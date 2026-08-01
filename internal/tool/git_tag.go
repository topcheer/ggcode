package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// GitTag implements the git_tag tool for creating, listing, and deleting tags.
// Tags are commonly used for release management.
type GitTag struct{ WorkingDir string }

func (t GitTag) Name() string { return "git_tag" }

func (t GitTag) Description() string {
	return "Manage git tags for release versioning. Actions: 'list' (show all tags, default), 'create' (create annotated tag), 'delete' (remove tag). Use git_log to find the target commit before creating a tag."
}

func (t GitTag) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"path": {
				"type": "string",
				"description": "Repository path (default: current directory)"
			},
			"action": {
				"type": "string",
				"description": "Tag action: 'list' (default), 'create', 'delete'",
				"enum": ["list", "create", "delete"]
			},
			"name": {
				"type": "string",
				"description": "Tag name (e.g., 'v1.0.0'). Required for create and delete."
			},
			"message": {
				"type": "string",
				"description": "Annotation message for the tag (create only, recommended for releases)"
			},
			"commit": {
				"type": "string",
				"description": "Commit hash to tag (create only, default: HEAD)"
			},
			"description": {
				"type": "string",
				"description": "REQUIRED. Brief activity label shown in the UI. Write in the user's language."
			}
		},
		"required": ["description"]
	}`)
}

func (t GitTag) Execute(ctx context.Context, input json.RawMessage) (Result, error) {
	var args struct {
		Path    string `json:"path"`
		Action  string `json:"action"`
		Name    string `json:"name"`
		Message string `json:"message"`
		Commit  string `json:"commit"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return Result{IsError: true, Content: fmt.Sprintf("invalid input: %v", err)}, nil
	}

	action := args.Action
	if action == "" {
		action = "list"
	}

	dir := resolveDir(args.Path, t.WorkingDir)

	switch action {
	case "list":
		cmd := gitCommand(ctx, "tag", "--list", "-n")
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			return Result{IsError: true, Content: fmt.Sprintf("git tag list failed: %v\n%s", err, out)}, nil
		}
		trimmed := strings.TrimSpace(string(out))
		if trimmed == "" {
			return Result{Content: "No tags found."}, nil
		}
		// Cap output for repos with many tags
		const maxTagLines = 50
		lines := strings.Split(trimmed, "\n")
		if len(lines) > maxTagLines {
			shown := strings.Join(lines[:maxTagLines], "\n")
			return Result{Content: shown +
				fmt.Sprintf("\n\n... [%d more tags hidden]", len(lines)-maxTagLines)}, nil
		}
		return Result{Content: trimmed}, nil

	case "create":
		if args.Name == "" {
			return Result{IsError: true, Content: "name is required for create action"}, nil
		}
		gitArgs := []string{"tag"}
		if args.Message != "" {
			gitArgs = append(gitArgs, "-a", args.Name, "-m", args.Message)
		} else {
			// Lightweight tag (no annotation)
			gitArgs = append(gitArgs, args.Name)
		}
		if args.Commit != "" {
			gitArgs = append(gitArgs, args.Commit)
		}

		cmd := gitCommand(ctx, gitArgs...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			return Result{IsError: true, Content: fmt.Sprintf("git tag create failed: %v\n%s", err, out)}, nil
		}
		trimmed := strings.TrimSpace(string(out))
		target := args.Commit
		if target == "" {
			target = "HEAD"
		}
		msg := fmt.Sprintf("Created tag %s at %s.", args.Name, target)
		if args.Message == "" {
			msg += " (lightweight tag — use 'message' for annotated release tags)"
		}
		if trimmed != "" {
			msg += "\n" + trimmed
		}
		return Result{Content: msg}, nil

	case "delete":
		if args.Name == "" {
			return Result{IsError: true, Content: "name is required for delete action"}, nil
		}
		cmd := gitCommand(ctx, "tag", "-d", args.Name)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			return Result{IsError: true, Content: fmt.Sprintf("git tag delete failed: %v\n%s", err, out)}, nil
		}
		trimmed := strings.TrimSpace(string(out))
		if trimmed == "" {
			return Result{Content: fmt.Sprintf("Deleted tag %s.", args.Name)}, nil
		}
		return Result{Content: trimmed}, nil

	default:
		return Result{IsError: true, Content: fmt.Sprintf("unsupported action %q: must be list, create, or delete", action)}, nil
	}
}

// Clone returns an independent copy of this tool for use by a different agent.
func (t GitTag) Clone() Tool {
	return &GitTag{WorkingDir: t.WorkingDir}
}
