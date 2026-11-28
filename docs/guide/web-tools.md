# Web tools: `web_search` and `web_fetch`

The built-in web tools are scrapers, not APIs: `web_search` reads a search
engine's result page and `web_fetch` downloads a page and reduces it to text.
Both run on the user's machine, so they see the same network the user sees -
corporate proxies, national firewalls, shared IPs that engines rate-limit.
The design goal is therefore not "one perfect backend" but **fail visibly and
fall back**:

- `web_search` tries several engines in order, skips engines that recently
  refused us, and picks the answer whose results actually match the query.
- `web_fetch` sends a browser-like identity first, falls back to an honest
  tool identity when a site refuses the browser one, decodes the body's
  charset, returns non-HTML bodies verbatim and explains anti-bot walls and
  JavaScript-only pages instead of returning garbage.

Both tools log to the `websearch` / `webfetch` debug categories
(`/debug` or `GGCODE_DEBUG=1`), one line per backend or HTTP attempt.

## `web_search`

```
query ─► cache (10 min) ─► DuckDuckGo HTML ─► Bing ─► 360 Search (so.com)
                                 │               │            │
                                 └── challenge / error / unrelated results ──► next
                                 └── results match ≥40% of query terms ──────► done
```

| Mechanism | What it does | Why |
|-----------|--------------|-----|
| Backend chain | DuckDuckGo (`html.duckduckgo.com`) first, then Bing, then 360 Search. | DuckDuckGo has the best snippets but rate-limits an IP after a burst (it answers HTTP 202 with a CAPTCHA page). Bing serves unrelated "decoy" result pages to cookie-less traffic from some regions; 360 is reachable from mainland China and returns organic results for technical queries. |
| Circuit breaker | A backend that challenged/errored is skipped for 1 min, doubling to 30 min; one success resets it. | A blocked engine must not add a wasted round-trip (and another rate-limit strike) to every call. |
| Relevance gate | Each backend's results are scored by query-term coverage (title hits weigh 3x snippet hits; CJK queries use Han bigrams). A set covering ≥40% of the terms is returned immediately; otherwise the next backend is tried and the best set wins. | Filters Bing's decoy SERPs (HTTP 200, ten well-formed results, zero terms in common with the query) and oneboxes. |
| Engine-self links dropped | Results pointing into `duckduckgo.com`, `bing.com`, `so.com`, `baidu.com` are discarded. | Those are ads, image/translate oneboxes and redirect helpers, not web results. |
| Cache | The chosen raw result set is cached per query for 10 minutes (64 entries). | Agents repeat the same query while refining a prompt; each repeat would otherwise cost another rate-limited request. |
| Headers | Desktop-Chrome `User-Agent`, `Accept`, `Accept-Language` chosen from the query script (`zh-CN` for CJK). Bing additionally gets `mkt=zh-CN` for CJK queries. | Engines 403/202 non-browser agents and rank by language. |
| Domain filters | `allowed_domains` / `blocked_domains` are applied **after** fetching (3x results are fetched when a filter is present) and match the host and its subdomains. | A filter must not hide the fact that the engine answered. |

Result quality hints are appended to the output instead of hiding the
problem:

- `[search-quality] best result matches only N% of query terms` - every
  backend answered weakly; rephrase with the vocabulary the target docs use.
- `[search-degraded] web_search has returned empty results for N consecutive
  queries` - after 3 backend-level empties the engines are probably
  rate-limiting; switch to `web_fetch` on known URLs.
- `Search backends are rate-limited right now` - every configured backend is
  in circuit-breaker cooldown.

The scrape is intentionally tolerant: each parser keys on the stable result
container (`div.result` / `li.b_algo` / `li.res-list`), unwraps click-tracking
redirects (`bing.com/ck/a?u=a1<base64url>`, `data-mdurl` on 360), and treats
an empty parse as "this engine did not answer", not as an error.

## `web_fetch`

```
GET (browser identity) ──► 403/406? ──► GET again with the plain tool identity (remembered per host)
        │
        ├─ non-200 → error with status, final URL, stripped body, and a [blocked] note when it is an anti-bot wall
        ├─ text/html → charset decode → readable text (main content, <pre> kept verbatim) → 20000-char cap
        ├─ text/*, JSON, XML, YAML, … → charset decode → verbatim (never HTML-stripped) → 20000-char cap
        └─ PDF / images / binaries → error naming the content type and suggesting run_command + read_file
```

| Behaviour | Detail |
|-----------|--------|
| Two identities | First a desktop-Chrome `User-Agent` with `Accept`/`Accept-Language` (most origins refuse non-browser agents). If that is refused with 403/406, one retry with `ggcode/1.0 (web fetch tool; +https://github.com/topcheer/ggcode)` - some WAFs (w3.org) 403 a "Chrome" UA that does not arrive with a browser TLS fingerprint but let an honest tool UA through. The identity that worked is remembered per host for 10 minutes. |
| Charset | A declared `charset` wins; otherwise valid UTF-8 passes through; otherwise `golang.org/x/net/html/charset` sniffs the BOM / `<meta charset>`. When even that is uncertain the body is tried as GBK and kept if it decodes to mostly Han characters - many Chinese sites still serve GBK/GB2312. Invalid UTF-8 is replaced, never passed through. |
| Non-HTML bodies | `application/json`, `text/plain`, `text/markdown`, XML feeds, YAML and friends are returned byte-for-byte. Running the HTML stripper on them destroyed `<` in code, generics and markup. |
| Main content | All `<article>` blocks are joined (index pages used to lose every article after the first), falling back to `<main>`, `role="main"` and common content ids. |
| `<pre>` blocks | Set aside before stripping and restored verbatim so code indentation survives. |
| JavaScript-only pages | When a 200 HTML page yields fewer than 300 characters and carries SPA markers (`id="root"`, `id="__next"`, `<noscript>`, "requires JavaScript"), the result says so and points at the `browser` tool. |
| Anti-bot walls | 403/429/503/202 with Cloudflare "Just a moment", DuckDuckGo "anomaly", captcha or "unusual traffic" markers get a `[blocked]` note instead of being presented as page content. |
| Transient faults | One retry on connection reset / EOF / TLS handshake timeout; never on a client timeout or after the caller cancelled. |
| Timeouts | The agent's adaptive deadline governs (web category: 120 s, growing with observed latency). Only calls without a deadline use a fixed 45 s client timeout. |
| SSRF guard | Unchanged: private/internal addresses are rejected on the initial URL, on redirects and at dial time (DNS answers are checked, the connection is pinned to a public IP). Only `http`/`https` URLs are accepted; the tool is registered with the guard always on. |

## When to use what

- **Looking for URLs**: `web_search`, then `web_fetch` the best hits.
- **Known URL**: `web_fetch` directly.
- **Login walls, JavaScript apps, anti-bot challenges**: the `browser` tool
  (real Chrome) - `web_fetch` will tell you when it hits one of these.
- **PDFs / Office documents**: download with `run_command` (`curl -L -o file
  URL`) and read with `read_file`, which parses them.

## Live health check

```bash
GGCODE_LIVE_WEB=1 go test -tags goolm -run TestLiveWebProbe -v ./internal/tool/     # search + fetch probes
GGCODE_LIVE_WEB=fetch go test -tags goolm -run TestLiveWebProbe -v ./internal/tool/ # fetch only
```

The probe prints one line per query/URL (latency, result count, first hit)
and fails only when a tool returns an error or zero results; third-party
ranking is reported, not asserted.

## Related

- `docs/design/p2p-direct-connection.md` - the mobile share path; the
  `Relay`/`P2P` status pill is unrelated to these tools.
- Provider-hosted search (`web_search` on Anthropic / OpenAI / Gemini) is a
  different feature: `docs/guide/providers.md`.
