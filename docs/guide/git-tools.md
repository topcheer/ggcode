# Git Workflow Tools

ggcode provides a comprehensive set of Git tools that allow the AI agent to perform
common version control operations directly, without falling back to raw shell commands.

## Available Git Tools

### Read-Only Tools

| Tool | Description |
|------|-------------|
| `git_status` | Show working tree status (porcelain output with file statuses) |
| `git_diff` | Show unstaged or staged changes (--cached) |
| `git_log` | Show commit history (oneline format) |
| `git_show` | Show details for a commit, branch, or tag (supports diffstat) |
| `git_blame` | Show last modification info per line |
| `git_branch_list` | List local or remote-tracking branches |
| `git_remote` | List configured remotes and their URLs |
| `git_stash_list` | List stash entries |

### Write Tools (require approval)

| Tool | Description |
|------|-------------|
| `git_add` | Stage files for commit |
| `git_commit` | Commit staged changes with message quality checks and advisory warnings |
| `git_stash` | Push, pop, apply, or drop stash entries |
| `git_checkout` | Switch branches or create new branches |
| `git_revert` | Revert a commit by creating a new undo commit (safe for shared branches) |
| `git_reset` | Reset staging area and/or working tree (soft, mixed, hard modes) |
| `git_tag` | Create, list, or delete tags for release versioning |

## git_revert

Creates a new commit that undoes the changes from a specified commit. This is
history-preserving and safe for shared branches.

```
git_revert(commit="<hash>", no_commit=false)
```

- **commit** (required): Commit hash to revert. Use `git_log` to find it.
- **no_commit** (optional): If true, stages revert changes without committing.
  Useful for combining multiple reverts into one commit.

**When to use:** Undoing a commit that has already been pushed to a shared branch.

**When NOT to use:** For local uncommitted changes, use `git_reset` or `git_stash` instead.

## git_reset

Resets the staging area and/or working tree to a specified state.

```
git_reset(mode="mixed", target="HEAD", files=["file1.go"])
```

- **mode**: `soft` (unstage only, keep working changes), `mixed` (unstage + keep file changes, default), `hard` (discard ALL changes permanently)
- **target**: `HEAD` (default), `HEAD~1` (parent commit), or a specific commit hash
- **files**: Optional specific files to unstage (mode is ignored; always mixed)

**Common use cases:**
- **Unstage accidentally staged files**: `git_reset(mode="mixed")` or `git_reset(files=["file.go"])`
- **Undo last commit (keep changes)**: `git_reset(mode="soft", target="HEAD~1")`
- **Discard all uncommitted work**: `git_reset(mode="hard")` — use with caution

## git_tag

Manages git tags for release versioning.

```
git_tag(action="create", name="v1.0.0", message="First release", commit="HEAD")
```

- **action**: `list` (default), `create`, `delete`
- **name** (required for create/delete): Tag name (e.g., `v1.0.0`)
- **message** (create only): Annotation message. Annotated tags are recommended for releases.
- **commit** (create only): Commit to tag (default: HEAD)

**Best practices:**
- Always use annotated tags (`message` field) for release tags
- Use `git_log` to identify the target commit before tagging
- List existing tags before creating to avoid duplicates
