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

## Fuzzy Name Matching

When a skill name is slightly off -- different casing, hyphens vs underscores,
or a partial name -- the `skill` tool suggests the closest matches instead of
failing with a bare "not found" error. For example:

```
skill(skill="browser_automation")  → "Did you mean: browser-automation?"
skill(skill="verify")               → "Did you mean: verify, verify-lint, verify-changes?"
```

This saves the agent an iteration by pointing directly to the correct name.

## Skill Versioning

Skills can declare a semantic version in frontmatter. This enables version-aware
dependency management and helps track which version of a workflow is active.

```markdown
---
name: deploy-to-vercel
version: "1.2.0"
description: Deploy the project to Vercel
---
```

The version is displayed when the skill is loaded (`Skill "deploy-to-vercel" (v1.2.0) loaded.`)
and in search results.

### Version-Constrained Dependencies

Dependencies can specify version constraints using the `@` syntax:

```yaml
dependencies:
  - check-env@>=1.0.0      # minimum version 1.0.0
  - build-app@2.0.0         # exact version 2.0.0
  - deploy-helper@<3.0.0    # any version before 3.0.0
```

Supported operators: `>=`, `>`, `<=`, `<`, `==` (or `=`), and bare version (exact match).

When a loaded dependency's version does not satisfy the constraint, the agent
receives a warning like:

```
Version mismatch: check-env (requires >=1.0.0, found 0.9.0).
```

Version mismatches are advisory -- they do not block execution. This lets the
agent proceed while alerting it to potential incompatibilities.

## Skill Chaining

Skills can chain -- one skill can invoke another within its workflow:

```markdown
## Workflow
1. Run the `verify` skill to confirm tests pass
2. If tests fail, invoke the `debug` skill
3. After fixing, run `documentation-update` to reflect changes
```

This makes skills composable building blocks rather than isolated scripts.
