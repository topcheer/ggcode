// Keep the legacy legal URLs (/privacy.html, /terms.html) while letting Astro
// use directory routes everywhere else. Astro renders `privacy.astro` to
// `privacy/index.html`; we move it to `privacy.html` so old links keep working.
import { renameSync, rmSync, existsSync, mkdirSync } from 'node:fs';
import { join } from 'node:path';

const dist = new URL('../dist/', import.meta.url).pathname;
const moves = [
  ['privacy/index.html', 'privacy.html'],
  ['terms/index.html', 'terms.html'],
  ['zh/privacy/index.html', 'zh/privacy.html'],
  ['zh/terms/index.html', 'zh/terms.html'],
];
for (const [from, to] of moves) {
  const src = join(dist, from), dst = join(dist, to);
  if (!existsSync(src)) { console.error(`[postbuild] missing ${from}`); process.exitCode = 1; continue; }
  renameSync(src, dst);
  rmSync(join(dist, from.replace('/index.html', '')), { recursive: true, force: true });
}
// Sitemap: rewrite directory legal URLs to the .html form.
const sm = join(dist, 'sitemap-0.xml');
if (existsSync(sm)) {
  const { readFileSync, writeFileSync } = await import('node:fs');
  writeFileSync(sm, readFileSync(sm, 'utf8').replace(/(privacy|terms)\/<\/loc>/g, '$1.html</loc>'));
}
console.log('[postbuild] legal URLs normalised');
