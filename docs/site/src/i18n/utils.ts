import en from './en.json';
import zh from './zh.json';

export type Locale = 'en' | 'zh';
export const LOCALES: Locale[] = ['en', 'zh'];
export const DEFAULT_LOCALE: Locale = 'en';

const DICT: Record<Locale, Record<string, string>> = { en, zh };

export function t(locale: Locale, key: string): string {
  const v = DICT[locale]?.[key] ?? DICT[DEFAULT_LOCALE][key] ?? key;
  return v;
}

/** Raw HTML string (keys whose value contains markup, e.g. hero.title). */
export function th(locale: Locale, key: string): string {
  return t(locale, key);
}

/** Map a path like `/zh/` or `/zh/index.html` to its locale. */
export function localeFromUrl(pathname: string): Locale {
  const seg = pathname.split('/').filter(Boolean)[0];
  return seg === 'zh' ? 'zh' : 'en';
}

/** Localized path for the *other* locale, preserving the trailing route. */
export function localizedPath(locale: Locale, pathname: string): string {
  const parts = pathname.split('/').filter(Boolean);
  if (parts[0] === 'zh' || parts[0] === 'en') parts.shift();
  const rest = parts.join('/').replace(/index\.html$/, '');
  if (locale === DEFAULT_LOCALE) return rest ? `/${rest}` : '/';
  return rest ? `/zh/${rest}` : '/zh/';
}

/** Prefix a root-absolute site path with the configured Astro base.
 *  '/' at the domain root (ggcode.dev), '/ggcode' on the GitHub Pages project site. */
export function withBase(path: string): string {
  const base = (import.meta.env.BASE_URL || '/').replace(/\/$/, '');
  return base + (path.startsWith('/') ? path : `/${path}`);
}

export function localeHtmlLang(locale: Locale): string {
  return locale === 'zh' ? 'zh-CN' : 'en';
}

export function localeOgLocale(locale: Locale): string {
  return locale === 'zh' ? 'zh_CN' : 'en_US';
}

export function localeHreflang(locale: Locale): string {
  return locale === 'zh' ? 'zh-CN' : 'en';
}
