// @ts-check
import { defineConfig } from 'astro/config';
import sitemap from '@astrojs/sitemap';

// https://astro.build/config
export default defineConfig({
  site: 'https://ggcode.dev',
  output: 'static',
  build: { inlineStylesheets: 'auto' },
  compressHTML: true,
  i18n: {
    defaultLocale: 'en',
    locales: ['en', 'zh'],
    routing: { prefixDefaultLocale: false },
    fallback: { zh: 'en' },
  },
  integrations: [sitemap()],
});
