# ggcode download mirror (`download.ggcode.dev`)

A Cloudflare Worker that mirrors the GitHub release assets of `topcheer/ggcode` on
**https://download.ggcode.dev**. It is stable and needs **nothing per release**: no
publishing step, no copied assets. The Worker resolves tags on demand, streams the
assets from GitHub and caches them at the edge.

## URL scheme

| Path | Behaviour |
| --- | --- |
| `/healthz` | `200 ok` |
| `/` | index page with the latest tag |
| `/latest/manifest.json` | `{"version","source","files":[{name,url,sha256,...}]}` built from the tag's `checksums.txt` (no GitHub API call), cached 1 h |
| `/latest/checksums.txt` | `checksums.txt` of the latest tag |
| `/latest/<asset>` | `302` to `/vX.Y.Z/<asset>` |
| `/vX.Y.Z/<asset>` | streamed from GitHub, `Cache-Control: public, max-age=31536000, immutable`, edge-cached; `Range` requests pass through (`206`) |

Only release paths of the configured repository are served (`REPO` var, default
`topcheer/ggcode`); tag and asset names are validated strictly. It is not an open proxy.

## Why this design

- **Zero maintenance**: the release workflow does not touch the mirror. GitHub
  `releases/latest` (HEAD redirect) resolves the tag, `checksums.txt` lists the assets.
- **No GitHub API dependency**: the manifest is built from `checksums.txt`. The
  unauthenticated GitHub API is rate limited per egress IP and Cloudflare Workers share
  egress IPs, so an API-based manifest failed with `403 rate limit exceeded` on the first
  deploy. `GITHUB_TOKEN` (optional secret) is only used as a last-resort fallback.
- **Fast and resilient**: immutable assets are cached in the Cloudflare edge cache
  (`cf-cache-status: HIT` on the second fetch); when GitHub is slow or unreachable the
  cached copy still serves. `Range` requests are proxied without caching.
- **Fallbacks in the installers**: `install.sh` / `install.ps1` use
  `https://download.ggcode.dev/latest` as the download base and fall back to GitHub
  for every step.

## Cloudflare policy

Checked against Cloudflare's Service-Specific Terms (2026-09-28):

- Developer Platform terms: Workers may proxy third-party content; obligations are to
  follow the developer docs and third-party terms (public GitHub release assets) and
  Cloudflare may limit usage that puts an undue burden on the network.
- CDN terms (Free/Pro/Business): large files must be served through the listed paid
  services (Developer Platform, Images, Stream) rather than the plain CDN; Workers is the
  Developer Platform, so the assets are served by the Worker (edge cache via the Cache
  API), not by origin caching of a Web host.

The Worker runs on the account's existing (free) Workers plan: streaming costs little
CPU and the free tier allows 100k requests/day. **Follow-up for the owner**: if download
volume grows, move the Worker to the Workers Paid plan ($5/month) for CPU/requests
headroom - a billing decision that is deliberately not automated.

## Develop

```bash
cd workers/download-mirror
npm install
npm run check      # tsc + vitest
npx wrangler deploy # needs CLOUDFLARE_API_TOKEN (+ CLOUDFLARE_ACCOUNT_ID) or wrangler login
```

`.github/workflows/deploy-download-mirror.yml` deploys on pushes to `main` that touch
this directory when the repository secrets `CLOUDFLARE_API_TOKEN` and
`CLOUDFLARE_ACCOUNT_ID` are set (one-time setup: create an API token with
`Workers Scripts: Edit` + `Workers Routes: Edit` + `Zone DNS: Edit` for the `ggcode.dev`
zone, then `gh secret set CLOUDFLARE_API_TOKEN` and `gh secret set CLOUDFLARE_ACCOUNT_ID`).
Without the secrets the job is skipped and the deploy has to be run manually.
