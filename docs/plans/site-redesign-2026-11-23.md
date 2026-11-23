# Plan: Website Redesign (ggcode.dev) — 2026-11-23

Status: **APPROVED & IMPLEMENTED (branch `site-redesign`)** — D1–D6 as recommended, primary colour switched to **blue** per user decision (app-icon family). Phases 0–7 done (#5933, #5934, #5935, #5936): both hosts live, Lighthouse budgets enforced in CI (98/97/100/100). Open human follow-ups: Plausible 7-day baseline, release note. Detailed artifacts: `specs/site-redesign/{requirements,design,tasks}.md`. This file is self-contained: an approver can decide from it alone.

## 1. Current state (audited 2026-11-23)

| Aspect | Finding |
|---|---|
| Stack | Plain static: `index.html` 15.9 KB · `styles.css` 12.5 KB · `script.js` 10 KB · `privacy.html` · `terms.html` · hand-written `sitemap.xml` |
| Imagery | **1 image** (`ggcode-banner.png`, 390 KB, doubles as OG). Feature cards use emoji only |
| Motion | 3 floating gradient orbs (`@keyframes float`), `gradientShift`, IntersectionObserver reveal. No scroll storytelling, no video, no 3D |
| i18n | Hand-rolled `STRINGS` dict, **53 keys** × en/zh in `script.js`; `data-i18n` + `localStorage("ggcode-lang")`; one URL for both languages → no Chinese SEO entry |
| Deploy | **GitHub Pages** via `.github/workflows/pages-deploy.yml` (copies `docs/site` verbatim on `docs/site/**`) **+ Railway** via `Dockerfile` (caddy) + `Caddyfile` + `site-release.yml`; `__SITE_ASSET_VERSION__` sed at image build |
| Unused assets | 3 real demo videos already in repo: `docs/marketing/videos/ggcode-{tui,desktop,mobile}-demo.mp4` (1.0–1.7 MB) |
| Toolchain | Node 26 / npm 11 locally; desktop app already on Vite 8 + TS, so a Node build in `docs/site/` is not foreign to the repo |

## 2. Goals / non-goals

**Goals** — (1) show the real product instead of describing it; (2) a coherent motion system that never janks; (3) a signature 3D hero with graceful fallbacks; (4) zero regression in SEO, speed, a11y, bilingual support; one-step deploy.

**Non-goals** — no CMS/SSR/client framework beyond the 3D island; no change to install scripts, product, or `docs/`; no paid asset services (Spline embeds, Lottie subscriptions).

## 3. Decisions D1–D6 (default = recommended; confirm or override)

| # | Decision | Recommended | Why | Rejected alternatives |
|---|---|---|---|---|
| D1 | Stack | **Astro 5, static output, inside `docs/site/`** | Components without shipping a framework; `astro:assets` = AVIF/WebP + `srcset` + intrinsic sizes (kills CLS); islands make the hero the *only* hydrated code; static `dist/` keeps Pages deploy a copy step | Zero-build vanilla (no image pipeline, hand-vendored libs, copy-pasted cards) · Vite vanilla TS (no SSG for legal/i18n pages) |
| D2 | 3D tech | **vanilla three.js in one `<script>` island** | ~150 KB gz, no React runtime, full control of render loop / DPR / pause | React Three Fiber (ships React for one scene) · Spline (external iframe, perf/privacy) |
| D3 | Motion lib | **GSAP + ScrollTrigger** + CSS keyframes for ambient + tiny IO helper for reveals | ScrollTrigger handles pin/scrub robustly across devices; CSS is cheapest for loops | Motion One (weaker pinning) · pure CSS/IO (no scroll-linked storytelling) |
| D4 | Hero subject | **Particle field forming `>_`, morphing into a lattice cube on scroll** | Reads as "terminal → structure"; one `Points` draw call; morph is a shader lerp | Glass terminal cube (generic) · flat sprite (not really 3D) |
| D5 | i18n routing | **Static `/` (en) + `/zh/`**, strings in `src/i18n/{en,zh}.json` | Native static Chinese page, hreflang, localized OG; switch preserves path | Keep client toggle (no zh SEO) |
| D6 | Fonts | **Self-hosted woff2 subsets** (`@fontsource` Space Grotesk / JetBrains Mono) | Removes third-party render-blocking request | Keep Google Fonts |

## 4. Page narrative (top → bottom)

| # | Section | Content | Motion | Visuals |
|---|---|---|---|---|
| 1 | **Hero** | badge, H1, sub, copyable install command, CTAs | 3D `>_`→cube morph, pointer tilt (damped), parallax on desktop | 3D canvas + real TUI still as poster; CSS typed-out terminal |
| 2 | **Proof** | 3 real metrics (platforms, install time, GitHub stars at build) | count-up once | typographic |
| 3 | **Product** | tabs TUI / Desktop / Mobile, each with 3 bullets | crossfade; video plays when active + in view | the 3 real demo videos (lazy, poster-first) |
| 4 | **Features** | bento grid: toolkit, MCP, multi-agent, IM/LAN, permission modes, worktrees | stagger reveal; hover tilt ≤ 3° (desktop) | mini UI illustrations (SVG), no emoji-only cards |
| 5 | **How it works** | Open in repo → Agent plans & edits in isolated worktree → Review/test/ship | pinned scroll story ≥ 1024 px (GSAP); stacked below | step illustrations |
| 6 | **Ecosystem** | MCP / multi-agent / LAN chat schematic | animated data-flow lines (CSS) | SVG schematic |
| 7 | **Install** | OS-detected tabs: macOS/Linux, Windows, Homebrew, npm, iOS, Android | copy micro-animation | OS glyphs |
| 8 | **CTA / Footer** | one-line CTA, links, language switch, Plausible | none | — |

Legal pages keep their URLs (`/privacy.html`, `/terms.html`) via `build.format: 'file'`.

## 5. Roadmap — 8 phases (details + ~55 verifiable steps in `specs/site-redesign/tasks.md`)

| Phase | Scope | Exit criteria |
|---|---|---|
| **0 · Decide & scaffold** | Confirm D1–D6; Astro project in `docs/site/` (astro, gsap, three, sitemap, fontsource); config (static, `build.format=file`, i18n en/zh); archive old HTML to `src/legacy/` | `npm run build` emits `dist/index.html`; `astro check` green |
| **1 · Tokens, layout, i18n migration** | Promote palette to tokens (raise muted text to AA); global CSS + prose; migrate all 53 strings to `en/zh.json`; `Base.astro` (hreflang/OG/JSON-LD/Plausible/fonts); Nav/Footer/LangSwitch/Reveal; index + zh/index + legal pages | `dist/{index,zh/index,privacy,terms}.html` built; both locales carry all migrated strings; no `__SITE_ASSET_VERSION__` |
| **2 · Media capture & pipeline** | `CAPTURE.md`; 6–10 real screenshots (TUI via vhs/asciinema, desktop via `desktop_control`, mobile via `mobile_device`); transcode 3 videos (mp4 faststart + webm + poster, ≤ 1.8 MB); 6 feature SVGs + ecosystem schematic; `Picture`/`VideoLoop` components; OG images ≤ 300 KB | Every raster `<img>` has AVIF+WebP `srcset`, `width/height`, `alt`; videos within budget |
| **3 · Sections & motion** | Proof, ProductTabs, BentoGrid, HowItWorks (pin), Ecosystem, TerminalPlayer, InstallTabs (OS detect + copy), CTA, hero parallax | 375/768/1024/1440 verified; pin only ≥ 1024; keyboard pass; reduced-motion = instant, no autoplay |
| **4 · 3D hero** | `Hero3D` island (Points `>_` → cube, pointer tilt, scroll morph, DPR ≤ 2, pause off-screen/hidden, context-loss handling); fallback ladder → `hero-poster.webp`; poster render script | 3D chunk ≤ 180 KB gz, loaded after LCP; WebGL-off shows poster; ≤ 8 ms/frame on M1 @1440×900 |
| **5 · SEO, deploy, CI** | Generated sitemap + hreflang; `pages-deploy.yml` gains setup-node + `npm ci` + build, publishes `dist/`; multi-stage Dockerfile + Caddy root→dist; `site-release.yml`; link-check script in build; `astro check` in lint | Pages preview serves `/`, `/zh/`, `/privacy.html`, `/terms.html` with 200; hashed assets immutable; JSON-LD validates |
| **6 · Perf & a11y QA** | Lighthouse CI assertions; bundlesize; font subsets; browser matrix (Chrome/Safari/Firefox, iOS/Android, JS-off, WebGL-off, reduced-motion, keyboard, 200 % zoom); axe contrast | Budgets in §6 met; matrix recorded in PR |
| **7 · Launch & measure** | PR with 3 viewports × 2 locales screenshots + reports → merge → Pages deploy → live verification + OG debuggers → 7-day Plausible baseline → release note | Live site verified; follow-ups filed |

Dependencies: 1,2 after 0 · 3,4 after 1 · 5 after 2,3,4 · 6 after 5 · 7 after 6. Phases are registered on the task board (`task-1`…`task-8`).

Stretch (post-launch): `/zh/privacy` + `/zh/terms`, interactive ecosystem schematic, asciinema inline terminal.

## 6. Quality gates

- **Lighthouse mobile**: Performance ≥ 90 · Accessibility ≥ 95 · Best practices ≥ 95 · SEO ≥ 95; LCP ≤ 2.5 s; CLS ≤ 0.1; INP ≤ 200 ms.
- **Bundle**: non-3D JS ≤ 120 KB gz · 3D chunk ≤ 180 KB gz (lazy, after LCP) · CSS ≤ 60 KB gz · first load ≤ 1.2 MB incl. poster.
- **Reduced motion**: all scroll-linked/parallax/loop animations off, reveals visible immediately, no video autoplay, 3D = static frame.
- **Fallback ladder (3D)**: no WebGL / context lost / `deviceMemory ≤ 2` / `Save-Data` / reduced-motion → same-composition poster, three.js never imported.
- **Mobile (< 768 px)**: no pinning, no parallax; opacity/transform reveals ≤ 400 ms only.
- **A11y**: keyboard-reachable, visible focus, ARIA tabs, decorative canvas/video `aria-hidden`, informative images `alt`, WCAG 2.1 AA contrast.
- **Build**: fails on broken internal links, missing referenced images, TS errors.
- **URL continuity**: `/`, `/privacy.html`, `/terms.html` unchanged; `/zh/` additive.

## 7. Risks & mitigations

3D hurting LCP/battery → island + idle load + off-screen pause + DPR cap + fallback ladder · GSAP pin jank on mobile → ≥ 1024 px only · Node toolchain in a Go repo → confined to `docs/site/`, CI setup-node, multi-stage Docker · autoplay policy → muted + playsinline + poster + play button · stale screenshots → `CAPTURE.md` + release checklist · scope creep → stretch list only after launch.

## 8. Awaiting approval

1. **D1–D6** — go with defaults? Most consequential: D1 (introduce a Node build for the site) and D4 (hero subject).
2. **Brand assets** — logo source files, preferred product screenshots, any color exclusions? Otherwise: keep the existing purple accent (`#7c6cff`) and capture visuals ourselves.
3. On approval, execution starts at Phase 0 and proceeds through `tasks.md` in order, verifying each phase before the next.
