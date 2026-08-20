# Enterprise Central Management (中央管控)

## Overview

Enterprise governance for ggcode: a control plane (enterprise API gateway)
distributes client policies that are **enforced, not suggested**. Covered
policy domains:

| Domain | Enforcement |
|---|---|
| API gateway routing | All LLM traffic through the enterprise gateway |
| Model allowlist | Only approved vendor/model/endpoint combos |
| Subagent caps | Max concurrent/run/total budget for sub-agents & teammates |
| Hooks | Mandatory audit/pre-tool hooks, locally unremovable |
| SSO | OIDC login gate; no valid token, no session |
| MCP config | Server allowlist + forced servers + blocked servers |

Design principle: **managed policy overlays, existing enforcement points**.
We do not build a parallel control system; we layer a signed, gateway-sourced
policy above `ggcode.yaml` and wire it into the places that already decide
these things today.

## Motivation

- Enterprises cannot adopt an agent CLI where any user can point at any
  endpoint, spawn unlimited sub-agents, or disable logging.
- Competitors: Claude Code (managed policy JSON + Bedrock/Vertex routing),
  Cursor (Teams mode with forced settings), GitHub Copilot (enterprise
  policy: model list, network allowlist). ggcode has none.
- ggcode already has every primitive this needs - override merge
  (`LoadA2AOverride`), limit application (`ApplyResolvedLimitsToAgent`,
  `ApplyToolCallBudget`), hook dispatch, MCP config, OIDC/OAuth2/mTLS in the
  A2A stack, command gating (`ApplyDisabledState`). What is missing is the
  distribution + enforcement spine.

## Architecture

```
+------------------+
| Enterprise       |  policy authoring, signing (ed25519), distribution
| Control Plane    |  REST: GET /v1/policy  (ETag, monotonic version)
| (API Gateway)    |  SSE:  /v1/policy/events (push on change)
+--------+---------+
         | signed policy bundle (JSON, version, issued_at, signatures[])
         v
+--------------------+     +---------------------------+
| internal/enterprise|---->| internal/config           |
|  fetcher (pull +   |     |  managed overlay:         |
|  SSE push, cache,  |     |  EnterprisePolicy struct  |
|  verify, anti-     |     |  precedence: managed >   |
|  rollback)         |     |  instance > user          |
+--------------------+     +------------+--------------+
                                        |
        +---------------+---------------+---------------+
        v               v               v               v
  provider/model   agentruntime     hooks          cmd/ggcode
  resolution       limits + spawn   dispatch       startup gate
  (allowlist)      gating           (injected)     (SSO verify)
```

### Precedence and lock semantics

Every managed field carries a mode:

- `set` - default; user config wins if it explicitly sets the field
- `locked` - managed value always wins; local edits are ignored + surfaced
  in `/config` as "managed by your organization"

This mirrors `LoadA2AOverride`'s field-level override semantics, extended
with the `locked` flag and a deny path.

## Policy schema (v1)

```yaml
apiVersion: enterprise.ggcode.dev/v1
version: 42              # monotonic; lower versions rejected (anti-rollback)
issuedAt: 2026-08-20T10:00:00Z
gateway:
  url: https://llm-gw.corp.example
  auth: token | oidc | mtls
  requestTimeout: 120s
models:
  allow:                 # allowlist; empty = deny all (fail-closed)
    - vendor: glm
      endpoint: corp-gw
      models: [glm-5.3, glm-5.2]
  default: glm/glm-5.3
  locked: true
subagents:
  maxConcurrent: 4       # running at once
  maxPerRun: 16          # per user turn
  tokenBudgetPerRun: 2_000_000
  allowTeammates: true
  allowDelegation: false
hooks:
  forced:                # injected; locally unremovable, run FIRST
    - type: pre_tool
      command: ["corp-audit-hook", "--siem"]
  locked: true
sso:
  required: true
  oidc:
    issuer: https://idp.corp.example
    clientID: ggcode-cli
    scopes: [openid, profile, ggcode.dev/use]
  maxSessionAge: 12h
mcp:
  allow: ["filesystem", "corp-registry"]   # by server name or URL prefix
  force:                                    # auto-connect on startup
    - name: corp-registry
      url: https://mcp.corp.example
  block: ["web-fetch"]
permissions:
  maxMode: auto            # ceiling: user may pick supervised..maxMode
  denyTools: [run_command_interactive]
commands:
  disable: [/share, /tunnel]
```

Bundle is served as JSON with detached ed25519 signatures; the client pins
the public key set out-of-band (enrollment) and validates before applying.

## Enforcement point mapping

Every policy domain maps to code that already decides the behavior. New
package `internal/enterprise` supplies the policy; one wiring point per row:

| Policy | Existing anchor | Wiring |
|---|---|---|
| Gateway routing | `config.ResolveActiveEndpoint`, vendor/endpoint structs (`config_vendor.go`) | New pseudo-vendor `enterprise-gw`; when policy has `gateway`, it becomes the only resolvable endpoint |
| Model allowlist | `ApplyResolvedLimitsToAgent` (`agentruntime/model_limits.go`), `/model` + `/provider` switches (TUI `commands.go`, IM `remote_commands.go`) | `enterprise.CheckModel(vendor, endpoint, model)` gate before every switch; `/model` list filtered; violations get a managed-policy error, not a generic one |
| Subagent caps | `ApplyToolCallBudget` (`agentruntime/model_switch.go`), `spawn_agent`/`teammate_spawn`/`delegate` tools | `ApplySubagentCaps(agent, policy)` alongside the budget calls; spawn tools consult a cap tracker before forking |
| Hooks | `internal/hooks` Dispatch, `HookConfig` | Managed hooks prepended to the dispatch list and marked `forced`; config loader cannot drop them; `hooks.Locked()` prevents local disable |
| SSO | `internal/auth` OIDC/device-flow primitives (A2AOIDCConfig already in config), `cmd/ggcode` startup | Pre-flight: if `sso.required`, acquire/refresh token via device flow, bind to machine, store in keychain; refuse non-read-only start on failure. Gateway calls carry the token (or exchange for gateway token) |
| MCP | `mcp_servers` config load path | Filter `allow`/`block` at load; append `force` entries; managed servers flagged non-removable in `/mcp` |
| Permission ceiling | `permission.ConfigPolicy.SetMode`, `/mode` handler | Clamp: requested mode above `maxMode` clamps + warns; `bypass`/`autopilot` unreachable when ceiling is lower |
| Command disable | `commands.ApplyDisabledState` | Managed disabled-set merges with local; managed entries win |

The IM slash registry (see `im-slash-registry` design) needs no change:
`/model`, `/mode` route through the same TUI/daemon handlers, so the gates
hold on both inbound paths.

## Distribution lifecycle

1. **Enrollment** (one-time): `ggcode enterprise enroll --url https://ctrl.corp`
   - device-code OIDC login, fetch+pin gateway signing keys, write
   `~/.ggcode/enterprise/enrollment.json` (keys, gateway URL, machine ID).
2. **Startup pull**: fetch policy, verify signatures + version > cached.
3. **Runtime push**: SSE subscription refreshes policy live; enforced
   domains re-apply (model switch re-check, hooks reload, MCP reconnect).
4. **Offline**: cached policy with `maxCacheAge` (default 72h). Expired +
   unreachable ⇒ `sso.required` fields fail closed (no start); others warn
   and continue on last-known-good. Fail-open/closed is per-domain:
   security domains (SSO, hooks, model allowlist) fail closed; productivity
   domains (subagent caps, MCP allow) fail open-with-warning.
5. **Anti-rollback**: monotonic `version`; server-verified revocation list
   for enrollment keys.

## Security considerations

- **Policy signing**: ed25519, threshold-of-N key set, keys rotated via the
  revocation list. Client rejects unsigned/low-version bundles.
- **Transport**: mTLS to control plane (reuse `A2AMTLSConfig` machinery).
- **Tamper resistance**: managed overlay held in memory; cached bundle
  checksummed; local edits to cache detected at next verify.
- **UX transparency**: `/config` shows every locked field with a
  "managed by your organization" badge; `ggcode enterprise status` prints
  policy version, gateway, and what is enforced. Silent enforcement is a
  support nightmare - surface it.
- **Escape-hatch denial**: no CLI flag bypasses a locked field. `--config`
  pointing elsewhere still loads the enrollment (it lives outside the
  config dir chain). Support path: `ggcode enterprise unenroll` requires
  gateway approval or breaks SSO (documented, not hidden).

## Rollout plan

- **P1 - policy fetch + model allowlist + permission ceiling** (highest
  value, narrowest wiring; proves the overlay spine end-to-end)
- **P2 - subagent caps + hooks forced + MCP allow/force**
- **P3 - SSO gate + gateway pseudo-vendor routing + live push**
- **P4 - reporting/telemetry hooks (opt-in per policy), revocation hardening**

Each phase is independently shippable; the overlay + verifier (`internal/
enterprise`) lands first and is dead code until an enrollment exists -
zero impact on non-enterprise users.

## Open questions

1. Policy authoring UI lives in the control plane - now designed in
   `enterprise-control-plane.md` (same directory); the schema above remains
   the shared contract between the two halves.
2. Team/project-scoped policies (different caps per repo)? Open in both
   halves: v1 is machine-global on the client and single-policy on the
   control plane (see its Open questions); the workspace-matcher seam is
   noted in both designs for v2.
3. Usage reporting granularity - decided by the control plane telemetry
   model (`enterprise-control-plane.md`, Data model + Admin UI #5): per
   user x model x repo-hash (repo sent as salted hash, never plaintext).
   Remaining open piece is retention/aggregation policy, deferred to P4.
