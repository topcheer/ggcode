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
| `scope` | No | `project` (default, saved to `.ggcode/skills/`) or `global` (saved to `~/.ggcode/skills/`) |
| `context` | No | `inline` (default) or `fork` execution mode |

## Skill File Format

Created skills use the standard `SKILL.md` format with YAML frontmatter:

```markdown
---
name: deploy-to-vercel
description: Deploy the project to Vercel
when_to_use: When the user wants to deploy to Vercel
---

## Steps
1. Run the build...
2. Deploy with vercel CLI...
```

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

## Related

- [Skill Loading](skills.md) -- Using the `skill` tool to load existing skills
- [Skill Search](skills.md#search) -- Discovering skills by keyword
