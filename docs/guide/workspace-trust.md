# Workspace Trust

r89: ggcode treats an untrusted checkout as a prompt-injection / code-execution vector. A cloned repository can ship project-scoped assets that ggcode would otherwise auto-load at startup:

- `.ggcode/skills/` and `.ggcode/commands/` (project skills and slash commands)
- `.mcp.json` (project MCP server definitions)
- `GGCODE.md` / `AGENTS.md` / `CLAUDE.md` / `COPILOT.md` (project memory injected into the system prompt)

## Behavior

- **Fail-closed**: a folder with no recorded decision is *restricted* — the assets above are not auto-loaded. Folders without any project-scoped assets are unaffected and never prompt.
- **Interactive (TUI) start**: on first open of a project that ships such assets, ggcode asks `Trust the files in this folder and enable them? [y/N]`. Answering `y` persists trust; anything else keeps restricted mode.
- **Headless (`ggcode -p ...`)**: no prompt. Restricted by default, with a stderr notice pointing at `ggcode trust`.
- **Nearest-ancestor-wins**: trusting `/work` covers `/work/repo`; an explicit revocation on a child shadows a trusted parent.
- **Runtime toggle**: `/trust` grants trust and immediately enables project skills/commands (`.mcp.json` servers connect on next start); `/trust --revoke` revokes.

## CLI

```bash
ggcode trust              # trust current folder (explicit, scriptable)
ggcode trust /path/to/ws  # trust a specific folder
ggcode trust --list       # list recorded decisions
ggcode trust --revoke     # revoke (explicit restriction)
ggcode trust --forget     # remove decision; inherit nearest ancestor again
```

Decisions persist in `~/.ggcode/trust.json`, keyed by canonical absolute path, with host and timestamp metadata.
