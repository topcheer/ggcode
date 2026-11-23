# Website Redesign — Requirements

Feature: `site-redesign` · Scope: `docs/site/` (ggcode.dev) · Status: DRAFT for approval

## Current state (verified 2026-11-23)

- Plain static site: `index.html` (15.9 KB), `styles.css` (12.5 KB), `script.js` (10 KB), `privacy.html`, `terms.html`, `sitemap.xml`, `robots.txt`. Two assets: `favicon.svg`, `ggcode-banner.png` (390 KB, used as OG image too).
- Sections: hero (3 floating gradient orbs + static terminal mock), features (9 emoji cards), install (OS tabs), CTA, footer.
- Motion: CSS `float` orbs, `gradientShift`, IntersectionObserver reveal-on-scroll. **No product images, no video, no 3D.**
- i18n: hand-rolled `STRINGS` dict (53 keys, en/zh) in `script.js`, `data-i18n` attributes, `localStorage("ggcode-lang")`, client-side toggle only — one URL for both languages.
- Deploy: `.github/workflows/pages-deploy.yml` publishes `docs/site` verbatim to GitHub Pages on `docs/site/**` changes. A parallel `Dockerfile` + `Caddyfile` (Railway) and `site-release.yml` also exist.
- Unused marketing media already in repo: `docs/marketing/videos/ggcode-{tui,desktop,mobile}-demo.mp4` (1.0–1.7 MB each).
- Toolchain: Node 26 / npm 11 available locally and in CI is trivial to add; desktop app already uses React 19 + Vite 8 + TS.

## Goals

1. A landing page that **shows** the product (real TUI / desktop / mobile footage and screenshots) instead of describing it with emoji.
2. A coherent **motion system** (scroll storytelling, micro-interactions, ambient depth) that feels premium and never janky.
3. A signature **3D hero** that renders on the brand accent and degrades gracefully.
4. Zero regression in SEO, load speed, accessibility, or bilingual support — and a build path that stays a one-step deploy.

## Non-goals

- No CMS, no server-side rendering, no client framework beyond what the 3D island needs.
- No change to install scripts, pricing (there is none), docs site (`docs/`), or product.
- No paid asset services (Spline embeds, LottieFiles subscriptions); all assets generated or open-licensed.

## Personas

- **Evaluator (first visit, 30 s)** — wants to see what ggcode looks like and how it differs from a "chat wrapper".
- **Developer (ready to install)** — wants the right command for their OS in one click.
- **Chinese-reading developer** — expects a native `/zh/` page, not a toggled overlay.
- **Search engine / link preview** — expects per-language canonical URLs, OG images, structured data.

## User stories & acceptance criteria (EARS)

### R1 — Show the product ("多图")

**US-1.1 As an evaluator, I see real product visuals in the first screen and throughout the page.**
- WHEN the landing page loads THEN the hero SHALL contain a visual of the real TUI (video or screenshot) in addition to the 3D object.
- WHEN the "Product" section is reached THEN it SHALL present TUI, Desktop and Mobile as three selectable tabs, each with a real video loop (muted, autoplay, loop, playsinline) and a poster image.
- WHEN any tab is selected THEN its media SHALL start within one frame and no layout shift SHALL occur (fixed aspect-ratio box).
- WHEN the Features section is reached THEN at least 4 feature cards SHALL carry an illustrative visual (mini UI render, diff view, diagram, or screenshot) rather than an emoji alone.

**US-1.2 As a developer, I can see the feature in context, not a stock photo.**
- IF a feature card describes a workflow (multi-agent, LAN chat, worktrees, MCP) THEN its visual SHALL be a schematic built from the product's own concepts (nodes, terminals, branches), not a generic illustration.

**US-1.3 As a maintainer, images are optimized automatically.**
- WHEN any raster image is referenced from content THEN the build SHALL emit responsive `srcset` with AVIF and WebP variants and explicit `width`/`height` (no CLS).
- WHEN the OG image is regenerated THEN it SHALL be ≤ 300 KB at 1200×630 and referenced by absolute URL.

### R2 — Motion system ("多动效")

**US-2.1 As a visitor, the page responds to scroll and input with purposeful motion.**
- WHEN a section enters the viewport THEN it SHALL reveal once with a staggered fade/translate (≤ 600 ms), never re-hiding.
- WHEN the "How it works" section is scrolled through THEN its 3 steps SHALL be pinned and progressed one-by-one (scroll-linked), with the active step highlighted.
- WHEN the hero is scrolled past THEN the 3D object and headline SHALL parallax at different rates (depth), bounded to the hero's height.
- WHEN the pointer moves over the hero THEN the 3D object SHALL tilt toward the pointer with damping (no snapping).
- WHEN a copyable command is clicked THEN a confirmation micro-animation SHALL play and the clipboard SHALL receive the command.

**US-2.2 As a visitor with motion sensitivity, I get a static experience.**
- IF `prefers-reduced-motion: reduce` is set THEN all scroll-linked, parallax and looping animations SHALL be disabled, reveal states SHALL be visible immediately, videos SHALL NOT autoplay, and the 3D object SHALL render as a static frame.

**US-2.3 As a mobile visitor, motion does not fight scrolling.**
- WHEN on a viewport < 768 px THEN pinned/scroll-linked sections SHALL fall back to stacked static cards, parallax SHALL be disabled, and only opacity/transform reveals ≤ 400 ms SHALL remain.

### R3 — 3D hero ("有 3D 效果")

**US-3.1 As a first-time visitor, I immediately see a 3D object that reads as "ggcode".**
- WHEN WebGL 2/1 is available and the device is not low-end THEN the hero SHALL render a real-time 3D scene using the brand accent (`#7c6cff`) on the dark surface, with a clear silhouette (abstract terminal block / `>_` glyph / lattice cube — final pick in design), lit and shaded (not a flat sprite).
- WHEN the scene is rendered THEN pixel ratio SHALL be capped at 2, the canvas SHALL be `aria-hidden`, and rendering SHALL pause when the hero is off-screen or the tab is hidden.

**US-3.2 As a visitor on an unsupported device, I see the same composition, static.**
- IF WebGL is unavailable, context creation fails, or `deviceMemory ≤ 2` / `Save-Data` is set THEN the 3D slot SHALL show a pre-rendered poster of the same object and no WebGL code SHALL execute.

**US-3.3 As a performance-conscious maintainer, 3D never blocks first paint.**
- WHEN the page loads THEN the 3D bundle SHALL load lazily (idle/visible) after LCP, SHALL be ≤ 180 KB gzipped total, and LCP SHALL stay ≤ 2.5 s on the Lighthouse mobile profile.

### R4 — Information architecture

**US-4.1 As an evaluator, the page tells a story in order.**
- WHEN the page is viewed top-to-bottom THEN sections SHALL appear in this order: Hero → Proof (3 metrics: platforms, install time, stars/downloads — real numbers only) → Product (TUI/Desktop/Mobile tabs) → Features (bento grid) → How it works (pinned 3 steps) → Ecosystem (MCP / multi-agent / LAN chat schematic) → Install (OS tabs, copyable) → CTA → Footer.
- WHEN a nav link is clicked THEN it SHALL smooth-scroll to the section with correct focus handling and an offset for the sticky nav.

**US-4.2 As a developer, install is frictionless.**
- WHEN the Install section loads THEN the tab for the visitor's OS SHALL be preselected via `navigator.userAgentData`/`userAgent` with a safe default (macOS/Linux).
- WHEN the Homebrew/npm/native package options exist THEN they SHALL be reachable as secondary options within the same section, not a new page.

### R5 — Internationalization & SEO

**US-5.1 As a Chinese-reading developer, I get a native page.**
- WHEN `/zh/` is requested THEN the full page SHALL render in Chinese server-side (static HTML), with `<html lang="zh-CN">`, localized OG/Twitter tags, and the language switch SHALL navigate between `/` and `/zh/` preserving the current path.
- WHEN the root `/` is requested THEN English SHALL be the default and `hreflang="en"` / `hreflang="zh-Hans"` / `x-default` SHALL be emitted.

**US-5.2 As a search engine, I can index everything.**
- WHEN the site builds THEN a sitemap with both locales, canonical URLs, `robots.txt`, JSON-LD `SoftwareApplication` (name, os, offers price 0, feature list) SHALL be emitted; legal pages SHALL keep their existing URLs.

### R6 — Performance & accessibility budgets

- WHEN measured with Lighthouse mobile (Moto G Power, slow 4G) THEN Performance ≥ 90, Accessibility ≥ 95, Best practices ≥ 95, SEO ≥ 95.
- WHEN the page loads THEN CLS ≤ 0.1, INP ≤ 200 ms, total JS for non-3D paths ≤ 120 KB gz, initial CSS ≤ 60 KB gz, fonts ≤ 2 families with `font-display: swap` and self-hosted subsets.
- WHEN navigated by keyboard THEN all interactive elements are reachable, focus is visible, tabs/accordions use ARIA patterns, decorative canvases/videos are `aria-hidden`, and every informative image has `alt`.
- WHEN color contrast is checked THEN text meets WCAG 2.1 AA (muted text currently `#7878a0` on `#0a0a0f` = 4.6:1 — keep or raise).

### R7 — Build & deploy

- WHEN `docs/site/**` changes on `main` THEN the existing `pages-deploy.yml` trigger SHALL still deploy, now running `npm ci && npm run build` and publishing `dist/`.
- WHEN the Railway Docker path is used THEN the Dockerfile SHALL multi-stage build the site and serve `dist/` via Caddy with the same cache headers (HTML no-cache, hashed assets immutable).
- WHEN a build runs THEN it SHALL fail on broken internal links, missing images referenced by content, or TypeScript errors.
- WHEN `__SITE_ASSET_VERSION__` was used THEN it SHALL be replaced by content-hashed filenames (no runtime substitution).

### R8 — Content operations

- WHEN a new screenshot is needed THEN a documented capture procedure (tooling, viewports, redaction) SHALL exist under `docs/site/CAPTURE.md` so visuals can be refreshed per release.
- WHEN demo videos are updated THEN they are transcoded to H.264 MP4 (yuv420p, faststart) + WebM, ≤ 1.8 MB each, with a poster frame.

## Open decisions (recommendation in parentheses — confirm or override)

| # | Decision | Options | Recommendation |
|---|---|---|---|
| D1 | Stack | A zero-build vanilla + vendored libs · B **Astro static** · C Vite vanilla TS | **B** — components, `astro:assets` image pipeline, islands for 3D only, static output keeps Pages deploy trivial |
| D2 | 3D tech | vanilla three.js island · React Three Fiber · Spline embed | **vanilla three.js** (~150 KB gz, no React runtime, full control) |
| D3 | Motion lib | GSAP + ScrollTrigger (free) · pure CSS/IO · Motion One | **GSAP ScrollTrigger** for pinned/scroll-linked, CSS for ambient |
| D4 | Hero 3D subject | glass terminal cube · particle `>_` glyph · lattice cube | **particle `>_` glyph morphing into a lattice cube** (brand-recognizable, cheap to animate) — final call in design review |
| D5 | i18n routing | keep client toggle · **static `/` + `/zh/`** | **static routes** (SEO + native rendering) |
| D6 | Fonts | keep Google Fonts (Space Grotesk / JetBrains Mono) · self-host | **self-host woff2 subsets** (privacy, no render-blocking third party) |
