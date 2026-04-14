# Release notes

GitHub releases are rendered from this directory.

## How it works

1. If `docs/releases/<tag>.md` exists, the Release workflow uses that file as the GitHub Release body.
2. If no tag-specific file exists, the workflow falls back to `scripts/release/render-notes.sh`, which generates a structured release body with:
   - release title
   - compare link
   - filtered changelog

## Recommended workflow

1. Before tagging a release, copy `docs/releases/_template.md` to `docs/releases/<tag>.md`.
2. Fill in the highlights, fixes, and upgrade notes for that version.
3. Commit the notes file together with the release bump.

## Current release notes

The newest tag-specific notes live in the highest-versioned `v*.md` file in this directory
(currently `docs/releases/v1.1.33.md`).

When preparing the next release, copy `docs/releases/_template.md` to the new tag name instead of
editing an older notes file in place.
