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
