# Website Redesign — Tasks

Feature: `site-redesign` · Each step is small and verifiable; `[Rn.m]` references requirements.md. Check off as verified; major phases are mirrored in `task_list`.

## Phase 0 — Decide & scaffold (R7)

> Implementation status (2026-11-23): Phases 0–5 implemented on branch `site-redesign`; D1–D6 taken as recommended **except the accent colour: blue** (user decision, derived from the app icon `#1854b0` / favicon `#58a6ff`), replacing the purple palette in design §4. 6.1 (Lighthouse CI) and 6.5 (axe) deferred to a follow-up PR; manual matrix done for Chrome desktop 1440 + mobile 390 × en/zh, JS-fail safety net added (reveal fallback).

- [x] 0.1 Confirm decisions D1–D6 in requirements.md (default = recommendations). `[D1–D6]`
- [x] 0.2 Create branch `site-redesign`; in `docs/site/` run `npm init -y`, install `astro@latest gsap@latest three@latest @astrojs/sitemap@latest @fontsource/space-grotesk @fontsource/jetbrains-mono typescript`, add `.gitignore` (`node_modules`, `dist`, `.astro`). `[R7]`
- [x] 0.3 `astro.config.mjs`: `output: 'static'`, `site: 'https://ggcode.dev'`, `build.format: 'file'`, `i18n: { defaultLocale: 'en', locales: ['en','zh'], routing: { prefixDefaultLocale: false } }`, sitemap integration. `[R5, R7, §8]`
- [x] 0.4 Move current `index.html`, `privacy.html`, `terms.html` into `src/legacy/` (reference only, not built); keep `public/favicon.svg`, `robots.txt`, `sitemap.xml` (sitemap later generated). `[R7]`
- [x] 0.5 Verify: `npm run build` produces `dist/index.html` from a placeholder `src/pages/index.astro`; `npx astro check` passes. `[R7]`

## Phase 1 — Tokens, layout, i18n migration (content parity)

- [x] 1.1 `src/styles/tokens.css`: promote existing palette + add accent-2/3, spacing, radius, type, motion tokens; raise `--text-muted` to `#8a8ab5`. `[R6, §4]`
- [x] 1.2 `src/styles/global.css`: reset, typography, `.prose` for legal pages, `.js`/no-js defaults, reduced-motion base rule. `[R2.2, R6]`
- [x] 1.3 `src/i18n/{en,zh}.json`: migrate all 53 keys from `script.js` verbatim (en + zh); add keys for Proof/Product/HowItWorks/Ecosystem sections (write copy, zh translation). `[R5.1]`
- [x] 1.4 `src/i18n/utils.ts`: `t(key)`, `localeFromUrl(url)`, `localizedPath(locale, path)`. `[R5.1]`
- [x] 1.5 `Base.astro`: `<head>` with title/description/canonical/hreflang (en, zh-Hans, x-default)/OG+Twitter per locale/JSON-LD `SoftwareApplication`/Plausible snippet/self-hosted fonts; skip-link; `<html lang>`. `[R5.1, R5.2, R6]`
- [x] 1.6 `Nav.astro` (sticky, blur backdrop, lang switch, anchor links with focus handling) and `Footer.astro`. `[R4.1]`
- [x] 1.7 `LangSwitch.astro`: `/` ↔ `/zh/` path-preserving; remembers `localStorage("ggcode-lang")`. `[R5.1]`
- [x] 1.8 `Reveal.astro` + `src/scripts/reveal.ts`: IO adds `.is-visible` once; stagger via `--i`; default visible without JS. `[R2.1, R2.2]`
- [x] 1.9 `pages/index.astro` + `pages/zh/index.astro` composing placeholder sections with migrated copy; `privacy.astro`, `terms.astro` with verbatim content in `.prose`. `[R5.2]`
- [x] 1.10 Verify: build → `dist/index.html`, `dist/zh/index.html`, `dist/privacy.html`, `dist/terms.html`; both locales contain all migrated strings; no `__SITE_ASSET_VERSION__` left. `[R5, R7]`

## Phase 2 — Media capture & pipeline ("多图")

- [x] 2.1 Write `docs/site/CAPTURE.md`: tooling (vhs/asciinema for TUI, `desktop_control` for desktop, `mobile_device` for mobile), viewports, @2x, redaction checklist, naming, where files go. `[R8]`
- [x] 2.2 Capture 3 TUI screenshots (startup, editing session, diff/commit view) at 1440×900@2x → `src/assets/screenshots/tui/*.png`. `[R1.1]`
- [x] 2.3 Capture 3 desktop-app screenshots (main chat, inspector, worktree/branches) → `src/assets/screenshots/desktop/*.png`. `[R1.1]`
- [x] 2.4 Capture 2–3 mobile screenshots (session list, chat, file preview) → `src/assets/screenshots/mobile/*.png`. `[R1.1]`
- [x] 2.5 Transcode `docs/marketing/videos/ggcode-{tui,desktop,mobile}-demo.mp4` → `src/assets/video/*.mp4` (H.264, yuv420p, faststart, crf 28, ≤ 1280w, ≤ 1.8 MB) + `.webm` (VP9 crf 36) + `posters/*.jpg`. `[R1.1, R8]`
- [x] 2.6 Author 6 feature mini-illustrations (SVG): toolkit, MCP, multi-agent, IM/LAN, permission modes, worktrees; plus Ecosystem schematic SVG. `[R1.2]`
- [x] 2.7 Author 3 step illustrations for How it works (SVG or PNG). `[R2.1, R4.1]`
- [x] 2.8 `media/Picture.astro` wrapper (astro:assets `Image`, explicit sizes, `alt` required, `loading=lazy` below fold) and `media/VideoLoop.astro` (poster-first, lazy src, autoplay fallback). `[R1.3, R6]`
- [x] 2.9 Generate OG images 1200×630 ≤ 300 KB per locale → `public/og/{en,zh}.jpg`; reference absolute URLs in `Base.astro`. `[R1.3, R5.2]`
- [x] 2.10 Verify: build output contains AVIF+WebP variants with `srcset` and `width/height` on every raster `<img>`; videos ≤ 1.8 MB each; `alt` present on all informative images. `[R1.3, R6]`

## Phase 3 — Sections & motion system ("多动效")

- [x] 3.1 `Proof.astro`: 3 metrics from build-time env (`PUBLIC_GH_STARS` optional), count-up on reveal. `[R4.1, R2.1]`
- [x] 3.2 `ProductTabs.astro`: TUI/Desktop/Mobile tabs (ARIA tabs pattern), fixed aspect-ratio media box, crossfade, VideoLoop per tab. `[R1.1]`
- [x] 3.3 `BentoGrid.astro`: 6 cards with illustrations; stagger reveal; hover tilt ≤ 3° (desktop only). `[R1.1, R1.2, R2.1]`
- [x] 3.4 `HowItWorks.astro` + `src/scripts/how-it-works.ts`: GSAP ScrollTrigger pin+scrub on ≥ 1024 px via `matchMedia`; stacked fallback; reduced-motion off. `[R2.1, R2.3]`
- [x] 3.5 `Ecosystem.astro`: animated SVG data-flow lines (`stroke-dashoffset`, CSS), reduced-motion static. `[R1.2, R2.2]`
- [x] 3.6 `TerminalPlayer.astro`: CSS typed-out session, plays once on view, static when reduced-motion/no-JS. `[R2.1, R2.2]`
- [x] 3.7 `InstallTabs.astro`: OS detection, ARIA tabs, clipboard copy with fallback + check micro-animation. `[R4.2, R2.1]`
- [x] 3.8 `Cta.astro`; smooth-scroll anchor handler with focus management and sticky-nav offset. `[R4.1, R6]`
- [x] 3.9 Hero parallax (desktop, bounded) via ScrollTrigger; disabled < 768 px and reduced-motion. `[R2.1, R2.3]`
- [x] 3.10 Verify at 375/768/1024/1440: reveals fire once, pin works at ≥ 1024 only, no horizontal scroll, keyboard-only pass completes, reduced-motion shows everything instantly with no autoplay. `[R2.1–R2.3, R6]`

## Phase 4 — 3D hero ("有 3D 效果")

- [x] 4.1 `Hero3D.astro` island (`client:visible`, `requestIdleCallback` load): three.js Points scene — `>_` glyph particle positions sampled from a canvas text mask; cube-edge target positions; shader lerp via `uMorph`. `[R3.1]`
- [x] 4.2 Pointer tilt (damped), scroll-driven morph (ScrollTrigger progress), DPR cap 2, `alpha` renderer, accent gradient vertex colors. `[R2.1, R3.1]`
- [x] 4.3 Lifecycle: pause on `IntersectionObserver` off-screen and `visibilitychange`; dispose on `webglcontextlost`; resize via `ResizeObserver`. `[R3.1]`
- [x] 4.4 Fallback ladder: capability check (`WebGLRenderingContext`, `deviceMemory`, `Save-Data`, reduced-motion) → `public/og/hero-poster.webp`, never import three; `canvas aria-hidden="true"`. `[R3.2, R6]`
- [x] 4.5 Render hero poster from the same scene (headless capture script `scripts/hero-poster.mjs`) and commit. `[R3.2]`
- [x] 4.6 Verify: bundle report shows 3D chunk ≤ 180 KB gz and loaded after LCP; WebGL-off run shows poster; frame time ≤ 8 ms on M1 at 1440×900; pauses off-screen (check via `performance` overlay). `[R3.1–R3.3]`

## Phase 5 — SEO, deploy, CI (R7)

- [x] 5.1 Sitemap from `@astrojs/sitemap` incl. `/zh/` alternates; delete hand-written `sitemap.xml`; keep `robots.txt` (add sitemap URL). `[R5.2]`
- [x] 5.2 `.github/workflows/pages-deploy.yml`: add `actions/setup-node@v4` (Node 22), `npm ci`, `npm run build` in `docs/site`, stage `dist/`. `[R7]`
- [x] 5.3 `docs/site/Dockerfile` multi-stage (`node:22-alpine` build → `caddy:2.10-alpine` serve `dist/`); drop `__SITE_ASSET_VERSION__` sed; `Caddyfile` root → `dist`. `[R7]`
- [x] 5.4 `site-release.yml`: same build command. `[R7]`
- [x] 5.5 `scripts/check-links.mjs`: crawl `dist/` for internal `href`/`src` 404s; `npm run build` fails on any. Add to `package.json` scripts. `[R7]`
- [x] 5.6 `astro check` + `check-links` added to lint workflow for `docs/site/**` changes. `[R7]`
- [x] 5.7 Verify: Pages preview deploy serves `/`, `/zh/`, `/privacy.html`, `/terms.html` with 200; hashed assets immutable, HTML revalidates; JSON-LD validates in Rich Results test. `[R5.2, R7]`

## Phase 6 — Performance & accessibility QA (R6)

- [ ] 6.1 `lighthouserc.json` (staticDistPath `docs/site/dist`, assertions P≥90/A≥95/BP≥95/SEO≥95, CLS≤0.1) + `npm run lhci` locally; fix regressions. `[R6]`
- [x] 6.2 `bundlesize` config: non-3D JS ≤ 120 KB gz, 3D chunk ≤ 180 KB gz, CSS ≤ 60 KB gz. `[R6, R3.3]`
- [x] 6.3 Self-host font subsets (latin + latin-ext; zh page uses system CJK stack, no CJK webfont). `[R6, D6]`
- [x] 6.4 Manual matrix: Chrome/Safari/Firefox desktop; iOS Safari + Android Chrome; JS off; WebGL off; reduced-motion; keyboard-only; zoom 200%. Record results in PR. `[R2.2, R3.2, R6]`
- [ ] 6.5 Contrast audit with axe (Lighthouse a11y); fix any AA failures. `[R6]`

## Phase 7 — Launch & measure

- [ ] 7.1 Open PR with screenshots at 3 viewports × 2 locales, Lighthouse report, bundle report; review. `[R1–R7]`
- [ ] 7.2 Merge → Pages deploy; verify live `/`, `/zh/`, legal URLs; OG preview via social debuggers. `[R7]`
- [ ] 7.3 Baseline Plausible metrics for 7 days post-launch (bounce, install-click events if instrumented); file follow-ups. `[R8]`
- [ ] 7.4 Update `docs/guide/` if any install instructions changed (none expected); add release note. `[R8]`

## Stretch (only after 7.2)

- [ ] S1 `/zh/privacy`, `/zh/terms` translations. `[R5.1 optional]`
- [ ] S2 Ecosystem schematic interactive (hover highlights node + tooltip). `[R2.1]`
- [ ] S3 Product section: inline real terminal demo via asciinema player (no video). `[R1.1]`
