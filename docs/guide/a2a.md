# Agent-to-Agent (A2A) Protocol

A2A lets ggcode instances communicate and delegate tasks across workspaces and projects.

## Overview

In a monorepo with multiple services, each project can run its own ggcode instance. A2A allows these instances to talk to each other — delegating work, requesting code reviews, or executing commands remotely.

## Starting an A2A Server

ggcode automatically registers as an A2A agent when **daemon mode** is active. No manual server startup is required.

## Discovering Agents

List all connected ggcode instances:

```
a2a_discover
```

### Signed Agent Cards (A2A §8.4)

Publishers may attach JWS (RFC 7515) signatures to their Agent Card. A
signature covers the RFC 8785 (JCS) canonicalization of the card JSON with the
`signatures` field itself excluded. The verification key is taken from an
embedded `jwk` or a `jku` JWKS URL (https only; plain http is accepted for
loopback in local development).

ggcode's client verifies signatures automatically during discovery:

- a signature that **fails to verify** (ES256/RS256/EdDSA with a resolvable
  key) proves the card was modified in transit — discovery **refuses** the
  card;
- signatures the build cannot validate (e.g. symmetric HS256, or a key that
  cannot be fetched) are reported but do not block; the card is then used
  unverified, the same as a client without this capability.

Authenticated extended cards (`agent/getExtendedCard`) are verified the same
way.

## Sending Tasks

Delegate work to another ggcode instance by project name:

```
a2a_send_task --target order-service --skill full-task --message "Add pagination to the orders API"
```

## Remote Execution

Call a specific skill on a remote instance:

```
a2a_remote --target user-service --skill code-review --message "Review the latest commit"
```

Available skills: `code-edit`, `file-search`, `command-exec`, `git-ops`, `code-review`, `full-task`.

## Configuration

A2A supports multiple authentication schemes, configured in `~/.ggcode/ggcode.yaml`:

```yaml
a2a:
  host: 0.0.0.0:7878      # 0.0.0.0 when auth configured, 127.0.0.1 otherwise
  auth:
    api_key: "your-secret"                # Shared secret (simplest)
    api_keys:                             # Additional keys
      - "${A2A_EXTRA_KEY}"
    oauth2:
      provider: "github"                  # Zero-config GitHub OAuth
      # flow: "device"                    # or Device Flow
    oidc:
      provider: "google"                  # OpenID Connect
      client_id: "xxx"
    mtls:
      cert_file: ".ggcode/certs/server.pem"
      key_file: ".ggcode/certs/server.key"
      ca_file: ".ggcode/certs/ca.pem"
  lan_discovery: false                     # mDNS broadcast for LAN discovery
```

### LAN Team Mode (Zero Config)

**LAN discovery is ON by default.** When you install ggcode and start it,
it automatically:

1. Binds to `0.0.0.0` (LAN accessible)
2. Broadcasts via mDNS (`_ggcode._tcp`)
3. Uses a built-in community key (`ggcode-lan-a2a-v1`) for authentication

Any other ggcode instance on the same network will auto-discover and can
immediately send tasks or delegate work — **no configuration needed**.

#### Disabling LAN Discovery

For single-user or security-sensitive environments:

```yaml
a2a:
  lan_discovery: false   # binds to 127.0.0.1 only
```

#### Using Your Own API Key

For teams that want real authentication:

```yaml
a2a:
  auth:
    api_key: "your-team-secret"
```

All team members must use the same key.

### Host Binding

- **Always binds to `0.0.0.0`** (LAN accessible)
- Loopback addresses (`127.0.0.1`, `::1`, `localhost`) are automatically overridden to `0.0.0.0`
- This ensures mDNS peer discovery and LAN Chat always work
- A built-in community key (`ggcode-lan-a2a-v1`) provides authentication even without explicit auth config

### Instance-Level Override

Per-workspace A2A config via `.ggcode/a2a.yaml` in the workspace root.

## Security

- A built-in default key (`ggcode-lan-a2a-v1`) is always active when no custom auth is configured — it ensures only ggcode instances can connect
- Multiple auth schemes can be enabled simultaneously (any matching scheme authenticates)
- Without custom auth or lan_discovery, only localhost connections are accepted
- API keys support `${ENV_VAR}` expansion and are stored in `keys.env`
- OAuth2 supports PKCE and Device Flow
- OIDC adds identity layer on top of OAuth2 with JWKS key rotation
- mTLS provides mutual certificate-based authentication

See [A2A Authentication Guide](../a2a-auth.md) for detailed setup instructions.

## Protocol Extensions (A2A v1.0)

ggcode implements the A2A extension negotiation protocol:

- **Declaration**: an agent lists the extensions it supports in the Agent Card (`capabilities.extensions`, mirrored in the legacy top-level `extensions` field for older peers).
- **Activation**: a client opts in by sending the `A2A-Extensions` request header (comma-separated extension URIs) on JSON-RPC calls. The agent echoes back the set it actually activated — unknown/unsupported URIs are ignored, never substituted.
- **Required extensions**: when an extension is declared `required: true`, clients that did not activate it are rejected with JSON-RPC error `-32004` (Unsupported operation), naming the missing URIs.

### Serving extensions (server side)

Declare the extensions this instance supports in the `a2a` config block:

```yaml
a2a:
  extensions:
    - uri: "https://example.com/ext/timestamp/v1"
      description: "Adds server timestamps to task artifacts"
      required: false
      params:
        timezone: "Asia/Shanghai"
    - uri: "https://example.com/ext/signed-message/v1"
      required: true   # clients must activate it or get -32004
```

### Activating remote extensions (client side)

To opt in to extensions on remote agents this instance calls:

```yaml
a2a:
  activate_extensions:
    - "https://example.com/ext/signed-message/v1"
```

Outgoing calls automatically attach the `A2A-Extensions` header. Before sending, the client checks the remote agent card: if an extension is marked `required` there but not activated here, the call **fails fast** with a clear error instead of violating the remote agent's contract.

The canonical well-known path `/.well-known/agent-card.json` is served in addition to `/.well-known/agent.json` and `/.well-known/a2a.json`.
