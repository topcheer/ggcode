# Accessibility (a11y) Intelligence Check

## Overview

Write-time WCAG 2.2 accessibility detection for HTML/Vue/Svelte/JSX/TSX files.
Detects common accessibility violations as the agent writes code, before the
file is even saved -- catching issues that runtime tools like axe-core and
Lighthouse only find after deployment.

## Motivation

Accessibility compliance is now a legal requirement:
- **WCAG 2.2** (2023): Updated guidelines with new success criteria
- **EU Accessibility Act** (June 2025): Mandates digital accessibility
- **ADA lawsuits**: US accessibility lawsuits increased 300% since 2018

Industry tools (axe-core, Lighthouse, Pa11y) provide **runtime-only** auditing.
No AI coding agent detects a11y issues at **write time**.

## Competitor Analysis

| Tool | Write-Time Detection | Coverage | LLM Cost |
|------|---------------------|----------|----------|
| **ggcode** | Yes (this check) | 5 WCAG checkpoints | Zero |
| axe-core | No (runtime) | Full WCAG | N/A |
| Lighthouse | No (runtime) | Full audit | N/A |
| GitHub Copilot | Inline hints only | Inconsistent | High |
| Cursor | Via extensions only | eslint-plugin-jsx-a11y | N/A |
| Claude Code | Agent judgment | Unreliable | High |

## Implemented Checks

All checks are deterministic pattern matching (zero LLM cost):

### 1. Missing Alt Text (WCAG 1.1.1)
Detects `<img>` tags without an `alt` attribute. Decorative images with
`alt=""` are allowed.

### 2. Interactive Div/Span (WCAG 4.1.2)
Detects `<div onclick>` or `<span onclick>` without `role`, `tabindex`,
or keyboard event handlers. These elements are invisible to keyboard and
screen reader users.

### 3. Input Without Label (WCAG 1.3.1/3.3.2)
Detects `<input>` elements without an associated `<label for>`, `aria-label`,
`aria-labelledby`, or `title` attribute. Hidden, submit, button, reset, and
image input types are excluded.

### 4. Heading Hierarchy Skip (WCAG 1.3.1/2.4.6)
Detects skipped heading levels (e.g., `<h1>` directly followed by `<h3>`).
Screen reader users navigate by heading structure; skips cause disorientation.

### 5. Invalid ARIA Values (WCAG 4.1.2)
- Validates `role="..."` against the WAI-ARIA 1.2 role list (77 valid roles)
- Validates boolean `aria-*` states (`aria-hidden`, `aria-expanded`, etc.)
  accept only `"true"` or `"false"`

## File Types

| Extension | Type | Checked |
|-----------|------|---------|
| `.html`, `.htm` | Markup | Yes |
| `.vue` | Markup | Yes |
| `.svelte` | Markup | Yes |
| `.svg` | Markup | Yes |
| `.jsx`, `.tsx` | JS/TS | Yes |
| `.js`, `.ts` | JS/TS | Yes |
| `.go`, `.py` | Other | No |

## Configuration

No configuration needed. The check is automatically enabled as part of the
post-write integrity check pipeline for markup and JS/TS files.

Warnings are capped at 6 per file to avoid flooding the agent's context.
