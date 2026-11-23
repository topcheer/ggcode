# Plan: Website Redesign (ggcode.dev) — 2026-11-23

Spec-driven plan for rebuilding `docs/site/` with real product imagery, a
motion system, and a 3D hero. Artifacts live in `specs/site-redesign/`:

- [`requirements.md`](../../specs/site-redesign/requirements.md) — current-state audit, goals/non-goals, user stories with EARS acceptance criteria (R1 images · R2 motion · R3 3D · R4 IA · R5 i18n/SEO · R6 perf/a11y · R7 build/deploy · R8 content ops), open decisions D1–D6.
- [`design.md`](../../specs/site-redesign/design.md) — stack (Astro static + three.js island + GSAP ScrollTrigger, `/` + `/zh/`), repo layout, section map, tokens, components, image/video pipeline, motion rules, fallbacks, SEO/deploy, budgets, risks.
- [`tasks.md`](../../specs/site-redesign/tasks.md) — 8 phases, ~55 verifiable steps; phases mirrored in the task board.

Status: DRAFT awaiting approval of D1–D6 (defaults: Astro, vanilla three.js, GSAP, particle `>_`→cube hero, static `/zh/`, self-hosted fonts). Implementation starts only on explicit go.
