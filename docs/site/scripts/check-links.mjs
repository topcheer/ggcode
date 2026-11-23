// Fail the build on broken internal links or missing referenced assets.
import { readdirSync, readFileSync, statSync, existsSync } from 'node:fs';
import { join, relative } from 'node:path';

const dist = new URL('../dist/', import.meta.url).pathname;
// Root-absolute URLs in a sub-path build carry the Astro base (e.g. /ggcode/_astro/..).
const base = (process.env.SITE_BASE || '/').replace(/\/$/, '');
const stripBase = (u) => (base && u.startsWith(base + '/') ? u.slice(base.length) : (u === base ? '/' : u));
const files = [];
(function walk(d) { for (const n of readdirSync(d)) { const p = join(d, n); statSync(p).isDirectory() ? walk(p) : files.push(p); } })(dist);

const html = files.filter((f) => f.endsWith('.html'));
const ref = /(?:href|src)=["']([^"'#?]+)(?:[?#][^"']*)?["']/g;
const bad = new Set();
for (const f of html) {
  const txt = readFileSync(f, 'utf8');
  for (const m of txt.matchAll(ref)) {
    const u = m[1];
    if (/^(https?:|mailto:|data:|tel:)/i.test(u) || u.startsWith('//')) continue;
    const rel = stripBase(u);
    let target = rel.startsWith('/') ? join(dist, rel) : join(f.replace(/\/[^/]+$/, ''), rel);
    if (existsSync(target) && statSync(target).isDirectory()) target = join(target, 'index.html');
    if (!existsSync(target)) bad.add(`${relative(dist, f)} -> ${u}`);
  }
}
// Every image/video referenced must exist (covered above); also check srcset urls.
for (const f of html) {
  const txt = readFileSync(f, 'utf8');
  for (const m of txt.matchAll(/srcset=["']([^"']+)["']/g)) {
    for (const part of m[1].split(',')) { const raw = part.trim().split(/\s+/)[0]; const u = stripBase(raw); if (u && !u.startsWith('/') && !existsSync(join(f.replace(/\/[^/]+$/, ''), u))) bad.add(`${relative(dist, f)} srcset -> ${raw}`); if (u.startsWith('/') && !existsSync(join(dist, u))) bad.add(`${relative(dist, f)} srcset -> ${raw}`); }
  }
}
if (bad.size) { console.error(`[check-links] ${bad.size} broken reference(s):\n` + [...bad].map((b) => '  ' + b).join('\n')); process.exit(1); }
console.log(`[check-links] ok (${html.length} html files)`);
