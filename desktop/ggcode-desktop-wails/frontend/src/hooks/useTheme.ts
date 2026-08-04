/**
 * useTheme - unified theme management with real-time OS theme following.
 *
 * Gap fixed: Previously the app only checked `prefers-color-scheme` once on
 * mount. When the user changed their OS appearance setting (System Settings ->
 * Appearance) while the app was running, it did NOT react. Claude Desktop,
 * ChatGPT Desktop, and Cursor all follow system theme in real-time.
 *
 * This hook:
 *  - Stores the user's mode choice ('light' | 'dark' | 'auto') in localStorage
 *  - Listens to the `prefers-color-scheme: dark` media query for real-time OS changes
 *  - Applies/removes the `dark` class on <html> reactively
 *  - Exposes resolved state so components can react without duplicating logic
 *
 * Usage:
 *   const { mode, isDark, setMode, toggle } = useTheme()
 */

import { useState, useEffect, useCallback } from 'react'

export type ThemeMode = 'light' | 'dark' | 'auto'

const STORAGE_KEY = 'ggcode-theme'

/** Read the user's saved theme mode (defaults to 'auto'). */
export function getSavedThemeMode(): ThemeMode {
  const saved = localStorage.getItem(STORAGE_KEY)
  return saved === 'dark' || saved === 'light' ? saved : 'auto'
}

/** Query whether the OS currently prefers dark mode. */
function systemPrefersDark(): boolean {
  return window.matchMedia ? window.matchMedia('(prefers-color-scheme: dark)').matches : false
}

export interface UseThemeResult {
  /** The user's explicit choice. 'auto' = follow system. */
  mode: ThemeMode
  /** Whether dark class is currently applied (resolved after combining mode + system). */
  isDark: boolean
  /** Set the mode and persist to localStorage. */
  setMode: (mode: ThemeMode) => void
  /** Toggle between light and dark explicitly (skips 'auto'). Returns new mode. */
  toggle: () => ThemeMode
}

/**
 * Unified theme hook with real-time system theme following.
 *
 * When mode is 'auto', the dark/light state reacts instantly when the user
 * changes their OS appearance setting - no app restart needed.
 */
export function useTheme(): UseThemeResult {
  const [mode, setModeState] = useState<ThemeMode>(() => getSavedThemeMode())
  const [systemDark, setSystemDark] = useState<boolean>(() => systemPrefersDark())

  // Listen to OS theme changes in real-time (the core fix).
  useEffect(() => {
    if (!window.matchMedia) return
    const mql = window.matchMedia('(prefers-color-scheme: dark)')
    const handler = (e: MediaQueryListEvent) => setSystemDark(e.matches)
    // addEventListener is the modern API; addListener is the legacy fallback (Safari < 14)
    if (mql.addEventListener) {
      mql.addEventListener('change', handler)
      return () => mql.removeEventListener('change', handler)
    }
    // Legacy fallback for older WebKit (Safari < 14)
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const legacyMql = mql as any
    if (legacyMql.addListener) {
      legacyMql.addListener(handler)
      return () => legacyMql.removeListener(handler)
    }
  }, [])

  // Resolved dark state: explicit if user chose light/dark, otherwise follow system.
  const isDark = mode === 'auto' ? systemDark : mode === 'dark'

  // Apply the dark class to <html> whenever resolved state changes.
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
    // Toggle between explicit light/dark (not auto), so the shortcut is predictable.
    const next: ThemeMode = isDark ? 'light' : 'dark'
    setMode(next)
    return next
  }, [isDark, setMode])

  return { mode, isDark, setMode, toggle }
}
