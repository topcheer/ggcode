# Capturing product visuals for the site

Everything the landing page shows must be a real frame of the product. This is
the per-release procedure for refreshing screenshots and demo loops.

## Sources of truth

| Surface | Source | Where it lives |
|---|---|---|
| TUI | `docs/marketing/videos/ggcode-tui-demo.mp4` | recorded with `vhs` or `asciinema` → `agg` → ffmpeg |
| Desktop | `docs/marketing/videos/ggcode-desktop-demo.mp4` | `desktop_control` screen recording of the Wails app |
| Mobile | `docs/marketing/videos/ggcode-mobile-demo.mp4` | `mobile_device` recording on iOS simulator / Android emulator |

Screenshots on the site are **frames extracted from these videos** so the stills
and the loops can never disagree.

## Frame extraction (what the site uses today)

```bash
cd docs/marketing/videos
S=../../site/src/assets
# hero / product stills
ffmpeg -ss 18    -i ggcode-tui-demo.mp4     -frames:v 1 $S/screenshots/tui/tui-session.png
ffmpeg -ss 6 -i ggcode-desktop-demo.mp4 -vf "crop=1920:1020:0:0" -frames:v 1 $S/screenshots/desktop/desktop-main.png
ffmpeg -ss 6     -i ggcode-mobile-demo.mp4  -frames:v 1 $S/screenshots/mobile/mobile-main.png
```

- Desktop frames are cropped to 1920×1020 to drop the account row in the sidebar
  footer (username/avatar). Keep that crop.
- Pick frames where the status bar shows a finished step (`✓`) rather than
  "running..." when the still will sit next to a "done" claim.

## Video transcoding (site budget: ≤ 1.8 MB each, poster-first)

```bash
for v in tui desktop mobile; do
  ffmpeg -i ggcode-$v-demo.mp4 -vf "scale=1280:-2:flags=lanczos" \
    -c:v libx264 -crf 28 -preset slow -pix_fmt yuv420p -movflags +faststart -an site/public/video/$v.mp4
  ffmpeg -i ggcode-$v-demo.mp4 -vf "scale=1280:-2:flags=lanczos" \
    -c:v libvpx-vp9 -crf 38 -b:v 0 -row-mt 1 -an site/public/video/$v.webm
  ffmpeg -ss 4 -i ggcode-$v-demo.mp4 -vf "scale=1280:-2" -frames:v 1 -q:v 3 site/public/video/posters/$v.jpg
done
```

## Redaction checklist (before committing any frame)

- [ ] No absolute paths with usernames (`/Users/<name>/…`) visible in prompts or bars.
- [ ] No account row / avatar / email (sidebar footer on desktop).
- [ ] No tokens, API keys, env values in terminal output.
- [ ] No private repo names or customer data in file paths.
- [ ] Status bar numbers (cost, context %) look plausible, not `–`.

## OG / hero poster

`public/og/{en,zh}.jpg` (1200×630) and `public/og/hero-poster.webp` are generated
procedurally (PIL, particle `>_`) so they stay on-brand. Regenerate with the
snippet in `scripts/` if the accent changes; keep JPEG ≤ 300 KB.

## Adding a new screenshot

1. Record/extract at 1920×1080 (or @2x for mobile), PNG.
2. Drop it under `src/assets/screenshots/<surface>/` and import it through
   `<Picture>` / `<Image>` (astro:assets) so AVIF/WebP + `srcset` + intrinsic
   size are emitted automatically. Never reference a raw file from `public/`
   for content images.
3. Provide an `alt` that describes what the screen shows, not "screenshot".
