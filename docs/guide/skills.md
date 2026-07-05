# Skills

## What Skills Are

Skills are reusable, composable workflows that ggcode can invoke. Each skill is a markdown file containing step-by-step instructions that guide the agent through a specific task pattern.

## Availability

Skills are available through the `skill` tool. When a listed skill clearly matches the user's task, the agent invokes it before continuing. Skills can also be loaded explicitly via the `skill` tool or the `/skills` slash command.

## Viewing Skills

Run `/skills` in the TUI to list all available skills:

```
/skills
```

## Loading a Skill

ggcode auto-loads a skill when the context matches its conditions. You can also explicitly load a skill via the `skill` tool:

```
skill(skill="debug", args="investigate the failing test in auth_test.go")
```

## Built-in Skills

| Skill | Description |
|-------|-------------|
| `browser-automation` | Drive a browser via built-in CDP browser tool |
| `debug` | Systematic debugging workflow |
| `documentation-update` | Keep docs in sync with code changes |
| `verify` | Run tests and validate changes |
| `simplify` | Refactor and reduce complexity |

## Skill Files

Skills are markdown files with workflow instructions:

```markdown
---
name: debug
trigger: "error message | test failure | stack trace"
---

## Debug Workflow

1. Read the error message carefully
2. Identify the failing file and line
3. Read the surrounding code
4. Form a hypothesis
5. Test the fix
6. Verify with the original failing case
```

## Skill Locations

| Location | Scope |
|----------|-------|
| `.ggcode/skills/` | Project-specific skills |
| `~/.ggcode/skills/` | Global skills (all projects) |

Project-specific skills override global skills with the same name.

## Skill Chaining

Skills can chain — one skill can invoke another within its workflow:

```markdown
## Workflow
1. Run the `verify` skill to confirm tests pass
2. If tests fail, invoke the `debug` skill
3. After fixing, run `documentation-update` to reflect changes
```

This makes skills composable building blocks rather than isolated scripts.
