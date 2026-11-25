/**
 * ggcode download mirror - a Cloudflare Worker on download.ggcode.dev.
 *
 * Stable, zero-maintenance mirror of the GitHub release assets of
 * topcheer/ggcode: nothing is published per release. The Worker resolves
 * tags on demand and streams assets through the edge cache.
 *
 *   GET /healthz                      -> 200 ok
 *   GET /                             -> index page (latest version + files)
 *   GET /latest/manifest.json         -> {"version","source","files":[{name,size,url}]}
 *                                        (built from the GitHub API, cached 1h)
 *   GET /latest/checksums.txt         -> checksums.txt of the latest tag
 *   GET /latest/<asset>               -> 302 /vX.Y.Z/<asset>
 *   GET /vX.Y.Z/manifest.json         -> manifest of that tag (cached)
 *   GET /vX.Y.Z/checksums.txt         -> proxied, cached (immutable per tag)
 *   GET /vX.Y.Z/<asset>               -> streamed from GitHub, cached immutable;
 *                                        Range requests pass through (206)
 *
 * Only the allow-listed repository's release paths are served - this is not
 * an open proxy.
 */

import { buildManifest, buildManifestFromChecksums, isValidAssetName, isValidTag, parseChecksums, parseTagFromLocation } from "./lib";

const GITHUB = "https://github.com";
const RELEASE_API = "https://api.github.com/repos";
const LATEST_TAG_TTL = 600; // seconds: the "latest" pointer
const MANIFEST_TTL = 3600; // seconds: asset list per tag
const ASSET_TTL = 31536000; // seconds: tagged assets never change

interface Env {
  REPO?: string; // default topcheer/ggcode
  GITHUB_TOKEN?: string; // optional: raises the API rate limit for manifests
}

export default {
  async fetch(request: Request, env: Env, ctx: ExecutionContext): Promise<Response> {
    const url = new URL(request.url);
    const repo = (env.REPO || "topcheer/ggcode").replace(/\/+$/, "");
    if (!/^[A-Za-z0-9_.-]+\/[A-Za-z0-9_.-]+$/.test(repo)) {
      return new Response("misconfigured REPO", { status: 500 });
    }

    if (request.method !== "GET" && request.method !== "HEAD") {
      return new Response("method not allowed", { status: 405, headers: { allow: "GET, HEAD" } });
    }

    const path = url.pathname.replace(/\/+$/, "");
    const cache = caches.default;

    // ---- well-known, non-versioned paths ---------------------------------
    if (path === "/healthz") {
      return new Response("ok\n", { headers: { "content-type": "text/plain; charset=utf-8", "cache-control": "no-store" } });
    }

    if (path === "/latest/manifest.json" || path === "/latest/checksums.txt" || (path.startsWith("/latest/") && path.split("/").length === 3)) {
      const tag = await resolveLatestTag(cache, repo);
      if (!tag) {
        return new Response("could not resolve the latest release (github.com unreachable or rate limited); retry later\n", {
          status: 502,
          headers: { "cache-control": "no-store", "content-type": "text/plain; charset=utf-8" },
        });
      }
      const rest = path.slice("/latest".length) || "/manifest.json";
      // The heavy object is always fetched under its versioned URL so the
      // immutable cache is shared between /latest/... and /vX.Y.Z/... hits.
      const target = new URL(`/${tag}${rest}`, url.origin);
      if (rest.startsWith("/manifest.json") || rest.startsWith("/checksums.txt") || request.method === "HEAD") {
        return handleVersioned(new Request(target, request), cache, repo, env, ctx);
      }
      // Assets: redirect so the client fetches the immutable URL itself
      // (curl -L / Invoke-WebRequest follow redirects; the installers use -L).
      return new Response(null, { status: 302, headers: { location: target.pathname + url.search, "cache-control": `public, max-age=60` } });
    }

    if (path === "/" || path === "") {
      return indexPage(cache, request, repo, env, ctx);
    }

    // ---- versioned paths /vX.Y.Z/<rest> ------------------------------------
    const m = path.match(/^\/(v\d+\.\d+\.\d+)(\/.*)?$/);
    if (!m) {
      return new Response("not found\n", { status: 404, headers: { "content-type": "text/plain; charset=utf-8", "cache-control": "no-store" } });
    }
    if (!m[2] || m[2] === "/") {
      return new Response(null, { status: 302, headers: { location: `/` } });
    }
    return handleVersioned(request, cache, repo, env, ctx);
  },
} satisfies ExportedHandler<Env>;

// ---------------------------------------------------------------------------
// Handlers
// ---------------------------------------------------------------------------

async function handleVersioned(
  request: Request,
  cache: Cache,
  repo: string,
  env: Env,
  ctx: ExecutionContext
): Promise<Response> {
  const url = new URL(request.url);
  const [, tag, rest] = url.pathname.match(/^\/(v\d+\.\d+\.\d+)(\/.*)?$/) ?? [];
  const name = (rest ?? "").replace(/^\//, "");
  if (!isValidTag(tag) || !isValidAssetName(name)) {
    return new Response("not found\n", { status: 404, headers: { "content-type": "text/plain; charset=utf-8" } });
  }

  if (name === "manifest.json") {
    return manifestFor(tag, request, cache, repo, env, ctx);
  }

  // ---- checksums.txt and release assets: edge-cached, immutable ---------
  const upstream = `${GITHUB}/${repo}/releases/download/${tag}/${name}`;
  const cacheKey = new Request(`${url.origin}/${tag}/${name}`);
  const isRange = request.headers.has("range");

  if (!isRange) {
    const cached = await cache.match(cacheKey);
    if (cached) {
      return withCors(cached.clone(), true);
    }
  }

  const upstreamRequest = new Request(upstream, {
    method: request.method,
    headers: { "user-agent": "ggcode-download-mirror (+https://download.ggcode.dev)", ...(isRange ? { range: request.headers.get("range")! } : {}) },
    redirect: "follow",
  });
  let upstreamResponse: Response;
  try {
    upstreamResponse = await fetch(upstreamRequest);
  } catch (err) {
    // Network failure: fall back to a stale cache entry if we have one.
    const stale = await cache.match(cacheKey);
    if (stale) return withCors(stale.clone(), true);
    return new Response(`upstream fetch failed for ${upstream}: ${String(err)}\n`, { status: 502, headers: { "cache-control": "no-store" } });
  }

  const isPartial = upstreamResponse.status === 206;
  if (upstreamResponse.status === 200 || isPartial) {
    const headers = new Headers(upstreamResponse.headers);
    headers.set("access-control-allow-origin", "*");
    headers.set("x-ggcode-source", upstream);
    if (!isPartial) {
      headers.set("cache-control", `public, max-age=${ASSET_TTL}, immutable`);
      headers.set("content-disposition", `attachment; filename="${name}"`);
    } else {
      headers.set("cache-control", "private, no-store");
    }
    const response = new Response(upstreamResponse.body, { status: upstreamResponse.status, headers });
    if (!isPartial && request.method !== "HEAD") {
      ctx.waitUntil(cache.put(cacheKey, response.clone()));
    }
    return response;
  }

  // 404 / errors from GitHub are passed through but never cached.
  const headers = new Headers({ "content-type": "text/plain; charset=utf-8", "cache-control": "no-store" });
  return new Response(`${name} not found in ${repo}@${tag} (github ${upstreamResponse.status})\n`, { status: upstreamResponse.status === 404 ? 404 : 502, headers });
}

async function manifestFor(
  tag: string,
  request: Request,
  cache: Cache,
  repo: string,
  env: Env,
  ctx: ExecutionContext
): Promise<Response> {
  const url = new URL(request.url);
  const origin = url.origin;
  const cacheKey = new Request(`${origin}/${tag}/manifest.json`);
  const cached = await cache.match(cacheKey);
  if (cached) {
    return withCors(cached.clone(), true);
  }
  const checksums = await fetchText(`${GITHUB}/${repo}/releases/download/${tag}/checksums.txt`);
  let body: string | undefined;
  let source: string | undefined;
  if (checksums !== null) {
    const entries = parseChecksums(checksums);
    if (entries.length > 0) {
      body = JSON.stringify(buildManifestFromChecksums(tag, entries, origin), null, 2) + "\n";
      source = `${GITHUB}/${repo}/releases/download/${tag}/checksums.txt`;
    }
  }
  if (body === undefined) {
    // No checksums (should not happen for a GoReleaser release): fall back to
    // the GitHub API, which is rate limited per egress IP, only with a token.
    if (!env.GITHUB_TOKEN) {
      return new Response(`manifest for ${tag} unavailable (checksums.txt missing and no GITHUB_TOKEN for the GitHub API)\n`, {
        status: 502,
        headers: { "cache-control": "no-store", "content-type": "text/plain; charset=utf-8" },
      });
    }
    const api = await fetch(`${RELEASE_API}/${repo}/releases/tags/${tag}`, {
      headers: { accept: "application/vnd.github+json", authorization: `Bearer ${env.GITHUB_TOKEN}`, "user-agent": "ggcode-download-mirror (+https://download.ggcode.dev)" },
    });
    if (api.status === 404) {
      return new Response(`release ${tag} not found\n`, { status: 404, headers: { "cache-control": "public, max-age=60" } });
    }
    if (!api.ok) {
      return new Response(`github api ${api.status}: ${(await api.text()).slice(0, 300)}\n`, {
        status: 502,
        headers: { "cache-control": "no-store", "content-type": "text/plain; charset=utf-8" },
      });
    }
    const release = (await api.json()) as { assets?: Array<{ name: string; size: number; browser_download_url: string }> };
    body = JSON.stringify(buildManifest(tag, release.assets ?? [], origin), null, 2) + "\n";
    source = `${RELEASE_API}/${repo}/releases/tags/${tag}`;
  }
  if (body === undefined || source === undefined) {
    return new Response(`manifest for ${tag} unavailable\n`, { status: 502, headers: { "cache-control": "no-store" } });
  }
  const response = new Response(body, {
    headers: {
      "content-type": "application/json; charset=utf-8",
      "cache-control": `public, max-age=${MANIFEST_TTL}`,
      "access-control-allow-origin": "*",
      "x-ggcode-source": source,
    },
  });
  ctx.waitUntil(cache.put(cacheKey, response.clone()));
  return response;
}

/** Fetch a small immutable text from GitHub (follows the release redirect); null on failure. */
async function fetchText(upstream: string): Promise<string | null> {
  try {
    const r = await fetch(upstream, {
      headers: { "user-agent": "ggcode-download-mirror (+https://download.ggcode.dev)" },
      redirect: "follow",
    });
    if (!r.ok) return null;
    return await r.text();
  } catch (err) {
    console.warn(`fetch failed for ${upstream}: ${String(err)}`);
    return null;
  }
}

async function resolveLatestTag(cache: Cache, repo: string): Promise<string | null> {
  const cacheKey = new Request(`https://download.ggcode.dev/__internal/latest-tag`);
  const cached = await cache.match(cacheKey);
  if (cached) {
    const tag = (await cached.text()).trim();
    if (isValidTag(tag)) return tag;
  }
  try {
    // The releases/latest redirect carries the tag in its Location; no API
    // call, so no rate limit is burned for the "latest" pointer.
    const r = await fetch(`${GITHUB}/${repo}/releases/latest`, {
      method: "HEAD",
      redirect: "manual",
      headers: { "user-agent": "ggcode-download-mirror (+https://download.ggcode.dev)" },
    });
    const tag = parseTagFromLocation(r.headers.get("location") ?? "");
    if (tag) {
      await cache.put(cacheKey, new Response(tag, { headers: { "cache-control": `public, max-age=${LATEST_TAG_TTL}` } }));
      return tag;
    }
  } catch (err) {
    // github.com unreachable: report "no tag" and let the caller decide (502 / index shows unavailable)
    console.warn(`latest tag resolution failed: ${String(err)}`);
  }
  return null;
}

async function indexPage(cache: Cache, request: Request, repo: string, env: Env, ctx: ExecutionContext): Promise<Response> {
  const tag = await resolveLatestTag(cache, repo);
  const html = `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>ggcode downloads</title>
<style>body{font-family:system-ui,sans-serif;margin:2rem auto;max-width:56rem;padding:0 1rem}code{background:#f2f2f2;padding:.1rem .3rem;border-radius:.25rem}li{margin:.4rem 0}</style></head>
<body><h1>ggcode downloads</h1>
<p>Latest release: <code>${tag ?? "(unavailable)"}</code></p>
<ul>
<li><code>curl -fsSL https://ggcode.dev/install.sh | bash</code></li>
<li>Latest manifest: <a href="/latest/manifest.json">/latest/manifest.json</a></li>
<li>Latest checksums: <a href="/latest/checksums.txt">/latest/checksums.txt</a></li>
<li>Versioned downloads: <code>/vX.Y.Z/&lt;asset&gt;</code> (immutable, edge-cached)</li>
</ul>
<p>Assets are mirrored from <a href="https://github.com/${repo}/releases">github.com/${repo}/releases</a>.</p>
</body></html>`;
  return new Response(html, { headers: { "content-type": "text/html; charset=utf-8", "cache-control": "public, max-age=60" } });
}

function withCors(response: Response, _fromCache: boolean): Response {
  const headers = new Headers(response.headers);
  headers.set("access-control-allow-origin", "*");
  return new Response(response.body, { status: response.status, headers });
}
