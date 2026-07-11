import React, { createContext, useContext, useState, useCallback, useEffect } from 'react'
import en, { TranslationKey } from './en'
import zh from './zh'
import ja from './ja'
import ko from './ko'
import es from './es'
import fr from './fr'
import de from './de'
import ru from './ru'
import pt from './pt'
import vi from './vi'
import tr from './tr'
import id from './id'
import zhTW from './zh-TW'

export type Locale = 'en' | 'zh' | 'zh-TW' | 'ja' | 'ko' | 'es' | 'fr' | 'de' | 'ru' | 'pt' | 'vi' | 'tr' | 'id'

export const LOCALE_LABELS: Record<Locale, string> = {
  en: 'English',
  zh: '中文',
  ja: '日本語',
  ko: '한국어',
  es: 'Español',
  fr: 'Français',
  de: 'Deutsch',
  ru: 'Русский',
  pt: 'Português',
  vi: 'Tiếng Việt',
  tr: 'Türkçe',
  id: 'Bahasa Indonesia',
  'zh-TW': '繁體中文',
}

const translations: Record<Locale, Partial<Record<TranslationKey, string>>> = { en, zh, 'zh-TW': zhTW, ja, ko, es, fr, de, ru, pt, vi, tr, id }

interface I18nContextValue {
  locale: Locale
  setLocale: (l: Locale) => void
  t: (key: TranslationKey, params?: Record<string, string | number>) => string
}

const I18nContext = createContext<I18nContextValue>({
  locale: 'en',
  setLocale: () => {},
  t: (key) => key,
})

/** Translate a key with optional {n} interpolation */
function translate(
  locale: Locale,
  key: TranslationKey,
  params?: Record<string, string | number>,
): string {
  let val = translations[locale]?.[key] ?? translations.en[key] ?? key
  if (params) {
    for (const [k, v] of Object.entries(params)) {
      val = val.replace(new RegExp(`\\{${k}\\}`, 'g'), String(v))
    }
  }
  return val
}

export function I18nProvider({
  initialLocale = 'en',
  onLocaleChange,
  children,
}: {
  initialLocale?: Locale
  onLocaleChange?: (locale: Locale) => void
  children: React.ReactNode
}) {
  const [locale, setLocaleInternal] = useState<Locale>(initialLocale)

  const setLocale = useCallback((l: Locale) => {
    setLocaleInternal(l)
    onLocaleChange?.(l)
  }, [onLocaleChange])

  const t = useCallback((key: TranslationKey, params?: Record<string, string | number>) => {
    return translate(locale, key, params)
  }, [locale])

  // Sync if initialLocale changes externally (e.g. config loaded)
  useEffect(() => {
    setLocaleInternal(initialLocale)
  }, [initialLocale])

  return (
    <I18nContext.Provider value={{ locale, setLocale, t }}>
      {children}
    </I18nContext.Provider>
  )
}

/** Detect the best matching locale from the browser/OS language setting. */
export function detectSystemLocale(): Locale {
  const langs = navigator.languages || [navigator.language || 'en']
  for (const lang of langs) {
    const lower = lang.toLowerCase()
    // Chinese variant detection
    if (lower.startsWith('zh')) {
      if (['zh-tw', 'zh-hant', 'zh-hk', 'zh-mo'].some(r => lower.startsWith(r))) {
        return 'zh-TW' as Locale
      }
      return 'zh' as Locale
    }
    // Exact match (e.g. "zh", "ja", "ko")
    const exact = lower.split('-')[0] as Locale
    if (exact in LOCALE_LABELS) return exact
    // Region match (e.g. "zh-CN" → "zh", "pt-BR" → "pt")
    const region = lower.split('-')[0]
    if (region in LOCALE_LABELS) return region as Locale
  }
  return 'en'
}

export function useTranslation() {
  return useContext(I18nContext)
}

export type { TranslationKey }
