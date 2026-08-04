# JS/TS Anti-Pattern Detection

## Overview

ggcode includes a post-write integrity check that detects common JavaScript/TypeScript anti-patterns introduced by the AI agent during file edits. This check runs automatically after every file write operation - no configuration needed.

## Detected Anti-Patterns

| Pattern | Severity | Languages | Description |
|---------|----------|-----------|-------------|
| Loose equality (`==`, `!=`) | High | JS/TS | Type coercion can cause subtle bugs. Use strict equality (`===`, `!==`) instead. |
| `var` declaration | Medium | JS/TS | Function-scoped with hoisting issues. Use `const` or `let`. |
| Explicit `any` type | Medium | TS only | Defeats TypeScript type safety. Use `unknown` with type narrowing or define proper types. |

## How It Works

- **Delta-based**: Only flags anti-patterns *introduced* by the current edit (count in new content > count in old content). Pre-existing anti-patterns are not flagged.
- **Zero LLM cost**: Pure regex-based detection, <1ms per file.
- **Language-aware**: `any` type check only fires for TypeScript files (`.ts`, `.tsx`, `.mts`, `.cts`).
- **Exempt directories**: Skips `node_modules/`, `dist/`, `build/`, vendor dirs, and minified files.

## Competitor Comparison

| Product | Inline Detection | External Linter Required |
|---------|-----------------|------------------------|
| ggcode | Yes (built-in) | No |
| Claude Code | No | Yes (ESLint) |
| Cursor | No | Yes (ESLint) |
| Cline/OpenHands | No | Yes |
| Aider | No | Yes |
| Windsurf | No | Yes (ESLint) |

## Relationship to Other Checks

This check complements existing JS/TS integrity checks:
- **debug-statements**: Detects `console.log`, `debugger`, etc.
- **insecure-patterns**: Detects `eval()`, XSS vectors, etc.
- **tag-balance**: Detects unbalanced HTML/JSX tags.
