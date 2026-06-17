# Shell Completion

ggcode provides built-in shell completion powered by Cobra. No external tools are needed.

## Install

### bash

Add to `~/.bashrc`:

```bash
source <(ggcode completion bash)
```

For persistent completion file installation:

```bash
ggcode completion bash > /etc/bash_completion.d/ggcode
```

### zsh

Add to `~/.zshrc`:

```zsh
source <(ggcode completion zsh)
```

If you get "compinit" errors, ensure the completion directory is in `fpath`:

```zsh
ggcode completion zsh > "${fpath[1]}/_ggcode"
```

### fish

Write to the fish completions directory:

```fish
ggcode completion fish > ~/.config/fish/completions/ggcode.fish
```

### PowerShell

Add to your PowerShell profile:

```powershell
ggcode completion powershell | Out-String | Invoke-Expression
```

## What Completion Includes

| Category | Examples |
|----------|----------|
| Subcommands | `config`, `completion`, `resume`, `mcp` |
| Flags | `--help`, `--model`, `--resume`, `--vendor` |
| Session IDs | Previous session IDs for `--resume` |
| MCP server names | Configured server names for tab completion |

## Verification

After installing, open a new shell and test:

```bash
ggcode <TAB>
ggcode config <TAB>
ggcode --resume <TAB>
```
