// @ts-check
import { defineConfig } from 'astro/config';
import sitemap from '@astrojs/sitemap';

// https://astro.build/config
// SITE_BASE lets the same source build for a sub-path host (GitHub Pages
// project site at /ggcode/) or the domain root (Railway → ggcode.dev). Default '/'.
const SITE_BASE = (process.env.SITE_BASE || '/').replace(/\/$/, '') || '/';

export default defineConfig({
  site: 'https://ggcode.dev',
  base: SITE_BASE,
  output: 'static',
  build: { inlineStylesheets: 'always' },
  compressHTML: true,
  i18n: {
    defaultLocale: 'en',
    locales: ['en', 'zh'],
    routing: { prefixDefaultLocale: false },
    fallback: { zh: 'en' },
  },
  // The sitemap describes the production host only; a sub-path mirror build
  // (GitHub Pages) skips it - its canonical/hreflang still point at ggcode.dev.
  integrations: SITE_BASE === '/' ? [sitemap()] : [],
});
