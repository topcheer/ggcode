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
// Astro's i18n fallback emits a redirect stub at zh/404/index.html that points
// at /404/ - a route that does not exist (the real page is 404.html, which the
// static host serves for every unknown path, including /zh/*). Drop the stub.
rmSync(join(dist, 'zh/404'), { recursive: true, force: true });
// Sitemap: rewrite directory legal URLs to the .html form.
const sm = join(dist, 'sitemap-0.xml');
if (existsSync(sm)) {
  const { readFileSync, writeFileSync } = await import('node:fs');
  writeFileSync(sm, readFileSync(sm, 'utf8').replace(/(privacy|terms)\/<\/loc>/g, '$1.html</loc>'));
}
// llms-full.txt: the full English README for AI crawlers that want the whole
// text in one fetch (https://llmstxt.org). Regenerated from the repo README on
// every build so it never drifts.
// docs/site/README.md on the deployment branch (Docker context = docs/site),
// ../../../README.md in the main repo checkout.
const readmeCandidates = [new URL('../README.md', import.meta.url), new URL('../../../README.md', import.meta.url)];
const readme = readmeCandidates.find((u) => existsSync(u.pathname));
if (readme) {
  const { copyFileSync } = await import('node:fs');
  copyFileSync(readme.pathname, join(dist, 'llms-full.txt'));
} else {
  console.error('[postbuild] README.md not found; llms-full.txt not generated');
  process.exitCode = 1;
}
console.log('[postbuild] legal URLs normalised');
