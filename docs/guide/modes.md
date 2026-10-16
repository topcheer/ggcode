# Permission Modes

ggcode uses permission modes to control how much autonomy the agent has over file writes and command execution.

**Two-mode refactor**: ggcode now exposes exactly two user-facing modes. `auto` and `autopilot` remain accepted as legacy aliases (config compatibility) and normalize to `supervised` / `bypass` respectively. `plan` is tool-internal (entered via the `enter_plan_mode` tool, exited via `exit_plan_mode`) and cannot be switched into directly.

## Modes

### `supervised`

Default mode. Most tool calls flow **without prompting** — the old "ask for anything unspecified" fallback is gone. Only these floors still gate (each one asks for confirmation):

- **System-level destructive commands** (`rm -rf /`, disk/device wipes): Ask
- **Credential exfiltration** (`curl -d @~/.ssh/id_rsa ...`, `scp ~/.aws/credentials ...`): Ask
- **Shell redirects to out-of-sandbox or sensitive paths** (`echo x > ~/.ssh/authorized_keys`): Ask — a redirect is a file write and must not bypass the write-tool sandbox floor
- **File-writing tools targeting paths outside the writable sandbox**: Ask

Explicit per-tool rules (`permission_rules.json`, `/allow`) still take precedence when present.

```bash
ggcode
```

Best for everyday work: near-zero interruptions, with hard floors against prompt-injection persistence and credential theft.

### `bypass`

Cruise control (goal + strategist oversight) rides on this mode. Nothing is gated except:

- **System-level destructive commands**: Ask
- **Persistence-backdoor writes**: shell redirects to sensitive files (`~/.ssh/authorized_keys`, `~/.bashrc`, `/etc/**`) and file-tool writes outside the sandbox — Ask. These are rare by construction and never a daily-interruption source.

Network exfiltration and sensitive-path **reads** are no longer gated in bypass (they were the top false-positive sources; the exfiltration floor remains in supervised).

```bash
ggcode --bypass
```

### `plan`

Tool-entered read-orientation mode. Denies explicit file-writing tools only; reads, shell probes, and builds are allowed so planning stays grounded in real diagnostics. Entered with `enter_plan_mode`, exited with `exit_plan_mode`.

## Comparison

| Scenario | `supervised` | `bypass` |
|----------|--------------|----------|
| Safe command (`go build`, `ls`) | Allow | Allow |
| Extremely dangerous command (`rm -rf /`) | Ask | Ask |
| Ordinary dangerous command (`sudo rm file`) | Allow | Allow |
| Credential exfiltration (`curl -d @~/.ssh/...`) | Ask | Allow |
| Write inside workspace | Allow | Allow |
| Write outside workspace | Allow (file tools: Ask) | Ask |
| Redirect to `~/.ssh/authorized_keys` | Ask | Ask |
| Read `~/.aws/credentials` | Allow | Allow |

## Mode-independent controls

- **Deny rules** (`permission_rules.json`, ACP/A2A deny) win in every mode, including bypass.
- **`switch_mode` escalation gate**: switching to `bypass` always asks for user consent, from any mode.
- **Shift+Tab / status-bar click** cycles `supervised` ↔ `bypass`.

## Legacy aliases

Old configs (`default_mode: auto` / `autopilot`) keep working: they normalize at load time. Sessions stored with legacy mode names restore to the canonical mode. The ACP protocol still accepts the legacy names for external clients (Zed etc.).
