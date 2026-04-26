# A2A Authentication Guide

This document describes the authentication mechanisms supported by ggcode's A2A (Agent-to-Agent) server, their applicable scenarios, and configuration examples.

## Authentication Schemes Overview

| Scheme | Spec Type | Secret Required | Best For | User Interaction |
|--------|-----------|----------------|----------|-----------------|
| **API Key** | `apiKey` | Shared secret | Development, trusted networks | None |
| **OAuth2 + PKCE** | `oauth2` | No (public client) | Human-driven agents, web integrations | Browser login |
| **OAuth2 Device Flow** | `oauth2` | No (public client) | Headless servers, SSH, CI/CD | Visit URL + enter code |
| **OpenID Connect** | `openIdConnect` | No (PKCE) | Enterprise identity, SSO | Browser login |
| **Mutual TLS** | `mutualTLS` | Certificates only | Machine-to-machine, zero-trust | None (automated) |

---

## 1. API Key (Default)

**Spec type:** `apiKey`  
**Scenario:** Local development, trusted internal networks, quick prototyping.

### How it works
- A shared secret string is configured on both the A2A server and client.
- The client sends the key in the `X-API-Key` HTTP header.
- Constant-time comparison prevents timing attacks.

### Configuration

```yaml
# ggcode.yaml
a2a:
  enabled: true
  auth:
    api_key: "${A2A_API_KEY}"  # from env var (under auth: block)
```

> **Note:** The legacy top-level `a2a.api_key` still works, but `a2a.auth.api_key` takes priority.

### When to use
- ✅ Local development / testing
- ✅ Trusted internal network (VPN/VPC)
- ✅ Quick prototyping
- ❌ Public networks (secret can be intercepted)
- ❌ Multi-tenant environments (no user identity)
- ❌ Production deployments needing audit trails

### Limitations
- Single shared secret — no per-user differentiation
- No identity information — cannot tell who made the request
- Secret must be distributed to all clients out-of-band

---

## 2. OAuth2 + PKCE (Authorization Code Flow)

**Spec type:** `oauth2` (authorizationCode)  
**Scenario:** Agents that operate with human initiation, web-based integrations, any environment with a browser.

### How it works
```
┌──────────┐        ┌──────────┐        ┌──────────┐
│ ggcode   │        │ Browser  │        │ Identity │
│ (client) │        │          │        │ Provider │
└─────┬────┘        └────┬─────┘        └────┬─────┘
      │ generate PKCE      │                   │
      │ code_verifier      │                   │
      │ code_challenge     │                   │
      │───────────────────>│  open browser     │
      │                    │──────────────────>│  authorize
      │                    │<──────────────────│  consent
      │<───────────────────│  callback(code)   │
      │ exchange code + verifier for token      │
      │────────────────────────────────────────>│
      │<────────────────────────────────────────│  access_token
      │                                        │
      │ use Bearer token for A2A requests       │
```

**Key point:** No `client_secret` needed for public clients. PKCE (Proof Key for Code Exchange) proves the token exchange is coming from the same client that started the flow. For confidential clients (GitHub OAuth Apps), `client_secret` is required.

**Flow selection:** Set `flow: "pkce"` (default) or `flow: "device"` to explicitly choose the OAuth2 flow. If omitted, the system selects based on provider capabilities.

### Configuration

**Zero-config (GitHub only — client_id built-in):**

```yaml
# ggcode.yaml
a2a:
  enabled: true
  auth:
    oauth2:
      provider: "github"   # client_id auto-filled, just works
```

**Using a built-in provider preset with custom client_id:**

```yaml
# ggcode.yaml
a2a:
  enabled: true
  auth:
    oauth2:
      provider: "github"
      client_id: "Ov23li-your-registered-app-id"    # overrides default
      scopes: "read:user user:email"                 # optional, defaults from preset
```

**Using a custom provider:**

```yaml
# ggcode.yaml
a2a:
  enabled: true
  auth:
    oauth2:
      client_id: "my-custom-client"
      issuer_url: "https://my-idp.example.com"
      scopes: "openid profile email"
```

> **Note:** GitHub has a built-in public client_id (PKCE-only, no secret).
> Just set `provider: "github"` for instant zero-config. Other providers
> require you to register an OAuth App and provide the client_id.

### Available Provider Presets

| Provider    | `provider` value | PKCE | Device Flow | Notes |
|-------------|-----------------|------|-------------|-------|
| GitHub      | `"github"`      | ✅   | ✅          | Register at Settings → Developer settings → OAuth Apps |
| Google      | `"google"`      | ✅   | ❌          | Register at Google Cloud Console → APIs & Services → Credentials |
| Auth0       | `"auth0"`       | ✅   | ✅          | Replace `AUTH0_TENANT` in URLs with your tenant name |
| Azure AD    | `"azure"`       | ✅   | ✅          | Replace `AZURE_TENANT` in URLs with your tenant ID |

### When to use
- ✅ Agents triggered by humans (with browser access)
- ✅ Web-based agent dashboards
- ✅ Multi-user environments needing per-user identity
- ✅ Any scenario where the IdP is accessible
- ❌ Headless servers without browser
- ❌ Fully automated machine-to-machine

### Supported Identity Providers

| Provider | issuer_url | Notes |
|----------|-----------|-------|
| Google | `https://accounts.google.com` | Register OAuth2 Web App (public) |
| GitHub | `https://github.com/login/oauth` | Set OAuth App as public |
| Auth0 | `https://<tenant>.auth0.com` | Enable PKCE in application settings |
| Keycloak | `https://<host>/realms/<realm>` | Self-hosted, full control |
| Azure AD | `https://login.microsoftonline.com/<tenant>` | Enterprise scenarios |

---

## 3. OAuth2 Device Authorization Flow

**Spec type:** `oauth2` (deviceCode)  
**Scenario:** Headless servers, SSH sessions, CI/CD pipelines, environments without a browser.

### How it works
```
┌──────────┐                             ┌──────────┐
│ ggcode   │                             │ Identity │
│ (client) │                             │ Provider │
└─────┬────┘                             └────┬─────┘
      │ POST device_code request              │
      │──────────────────────────────────────>│
      │<──────────────────────────────────────│  user_code + verification_uri
      │                                        │
      │ Display: "Visit https://... Enter: ABCD-1234"
      │                                        │
      │ Poll: POST token request (slow)        │
      │──────────────────────────────────────>│  authorization_pending
      │<──────────────────────────────────────│
      │ ... user visits URL and enters code ...│
      │ Poll: POST token request               │
      │──────────────────────────────────────>│
      │<──────────────────────────────────────│  access_token ✓
```

**Key point:** No browser on the server needed. User authenticates on any device with a browser.

### Configuration

```yaml
# ggcode.yaml
a2a:
  enabled: true
  auth:
    oauth2:
      client_id: "ggcode-a2a-device"                     # safe to embed
      issuer_url: "https://github.com/login/oauth"       # must support device flow
      scopes: "read write"
```

### When to use
- ✅ SSH / remote server environments
- ✅ CI/CD pipelines with human approval
- ✅ Containers / headless VMs
- ✅ Any environment where browser is not available
- ❌ Fully automated M2M (use mTLS instead)
- ❌ High-frequency automated calls (token lifecycle overhead)

### Supported Identity Providers

| Provider | Device Flow Support |
|----------|-------------------|
| GitHub | ✅ Native |
| Google | ❌ Not supported |
| Auth0 | ✅ With configuration |
| Azure AD | ✅ Native |
| Keycloak | ✅ With protocol mapper |

---

## 4. OpenID Connect (OIDC)

**Spec type:** `openIdConnect`  
**Scenario:** Enterprise SSO, identity federation, environments needing verified user identity.

### How it works
OIDC = OAuth2 + standardized identity layer. Same flow as OAuth2 + PKCE, but additionally:
- Receives an `id_token` (signed JWT with user claims)
- Can call `/userinfo` endpoint for profile data
- Automatic configuration via `/.well-known/openid-configuration`

```
Same as OAuth2 + PKCE, but also:
  → id_token contains: sub, name, email, groups, ...
  → Server validates JWT signature using provider's JWKS
  → User identity extracted from token claims
```

### Configuration

**Using a built-in provider preset:**

```yaml
# ggcode.yaml
a2a:
  enabled: true
  auth:
    oidc:
      provider: "google"                                # auto-fills discovery URL
      client_id: "your-client-id.apps.googleusercontent.com"
      scopes: "openid profile email groups"
```

**Using a custom OIDC provider:**

```yaml
# ggcode.yaml
a2a:
  enabled: true
  auth:
    oidc:
      client_id: "my-oidc-client"
      issuer_url: "https://keycloak.example.com/realms/myrealm"
      scopes: "openid email roles"
```

> The `issuer_url` must serve `/.well-known/openid-configuration` with
> standard OIDC discovery metadata.

### When to use
- ✅ Enterprise environments with SSO (Okta, Azure AD, Keycloak)
- ✅ Need for auditable user identity in A2A requests
- ✅ Group/role-based access control
- ✅ Federated identity across organizations
- ❌ Simple single-user setups (API Key is simpler)
- ❌ Machine-to-machine without identity context

---

## 5. Mutual TLS (mTLS)

**Spec type:** `mutualTLS`  
**Scenario:** Machine-to-machine communication, zero-trust networks, environments where no IdP is available.

### How it works
```
┌──────────┐                           ┌──────────┐
│ ggcode   │     TLS Handshake         │ ggcode   │
│ (client) │◄─────────────────────────►│ (server) │
└──────────┘   client_cert ↔ server_cert└──────────┘
                    │
            Both sides verify
            the other's certificate
            against a shared CA
```

- Each ggcode instance has its own certificate signed by a shared CA.
- TLS handshake validates both sides — no tokens, no passwords.
- Certificate = Identity.

### Configuration

```yaml
# ggcode.yaml
a2a:
  enabled: true
  auth:
    mtls:
      cert_file: "/etc/ggcode/a2a/server.crt"
      key_file: "/etc/ggcode/a2a/server.key"
      ca_file: "/etc/ggcode/a2a/ca.crt"
```

### Certificate Management

**Option A: Self-hosted CA (step-ca / cfssl)**
```bash
# Install step-ca
step ca init --name="ggcode-a2a" --provisioner="admin"

# Issue server cert
step ca certificate "agent1.example.com" server.crt server.key

# Issue client cert
step ca certificate "agent2.example.com" client.crt client.key
```

**Option B: HashiCorp Vault PKI**
```bash
vault secrets enable pki
vault write pki/root/generate/internal common_name="ggcode-a2a"
vault write pki/roles/ggcode allowed_domains="example.com"
vault write pki/issue/ggcode common_name="agent1.example.com"
```

### When to use
- ✅ Pure machine-to-machine (no human in the loop)
- ✅ Zero-trust network architectures
- ✅ Environments without internet/IdP access (air-gapped)
- ✅ High-security deployments requiring cryptographic identity
- ✅ Low-latency requirements (no token validation round-trips)
- ❌ Environments where certificate management is too complex
- ❌ Need for human identity (use OAuth2/OIDC instead)
- ❌ Dynamic/unmanaged clients (cert distribution overhead)

---

## Combining Multiple Schemes

Multiple authentication mechanisms can be enabled simultaneously:

```yaml
# ggcode.yaml
a2a:
  enabled: true
  api_key: "${A2A_API_KEY}"              # Legacy / dev fallback
  auth:
    oauth2:                                # Human-driven agents
      client_id: "ggcode-a2a-public"
      issuer_url: "https://accounts.google.com"
      scopes: "openid profile"
    mtls:                                  # Machine-to-machine
      cert_file: "/etc/ggcode/a2a/server.crt"
      key_file: "/etc/ggcode/a2a/server.key"
      ca_file: "/etc/ggcode/a2a/ca.crt"
```

When multiple schemes are configured, the server accepts **any** valid credential:
1. `X-API-Key` header → API Key validation
2. `Authorization: Bearer <token>` → OAuth2/OIDC validation
3. Client certificate (TLS) → mTLS validation

---

## Decision Matrix

```
Is there a human in the loop?
├── Yes, with browser
│   └── Need identity/SSO?
│       ├── Yes → OpenID Connect
│       └── No  → OAuth2 + PKCE
├── Yes, no browser (SSH/CI)
│   └── OAuth2 Device Flow
└── No, pure machine-to-machine
    ├── Have a CA/identity provider?
    │   ├── Yes → Mutual TLS
    │   └── No  → API Key (internal network only)
    └── Have an OAuth2 IdP?
        ├── Yes → OAuth2 Client Credentials
        └── No  → API Key (development only)
```

---

## Security Considerations

| Concern | API Key | OAuth2+PKCE | Device Flow | OIDC | mTLS |
|---------|---------|-------------|-------------|------|------|
| Secret in source code | ⚠️ Risk | ✅ No secret | ✅ No secret | ✅ No secret | ✅ Certs only |
| Man-in-the-middle | ⚠️ Use HTTPS | ✅ PKCE | ✅ PKCE | ✅ PKCE | ✅ Built-in |
| Identity tracking | ❌ None | ✅ Per-user | ✅ Per-user | ✅ Full claims | ✅ Per-cert |
| Token theft | ⚠️ Permanent | ✅ Expires | ✅ Expires | ✅ Expires | ✅ No tokens |
| Revocation | ❌ Manual | ✅ IdP | ✅ IdP | ✅ IdP | ✅ CRL/OCSP |
| Setup complexity | ⭐ Minimal | ⭐⭐ Low | ⭐⭐ Low | ⭐⭐⭐ Medium | ⭐⭐⭐ Medium |
| Production ready | ⚠️ Limited | ✅ Yes | ✅ Yes | ✅ Yes | ✅ Yes |

---

## Token Persistence & Cache

OAuth2/OIDC tokens are automatically cached to disk so the agent doesn't need to re-authenticate on every restart.

### Cache Location

```
~/.ggcode/oauth-tokens/
  github-Ov23liq0EQyT.json     ← built-in GitHub client_id
  github-my-own-app.json        ← user's own client_id
  auth0-abc123def456.json       ← different provider
```

### Isolation Rules

- **Cache key** = `{provider}-{clientID[:12]}` — first 12 chars of client ID
- Same `client_id` + `provider` = shared token file (intentional — same OAuth App)
- Different `client_id` = isolated cache (prevents multi-instance overwrites)
- File permissions: `0600` (owner read/write only)

### Lifecycle

1. Client needs a token → checks cache first
2. Cache hit + not expired → reuse (no user interaction)
3. Cache miss or expired → initiate OAuth2 flow (PKCE or Device)
4. Token received → save to cache with expiry
5. Daemon restart → cache still valid → seamless

> The token cache is purely client-side. Server auth state is config-driven — no server-side token persistence needed.

---

## Instance-Level Config Override

Each ggcode workspace can override the global A2A config via `.ggcode/a2a.yaml`:

```yaml
# .ggcode/a2a.yaml — merges into global a2a config
auth:
  api_key: "project-specific-key"
  oauth2:
    provider: "github"
    flow: "device"
```

This allows per-project auth settings while sharing the same global config. Fields are merged — instance values override global values.

---

## IM Slash Commands (Daemon Mode)

When running in daemon mode with IM adapters, the following slash commands are available for adapter management:

| Command | Description |
|---------|-------------|
| `/listim` | List all IM adapters with name, platform, status (online/muted/active) |
| `/muteim <name>` | Mute a specific adapter by name. Cannot mute yourself — use `/muteself` instead |
| `/muteall` | Mute all adapters **except** the one you're messaging from |
| `/muteself` | Mute THIS adapter. You will stop receiving all replies. Use `/restart` from another adapter to recover |
| `/restart` | Restart daemon — unmutes all adapters (mute is in-memory, not persisted) |
| `/help` | Show available slash commands |

**Important behaviors:**
- Mute is **in-memory only** — not persisted to the binding store. Daemon restart recovers all adapters.
- `/muteall` uses `MuteAllExcept(selfAdapter)` — the sender's adapter is never touched.
- `/muteself` emits the warning message **before** muting (500ms delay ensures delivery).
