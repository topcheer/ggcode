# Enterprise Control Plane (控制面产品)

## Overview

The server half of central management. The client design
(`enterprise-central-management.md`) defines what ggcode CLI enforces; this
document defines the product that **authors, signs, distributes, and
observes** those policies:

- Policy authoring + review workflow (4-eyes approval)
- Signing infrastructure (ed25519 key set, rotation, revocation)
- Distribution API (pull + SSE push, the exact endpoints the client expects)
- Enrollment / fleet management (who runs which policy version)
- Telemetry ingestion (P4 usage reports)
- Admin UI + RBAC + audit log

Deployment shape follows the `ggcode-relay/` precedent: a separate Go
module (`ggcode-control/`), single static binary, SQLite by default
(WAL mode), Postgres for scale-out, Docker image, stateless enough to run
two replicas behind an LB once Postgres is in.

```
+-----------------------+   admin SSO (corp IdP)
| Admin UI (embedded SPA)|---RBAC: viewer/author/approver/admin
+-----------+-----------+
            | /api (admin API)
+-----------v---------------------------------------+
| ggcode-control                                     |
|  policy service | signing svc | fleet | telemetry |
|  SQLite/Postgres | SSE hub | audit log (append-only)|
+---+-------------------+-------------------+-------+
    | /v1/policy        | /v1/enroll       | /v1/policy/events (SSE)
    v                   v                   v
  ggcode CLI clients (enrolled machines)
```

## Motivation

- Without an authoring product, "central management" degrades to admins
  hand-editing YAML on a file server - no approval trail, no fleet
  visibility, no staged rollout. Enterprises buy the workflow, not the
  schema.
- Precedent: Claude Code's managed-policy story is config-file-only and
  widely cited as its enterprise weak spot; JetBrains Gateway and Cursor
  Teams both ship a console. The console is the moat.
- Everything here is conventional server engineering; the hard part (the
  enforcement contract) is already specified client-side. This document
  exists so the two halves never drift.

## Policy lifecycle

```
draft -> in_review -> approved -> signed -> published
  ^          |                        |
  +-- reject +           staged: 1% -> 25% -> 100% (fleet canary)
```

1. **Draft**: author edits policy as YAML against the v1 schema (same
   schema the client parses; the server validates on save).
2. **Review**: author != approver enforced (4-eyes). Approvers see a
   structured diff against the currently published version, not raw YAML
   noise.
3. **Sign**: on approval the server renders the canonical bundle (JSON,
   monotonic version, issuedAt) and signs it with the active key set
   (threshold-of-N, default N=1 for small orgs).
4. **Publish**: immutable. Republishing = new version. Rollback = publish
   an old config as a new version (monotonic version still moves forward;
   this is deliberate - the client anti-rollback check must never see
   history go backwards).
5. **Staged rollout**: a published version carries a rollout percentage.
   The policy endpoint picks per-machine (hash(machine_id) < threshold) so
   canary assignment is stable across restarts.

## API contract (client-facing)

Exactly the endpoints the client design depends on; changing any of these
is a breaking change against enrolled fleets:

| Endpoint | Purpose |
|---|---|
| `POST /v1/enroll/start` | Device-code flow start; returns user_code + verification_uri |
| `POST /v1/enroll/complete` | Client polls with device_code; on approval returns enrollment token + pinned signing keys |
| `GET /v1/policy?machine_id=` | Current signed bundle for this machine (canary-aware); ETag + version |
| `GET /v1/policy/events` | SSE: policy-version pushes + key-rotation notices |
| `POST /v1/telemetry` | Batched usage reports (P4); fire-and-forget, 202 |

Auth: enrollment token (bearer) for policy/events; mTLS optional
(control-plane cert pinned at enrollment).

## Data model (SQLite/Postgres, same DDL)

```sql
machines(id, org_id, machine_id, user_email, device_meta JSON,
         enrolled_at, last_seen_at, enrolled_keys JSON, revoked INT)
policies(id, org_id, version INT, yaml TEXT, rendered_bundle JSON,
         created_by, created_at)                    -- immutable rows
policy_states(org_id, published_version INT, rollout_pct INT,
              updated_by, updated_at)               -- one active row per org
approvals(policy_id, reviewer, decision, note, decided_at)
signing_keys(key_id, public_key, private_key_encrypted, status,
             created_at, retired_at)                -- status: active|retired|revoked
audit_log(id, ts, actor, action, object, before JSON, after JSON,
          sig)  -- append-only; sig = chained HMAC so deletion is detectable
telemetry(id, machine_id, ts, tokens_in, tokens_out, cost_usd, model,
          repo_hash)   -- P4; repo sent as salted hash, never plaintext
```

Single-org v1: org_id is a constant; the column exists so multi-tenant is
a migration, not a rewrite.

## Signing infrastructure

- **Key storage**: v1 - private keys encrypted at rest (AES-GCM with a
  master key from env/KMS-less secret file); plugin interface
  `SignerProvider` (crypto.Signer) so HSM/KMS (AWS KMS, Vault) drops in
  without touching the policy service.
- **Rotation**: new key signs alongside the old for a grace window (both
  listed in the bundle's signatures[]); retirement happens when fleet
  telemetry shows zero clients pinned to the old key only.
- **Revocation**: `/v1/policy/events` pushes a key-revocation notice;
  bundles signed solely by a revoked key fail client verification. The
  revocation list itself is part of the signed bundle header (keyset
  version), not a separate fetch - clients offline at revocation time are
  still safe because they cannot verify a bundle they cannot fetch.
- **Threshold**: signatures[] carries >= threshold entries; client doc
  specifies verification, this server specifies production.

## Fleet management

- **Compliance view**: dashboard of machines x (current version, published
  version, last_seen, drift). "Drift" = last_seen older than maxCacheAge
  from the client spec (default 72h) - the same constant, defined once in
  the shared schema doc.
- **Revocation of a machine**: marks machines.revoked; policy endpoint
  returns 410 so the client un-enrolls (fails closed on SSO-gated fields).
- **Device metadata**: OS, ggcode version, hostname - collected at
  enrollment, refreshed on policy fetch. Powers the "minimum client
  version" gate: bundles may declare `minClientVersion`, letting the
  control plane force upgrades by policy.

## Admin UI

Embedded SPA (same pattern as desktop/, built into the binary; no CDN
dependency - enterprises air-gap). Screens:

1. **Policies**: list/compare versions, editor with schema validation,
   publish workflow (approve, sign, staged rollout slider)
2. **Fleet**: compliance table, machine detail (version timeline)
3. **Keys**: rotation wizard, revocation, fleet-pinning status
4. **Audit**: filterable log, SIEM export (NDJSON)
5. **Telemetry** (P4): tokens/cost by user, model, repo-hash

RBAC roles map to IdP groups (SSO via the same corp OIDC the end users
hit; admins are just another audience). All mutations go through the
audit log; the log is hash-chained so truncation is detectable.

### RBAC matrix

Four roles; every API route and UI action maps to exactly one column
below (denied = not routed, so the UI never shows a button that would 403):

| Capability | Policy author | Approver | Fleet viewer | Key admin |
|---|---|---|---|---|
| Draft / edit policy | yes | no (read) | no | no |
| Approve / reject (4-eyes) | no | yes | no | no |
| Publish + rollout slider | no | yes | no | no |
| View policies / versions | yes | yes | yes | yes |
| View fleet + drift | no | yes | yes | no |
| Revoke machine | no | yes | no | no |
| Rotate / revoke signing keys | no | no | no | yes |
| View audit log | yes | yes | yes | yes |

Separation of duties: author cannot approve (enforced server-side,
matching the approvals table's reviewer != created_by check); key admin
touches keys only - keys and policy are different blast radii, and the
same person holding both could sign anything they authored.

### Audit event types

Every mutation appends one row; `action` values (extend as routes are
added - the enum lives in one place, validated on write):

- `policy.draft.save`, `policy.submit_review`, `policy.approve`,
  `policy.reject` (with approver note)
- `policy.publish` (version, rollout_pct, bundle hash)
- `policy.rollout.update` (canary percentage changes)
- `key.generate`, `key.rotate` (grace window start/end),
  `key.retire`, `key.revoke`
- `device.enroll.approve`, `device.revoke`, `device.tags.update`
- `admin.role.grant`, `admin.role.revoke`
- `settings.update` (retention, maxCacheAge overrides)

`before`/`after` carry the affected object JSON; `sig` chains each row
to the previous (HMAC per the Data model DDL), so silent deletion or
in-place edits break verification during export or spot-check.

## Deployment & ops

- Single binary `ggcode-control`; config via env + `control.yaml`
- SQLite default (zero-dep on-prem); `DATABASE_URL` switches to Postgres
  (identical DDL, migration files shared)
- HA: 2+ replicas acceptable once Postgres is used; SSE hub fans out from
  a pub/sub table (poll-interval fallback for air-gapped LB topologies)
- Backup: the database IS the state; snapshot policy per org. Losing it
  means re-enrollment (keys are in it) - documented recovery path.
- Observability: /metrics (Prometheus), /healthz, structured logs

### Enterprise deployment profiles

How enterprises actually run this internally. The single static binary
(relay Dockerfile precedent: multi-stage, `CGO_ENABLED=0`, alpine scratch
runtime - no glibc, no external fetches at runtime) is what makes all
three profiles the same artifact:

**Profile A - Air-gapped (regulated: finance/defense/gov)**
- Deliverable: versioned bundle = OCI image tarball (`docker save`) +
  SHA256SUMS + signed Helm chart, shipped over the enterprise's approved
  file-transfer channel. Nothing phones home; the embedded SPA has zero
  CDN deps by design.
- Install: internal Harbor registry (or `docker load` on a single VM),
  internal CA-issued TLS certs, secrets from the on-prem vault
  (SignerProvider seam; master key never lives in a committed env file).
- Upgrades are pull-based events initiated by the customer: new bundle
  → `helm upgrade` / compose pull. Version pinning is theirs; the
  changelog + migration notes ship inside the bundle.

**Profile B - Kubernetes + internal registry (the default enterprise shape)**
- Helm chart in-repo (`deploy/chart/`), values mirror-able: image from
  internal Harbor, Postgres via operator or platform DB service,
  Ingress with corp TLS, HPA 2+ replicas (Postgres mode), PDB.
- GitOps friendly: chart renders declaratively; the policy DB stays the
  only stateful thing, so DR = database snapshot + re-deploy.
- LLM gateway sits in the same cluster or an egress-gateway segment;
  clients reach it over the corp network/VPN with mTLS from the internal
  CA.

**Profile C - Single VM + docker compose (SMB / pilot)**
- compose file: control + Postgres + Caddy (TLS from internal CA or
  corp wildcard). SQLite mode acceptable below ~50 enrolled machines;
  the migration to Postgres is a documented dump/restore, not a rewrite.

**Client-side distribution (all profiles)**
- Employees install ggcode through the internal channel: MDM push
  (pkg/msi), internal installer endpoint (`ggcode-installer` already
  exists as a cmd), or a mirrored release artifact - never public
  GitHub. Enrollment (`ggcode enterprise enroll`) points at the internal
  control plane; `minClientVersion` in the policy forces upgrades via
  the same channel the fleet already uses.
- Degraded-mode is a deployment property: control plane unreachable
  (maintenance, DR) → clients keep enforcing the last cached policy
  (72h maxCacheAge), so LLM work does not stop when the control plane
  does. Only SSO-token expiry gates sessions.

**HA / DR summary**: control plane is stateless replicas + one stateful
DB; RPO = DB snapshot cadence, RTO = re-deploy time; enrollment keys
export procedure documented (losing keys = fleet re-enrollment, which
is the documented worst case).

## Rollout plan (mirrors client phases)

- **CP-1** (with client P1): policy CRUD + approval flow + signing +
  `/v1/policy` pull + enrollment device-code flow + minimal audit.
  Fleet UI is a table; no SPA yet - CLI-first (`ggcode-control policy
  publish`) is acceptable for the first enterprise design partner.
- **CP-2** (with client P2): SSE push, staged rollout, fleet dashboard.
- **CP-3** (with client P3): admin SSO/RBAC, key rotation wizard, mTLS.
- **CP-4** (with client P4): telemetry ingestion + reporting screens,
  SIEM export hardening.

## Open questions

1. Multi-tenant hosting (ggcode-hosted SaaS vs always self-host)? v1 is
   self-host single-org; the org_id column and per-org key isolation are
   the seams.
2. Policy-as-code import (git repo as source of truth, server only signs)?
   Natural enterprise ask; would reuse the approval flow as the sync gate.
3. Pricing/packaging (bundled with relay? separate SKU?) - business, not
   engineering; flagging so the module boundary stays clean either way.
