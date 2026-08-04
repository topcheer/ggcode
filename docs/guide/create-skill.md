# Skill Creation (`create_skill`)

The `create_skill` tool lets the agent create **reusable skill files** that persist across sessions. When the agent learns a useful workflow during a session, it can save it as a skill for future reuse.

## How It Works

1. The agent identifies a repeatable workflow worth saving
2. Calls `create_skill` with a name, description, and the full prompt/instructions
3. The skill is written as a `SKILL.md` file with YAML frontmatter
4. The command manager reloads automatically -- the skill is immediately available
5. Future sessions can invoke the skill via `skill: "<name>"`

## Parameters

| Parameter | Required | Description |
|-----------|----------|-------------|
| `name` | Yes | Skill name (lowercase, hyphens/digits only, e.g. `deploy-to-vercel`) |
| `description` | Yes | Short description of what the skill does |
| `content` | Yes | Full skill body -- the prompt/instructions executed when invoked |
| `when_to_use` | No | When this skill should be used (shown in skill search) |
| `allowed_tools` | No | Tools this skill can use in fork mode |
| `requires_tools` | No | External CLI tools that must be on PATH (e.g. `["docker", "kubectl"]`). Validated at load time |
| `dependencies` | No | Prerequisite skill names that should be loaded first |
| `scope` | No | `project` (default, saved to `.ggcode/skills/`) or `global` (saved to `~/.ggcode/skills/`) |
| `context` | No | `inline` (default) or `fork` execution mode |

## Skill File Format

Created skills use the standard `SKILL.md` format with YAML frontmatter:

```markdown
---
name: deploy-to-vercel
description: Deploy the project to Vercel
when_to_use: When the user wants to deploy to Vercel
requires-tools:
  - vercel
dependencies:
  - build-app
---

## Steps
1. Run the build...
2. Deploy with vercel CLI...
```

## Dependency Declaration

Skills can declare two types of dependencies in frontmatter:

### `requires-tools`

External CLI tools that must be installed and on `PATH` for the skill to work.

```yaml
requires-tools:
  - docker
  - kubectl
  - terraform
```

When the skill is invoked, ggcode validates each tool exists on `PATH` via `exec.LookPath`. If any are missing, execution is blocked with a clear error message listing the missing tools.

### `dependencies`

Prerequisite skill names that should be loaded before the current skill.

```yaml
dependencies:
  - check-env
  - build-app
```

When the skill is loaded, the agent receives an advisory hint listing prerequisite skills. Available dependencies are suggested for loading; missing or disabled ones are noted but do not block execution.

## Scope

- **Project skills** (`.ggcode/skills/`): Shared with the team via version control
- **Global skills** (`~/.ggcode/skills/`): Available across all projects on the machine

## Competitor Comparison

| Feature | ggcode | Claude Code | Cursor | Cline |
|---------|--------|-------------|--------|-------|
| Agent creates skills at runtime | Yes | No | No | No |
| Skills persist across sessions | Yes | Yes (manual) | Yes (.cursorrules) | No |
| Immediate availability | Yes (auto-reload) | Manual restart | Manual | N/A |
| Skill search/discovery | Yes | Yes | No | No |
| Skill dependency declaration | Yes | No | No | No |
| External tool validation | Yes | No | No | No |

## Related

- [Skill Loading](skills.md) -- Using the `skill` tool to load existing skills
- [Skill Search](skills.md#search) -- Discovering skills by keyword
