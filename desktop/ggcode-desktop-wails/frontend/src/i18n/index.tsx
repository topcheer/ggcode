import React, { createContext, useContext, useState, useCallback, useEffect } from 'react'
import en, { TranslationKey } from './en'
import zh from './zh'

export type Locale = 'en' | 'zh'

const translations: Record<Locale, Record<TranslationKey, string>> = { en, zh }

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

export function useTranslation() {
  return useContext(I18nContext)
}

export type { TranslationKey }
