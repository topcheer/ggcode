

import { useState, useEffect, useCallback } from 'react'

export type ThemeMode = 'light' | 'dark' | 'auto'

const STORAGE_KEY = 'ggcode-theme'

export function getSavedThemeMode(): ThemeMode {
  const saved = localStorage.getItem(STORAGE_KEY)
  return saved === 'dark' || saved === 'light' ? saved : 'auto'
}

function systemPrefersDark(): boolean {
  return window.matchMedia ? window.matchMedia('(prefers-color-scheme: dark)').matches : false
}

export interface UseThemeResult {

  mode: ThemeMode

  isDark: boolean

  setMode: (mode: ThemeMode) => void

  toggle: () => ThemeMode
}

export function useTheme(): UseThemeResult {
  const [mode, setModeState] = useState<ThemeMode>(() => getSavedThemeMode())
  const [systemDark, setSystemDark] = useState<boolean>(() => systemPrefersDark())

  useEffect(() => {
    if (!window.matchMedia) return
    const mql = window.matchMedia('(prefers-color-scheme: dark)')
    const handler = (e: MediaQueryListEvent) => setSystemDark(e.matches)
    if (mql.addEventListener) {
      mql.addEventListener('change', handler)
      return () => mql.removeEventListener('change', handler)
    }
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const legacyMql = mql as any
    if (legacyMql.addListener) {
      legacyMql.addListener(handler)
      return () => legacyMql.removeListener(handler)
    }
  }, [])

  const isDark = mode === 'auto' ? systemDark : mode === 'dark'

  useEffect(() => {
    document.documentElement.classList.toggle('dark', isDark)
  }, [isDark])

  const setMode = useCallback((newMode: ThemeMode) => {
    setModeState(newMode)
    if (newMode === 'auto') {
      localStorage.removeItem(STORAGE_KEY)
    } else {
      localStorage.setItem(STORAGE_KEY, newMode)
    }
  }, [])

  const toggle = useCallback((): ThemeMode => {
    const next: ThemeMode = isDark ? 'light' : 'dark'
    setMode(next)
    return next
  }, [isDark, setMode])

  return { mode, isDark, setMode, toggle }
}
