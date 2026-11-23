# Website Redesign — Design

Feature: `site-redesign` · Depends on: requirements.md (D1–D6 decided as recommended) · Status: DRAFT

## 1. Stack decision (D1–D6)

| Decision | Choice | Why |
|---|---|---|
| D1 Stack | **Astro 5, `output: 'static'`** inside `docs/site/` | Components without shipping a framework; `astro:assets` gives AVIF/WebP + `srcset` + intrinsic sizes for free (R1.3, R6 CLS); islands let the hero be the *only* hydrated client code; static `dist/` keeps the Pages deploy a copy step (R7) |
| D2 3D | **three.js (vanilla, ES module) in one `<script>` island** | ~150 KB gz core, no React runtime; full control over render loop, DPR cap, visibility pause (R3) |
| D3 Motion | **GSAP + ScrollTrigger** (pinned/scroll-linked) + CSS keyframes for ambient + a tiny IO helper for reveals | ScrollTrigger handles pinning/progress/scrub robustly across mobile; CSS is cheapest for loops |
| D4 Hero subject | **Particle field forming `>_`, morphing into a lattice cube on scroll** | Reads as "terminal → structure"; particles are a single `Points` draw call (cheap); morph is a shader uniform lerp (R3.1) |
| D5 i18n | **Astro i18n routing: `/` (en) and `/zh/`**, strings in `src/i18n/{en,zh}.json` | Static localized HTML for SEO; keep a `localStorage` memory of last choice but navigation is real routes (R5) |
| D6 Fonts | **Self-hosted woff2 subsets** via `@fontsource` (Space Grotesk, JetBrains Mono) | Removes third-party render-blocking request; `font-display: swap` (R6) |

Rejected: zero-build vanilla (no image pipeline, hand-maintained vendored libs, 9 copy-pasted cards); Vite vanilla TS (no SSG for legal/i18n pages, hand-rolled bundling); R3F (ships React for one scene); Spline (external iframe, perf/privacy, no offline build).

## 2. Repository layout

Keep everything under `docs/site/` so the `pages-deploy.yml` `paths: docs/site/**` trigger stays valid.

```
docs/site/
  package.json            # astro, gsap, three, @astrojs/sitemap, @fontsource/*, sharp (opt), typescript
  astro.config.mjs        # output static, i18n { defaultLocale: 'en', locales: ['en','zh'] }, site: 'https://ggcode.dev', compressHTML
  tsconfig.json
  public/
    favicon.svg  robots.txt  install.sh  install.ps1   # unchanged binaries/scripts
    og/{en,zh}.jpg                                      # generated, committed
  src/
    styles/tokens.css     # existing palette promoted to tokens + spacing/type/motion scales
    styles/global.css     # reset, typography, utilities, reduced-motion base
    i18n/{en,zh}.json     # migrated from script.js STRINGS (53 keys) + new copy
    i18n/utils.ts         # t(), localeFromUrl(), localizedPath()
    layouts/Base.astro    # <head> (meta/OG/hreflang/JSON-LD/Plausible), nav, footer, lang switch
    components/
      Nav.astro  Hero.astro  Hero3D.astro        # island: client:visible
      TerminalPlayer.astro                      # typed-out TUI session (pure CSS/JS, no video dependency)
      Proof.astro  ProductTabs.astro  BentoGrid.astro
      HowItWorks.astro                           # pinned scroll story (GSAP)
      Ecosystem.astro                            # SVG schematic, animated nodes
      InstallTabs.astro  Cta.astro  Footer.astro  Reveal.astro
      media/VideoLoop.astro  media/Picture.astro
    pages/
      index.astro  zh/index.astro                 # same components, locale prop
      privacy.astro  terms.astro  zh/privacy.astro  zh/terms.astro (or keep .html URLs via redirects—see §8)
    content/                                     # front-matter-free; copy lives in i18n JSON
    assets/
      screenshots/{tui,desktop,mobile}/*.png     # source images (astro:assets optimizes)
      video/{tui,desktop,mobile}.{mp4,webm}      # transcoded from docs/marketing/videos
      video/posters/*.jpg
      illustrations/*.svg                         # feature mini-UIs, ecosystem schematic
  Dockerfile              # multi-stage: node build → caddy serve dist/
  Caddyfile               # unchanged headers; root → dist
  CAPTURE.md              # screenshot/recording procedure (R8)
```

## 3. Information architecture & sections

| # | Section | Content | Motion | Visuals |
|---|---|---|---|---|
| 1 | **Hero** | badge, H1, sub, install command (copy), primary/secondary CTA | 3D particle `>_`→cube, pointer tilt, parallax on scroll (desktop) | 3D canvas + terminal still as poster |
| 2 | **Proof** | 3 real metrics (platforms count, install time, GitHub stars fetched at build via env var, no client fetch) | count-up once | typographic |
| 3 | **Product** | tabs TUI / Desktop / Mobile; each: headline, 3 bullets, media | tab crossfade, video plays when tab active & in view | real demo videos (lazy, poster) |
| 4 | **Features** | bento grid 6 cards (toolkit, MCP, multi-agent, IM/LAN, permission modes, worktrees) | stagger reveal; hover tilt on cards (≤ 3°) | mini UI SVG/PNG per card |
| 5 | **How it works** | 3 steps: Open in your repo → Agent plans & edits in isolated worktree → Review, test, ship | pinned, steps progress with scroll (mobile: stacked) | step illustrations |
| 6 | **Ecosystem** | MCP / multi-agent / LAN chat schematic | animated data-flow lines (CSS `stroke-dashoffset`) | SVG schematic |
| 7 | **Install** | OS-detected tabs (macOS/Linux, Windows, Homebrew, npm, iOS, Android) | copy micro-animation | OS glyphs |
| 8 | **CTA / Footer** | one-line CTA, links, language switch, Plausible | none | — |

Legal pages: plain `Base.astro` layout with prose styles; existing URLs preserved (see §8).

## 4. Design tokens (extend, don't replace)

Keep the existing palette so brand continuity holds:
`--bg #0a0a0f`, `--bg-surface #12121a`, `--bg-card #16161f`, `--border #1e1e2a`, `--accent #7c6cff`, `--success #34d399`, fonts Space Grotesk / JetBrains Mono.

Add: `--accent-2 #4fd1ff`, `--accent-3 #ff6cf2` (already used by orbs → promoted to tokens); spacing scale 4/8/12/16/24/32/48/64/96; radii 8/14/24; type scale 12/14/16/20/24/32/48/64 (fluid `clamp()`); motion tokens `--dur-1 150ms · --dur-2 300ms · --dur-3 600ms`, `--ease-out cubic-bezier(.2,.8,.2,1)`, `--ease-in-out cubic-bezier(.65,0,.35,1)`. Raise `--text-muted` to `#8a8ab5` (≥ 5.2:1) for AA on card surfaces.

## 5. Key components

### Hero3D.astro (island, `client:visible`)
- Scene: ~6k particles (`THREE.Points`, additive blending, accent gradient via vertex color), arranged on a `>_` glyph (sampled from a canvas-drawn text mask) → morph target positions forming a wireframe cube's edges (and back on pointer proximity). Uniform `uMorph` driven by scroll progress (ScrollTrigger) and pointer tilt (damped lerp).
- Render: `WebGLRenderer({ antialias: true, alpha: true, powerPreference: 'high-performance' })`, DPR `min(devicePixelRatio, 2)`, `setAnimationLoop` paused via `IntersectionObserver` + `visibilitychange`.
- Fallback ladder: no WebGL / context lost / `deviceMemory ≤ 2` / `Save-Data` / reduced-motion → render `public/og/hero-poster.webp` (pre-rendered frame from the same scene via headless capture in build), never import three.
- Bundle: `three` imported from `three/src/Three.Core.js` subset (Points, ShaderMaterial, PerspectiveCamera) → target ≤ 180 KB gz.

### TerminalPlayer.astro
CSS-driven typed-out session (reuses today's terminal markup) — deterministic, no video needed for the hero; plays once on view.

### VideoLoop.astro
`<video muted playsinline loop preload="none" poster>`; `src` set from `data-src` when tab active AND in view; `autoplay` attempted; on `play()` rejection (autoplay policy) show poster + play button. Sources: MP4 (H.264, yuv420p, faststart) + WebM (VP9).

### HowItWorks.astro
GSAP ScrollTrigger pin (`pin: true, scrub`) on ≥ 1024 px; `matchMedia` to stacked cards below; `prefers-reduced-motion` → no pin.

### InstallTabs.astro
OS detection via `navigator.userAgentData?.platform ?? navigator.userAgent`; copy via `navigator.clipboard` with fallback; micro-animation via CSS class toggle.

### i18n
`src/i18n/{en,zh}.json`; `Base.astro` emits `<html lang>`, `hreflang` alternates, localized `og:locale`; `LangSwitch.astro` links `/` ↔ `/zh/` and remembers choice in `localStorage("ggcode-lang")` to pre-suggest (never redirect bots).

## 6. Image & media pipeline

- **Source capture (R8, `CAPTURE.md`)**: TUI screenshots via `asciinema`/`script` + `vhs` or headless `ggcode` run in `browser`/`desktop_control` tooling at 1440×900 @2x; desktop app via `desktop_control` screenshot of the Wails app; mobile via `mobile_device` screenshot on iOS simulator + Android emulator. Redaction checklist (paths, tokens, usernames). Target 6–10 screenshots + 3 videos.
- **Astro assets**: all raster images imported in frontmatter/`Image` component → AVIF + WebP `srcset`, `width`/`height` → zero CLS. Illustrations as inline SVG (no request).
- **Video**: transcode `docs/marketing/videos/*.mp4` with ffmpeg → `-crf 28 -preset slow -pix_fmt yuv420p -movflags +faststart`, 1280 px wide, ≤ 1.8 MB; poster JPG from first frame; WebM VP9 at crf 36.
- **OG**: generated at build (`scripts/og.mjs` with `satori` or pre-rendered committed JPG) at 1200×630 ≤ 300 KB, per locale.

## 7. Motion system

- **Ambient (CSS)**: orbs (existing `float`), gradient text shift, subtle grain overlay via SVG `feTurbulence` (static, no animation).
- **Reveal**: `Reveal.astro` wrapper with `data-reveal`; one IntersectionObserver adds `.is-visible`; stagger via `--i` index; once only.
- **Scroll-linked (GSAP)**: hero parallax, HowItWorks pin, Proof count-up, ecosystem line draw.
- **Micro**: copy button check, tab underline slide, card hover tilt (transform only, `will-change` on hover only).
- **Rules**: transform/opacity only; no layout-thrashing reads in rAF; all GSAP effects registered inside `matchMedia`; `ScrollTrigger.refresh()` after fonts load.
- **Reduced motion**: global `@media (prefers-reduced-motion: reduce) { *, *::before, *::after { animation: none !important; transition: none !important } }` + JS guard that skips GSAP and three entirely.

## 8. Integration, SEO, deploy, errors

- **URL continuity**: today `privacy.html` / `terms.html`. Astro emits `privacy/index.html` style by default; set `build.format: 'file'` so output is `privacy.html` — URLs unchanged, sitemap unchanged. Redirects file not needed.
- **SEO**: `@astrojs/sitemap` with i18n alternates; JSON-LD `SoftwareApplication` (operatingSystem: macOS, Linux, Windows, iOS, Android; offers price 0; featureList from i18n); canonical; `og:image` absolute per locale.
- **Analytics**: keep Plausible snippet in `Base.astro` unchanged.
- **Deploy**: `pages-deploy.yml` — add `actions/setup-node@v4` (Node 22 LTS) + `npm ci` + `npm run build` in `docs/site`, stage `dist/`. `Dockerfile` → `node:22-alpine` build stage + `caddy:2.10-alpine` serving `dist/`; drop the `__SITE_ASSET_VERSION__` sed (hashed names). `site-release.yml` — same build command.
- **CI**: add `docs/site` job to lint/verify: `npm run build` + `astro check` + `npm run lint:links` (custom script: crawl `dist/` for internal `href`/`src` 404s). Pre-commit Go hooks unaffected; add `npm` files to Go-hook ignore if needed.
- **Error handling**: WebGL fail → poster; context lost → dispose & poster; video autoplay blocked → poster + button; JS disabled → all content visible (reveals default to visible without `.js` class; terminal static; 3D poster); fonts fail → system stack fallback; broken image → build error.
- **Legal pages**: migrate content verbatim into Astro pages with `prose` styles; bilingual `/zh/privacy` optional (not required by R5; mark as stretch).

## 9. Performance budgets & measurement

| Metric | Budget | How measured |
|---|---|---|
| LCP (mobile) | ≤ 2.5 s | Lighthouse CI in PR workflow (`lighthouserc.json`, staticDistPath `docs/site/dist`) |
| CLS / INP | ≤ 0.1 / ≤ 200 ms | Lighthouse CI |
| Initial JS (non-3D) | ≤ 120 KB gz | `astro build` output + `bundlesize` |
| 3D chunk | ≤ 180 KB gz | same |
| Initial CSS | ≤ 60 KB gz | same |
| Page weight (first load) | ≤ 1.2 MB incl. poster | Lighthouse |
| Lighthouse scores | P ≥ 90, A ≥ 95, BP ≥ 95, SEO ≥ 95 | CI assertion |

Manual verification matrix: Chrome/Safari/Firefox desktop, iOS Safari + Android Chrome, 375/768/1024/1440 px, reduced-motion ON, JS OFF, WebGL OFF (via `--disable-webgl`), keyboard-only pass, `axe-core` via Lighthouse.

## 10. Risks & mitigations

| Risk | Mitigation |
|---|---|
| 3D hurts LCP / battery | island `client:visible`, loads after LCP via `requestIdleCallback`; pauses off-screen; DPR cap; fallback ladder |
| GSAP pin jank on mobile | `matchMedia` ≥ 1024 px only; stacked fallback |
| Node toolchain in a Go repo | confined to `docs/site/`; CI uses setup-node; Docker multi-stage; `node_modules` ignored |
| Astro i18n URL change for zh | `/` stays English default (today's default); `/zh/` is additive; add `hreflang`; no redirects needed |
| Video autoplay policies | muted+playsinline; poster-first; play button fallback |
| Content drift (screenshots stale) | `CAPTURE.md` + assets in repo; refresh checklist in release notes template |
| Scope creep | Non-goals §; stretch items (zh legal pages, ecosystem animation polish) explicitly marked |

## 11. Phasing (maps to tasks.md)

0. Decide & scaffold · 1. Tokens/layout/i18n migrate (content parity) · 2. Media capture + pipeline · 3. Motion system · 4. 3D hero + fallbacks · 5. SEO/deploy/CI · 6. Perf/a11y QA · 7. Launch & measure.
