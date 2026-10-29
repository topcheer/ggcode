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
- **Persistence-backdoor writes**: shell redirects to out-of-sandbox **or** sensitive paths (`~/.ssh/authorized_keys`, `~/.bashrc`, `/etc/**`) and file-tool writes outside the sandbox - Ask. These are rare by construction and never a daily-interruption source.
- **Credential-payload uploads** (narrow watchdog floor): upload-direction transfers whose own command text references a credential location (`scp ~/.ssh/id_rsa host:`, `curl -d @~/.aws/credentials`) - Ask. Ordinary uploads are unaffected.

Network exfiltration and sensitive-path **reads** are no longer gated in bypass (they were the top false-positive sources; the exfiltration floor remains in supervised).

```bash
ggcode --bypass
```

### `plan`

Tool-entered read-orientation mode. Denies explicit file-writing tools only; reads, shell probes, and builds are allowed (extremely-dangerous, exfiltration, and redirect-to-backdoor floors still ask) so planning stays grounded in real diagnostics. Note this intentionally permits non-redirect indirect side effects (a `git commit -a`, `make`, or `curl -o file` run during planning) — the contract trades strict read-only-ness for grounded planning. Entered with `enter_plan_mode`, exited with `exit_plan_mode`.

## Comparison

| Scenario | `supervised` | `bypass` |
|----------|--------------|----------|
| Safe command (`go build`, `ls`) | Allow | Allow |
| Extremely dangerous command (`rm -rf /`) | Ask | Ask |
| Ordinary dangerous command (`sudo rm file`) | Allow | Allow |
| Credential exfiltration (`curl -d @~/.ssh/...`) | Ask | Allow |
| Write inside workspace | Allow | Allow |
| Write outside workspace | Ask (file tools and shell redirects) | Ask (file tools and shell redirects) |
| Redirect to `~/.ssh/authorized_keys` (incl. `sh -c '... > path'`) | Ask | Ask |
| Read `~/.aws/credentials` | Allow | Allow |

## Mode-independent controls

- **Deny rules** (`permission_rules.json`, ACP/A2A deny) win in every mode, including bypass. Explicit **ask** rules (per-tool or command-pattern) are also honored in every mode - the two-mode default-Allow never silently overrides a rule the user pinned. Explicit **allow** rules cannot cross the safety floors (a floor always asks regardless).
- **Dangerous-but-not-extreme commands** (`IsDangerous` tier: `git push --force`, `sudo` file ops, etc.) are **allowed** in both modes - deliberate: the interruption budget is reserved for system-level destruction and persistence backdoors. Deny/ask rules can still pin them.
- **`switch_mode` escalation gate**: switching to `bypass` always asks for user consent, from any mode.
- **Shift+Tab / status-bar click** cycles `supervised` ↔ `bypass`.

## Legacy aliases

Old configs (`default_mode: auto` / `autopilot`) keep working: they normalize at load time. Sessions stored with legacy mode names restore to the canonical mode. The ACP protocol still accepts the legacy names for external clients (Zed etc.).

## Related mechanisms (previously in this page)

- **Allowed-risk accumulation** and **session-scoped persistence** still exist; see the permission rules guide (`docs/guide/permission-rules.md`) and `/allow` command docs.
- **Pipe mode** (`ggcode -p`) still defaults to supervised.
