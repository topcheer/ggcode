/**
 * Tests for useTheme hook - real-time system theme following.
 */
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { renderHook, act } from '@testing-library/react'
import { useTheme } from './useTheme'

// Ensure matchMedia exists (jsdom doesn't provide it)
if (typeof (window as any).matchMedia === 'undefined') {
  ;(window as any).matchMedia = () => ({
    matches: false,
    media: '',
    onchange: null,
    addEventListener: () => {},
    removeEventListener: () => {},
    addListener: () => {},
    removeListener: () => {},
    dispatchEvent: () => false,
  })
}

// Ensure localStorage exists (some jsdom/Node configs lack it)
if (typeof globalThis.localStorage === 'undefined' || !globalThis.localStorage) {
  const store: Record<string, string> = {}
  const lsMock = {
    getItem: (k: string) => store[k] ?? null,
    setItem: (k: string, v: string) => { store[k] = v },
    removeItem: (k: string) => { delete store[k] },
    clear: () => { for (const k of Object.keys(store)) delete store[k] },
  }
  Object.defineProperty(globalThis, 'localStorage', { value: lsMock, writable: true, configurable: true })
  if (typeof window !== 'undefined') {
    Object.defineProperty(window, 'localStorage', { value: lsMock, writable: true, configurable: true })
  }
}

describe('useTheme', () => {
  beforeEach(() => {
    localStorage.clear()
    document.documentElement.classList.remove('dark')
  })

  // Helper: mock window.matchMedia with configurable matches value.
  function mockMatchMedia(matches: boolean) {
    const listeners: ((e: MediaQueryListEvent) => void)[] = []
    const mql: any = {
      matches,
      media: '(prefers-color-scheme: dark)',
      onchange: null,
      addEventListener: vi.fn((_: string, cb: any) => { listeners.push(cb) }),
      removeEventListener: vi.fn(),
      addListener: vi.fn((cb: any) => { listeners.push(cb) }),
      removeListener: vi.fn(),
      dispatchEvent: vi.fn(),
    }
    // jsdom may not have matchMedia; use defineProperty for broad compat
    Object.defineProperty(window, 'matchMedia', {
      value: vi.fn().mockReturnValue(mql),
      writable: true,
      configurable: true,
    })
    return { mql, listeners }
  }

  it('defaults to auto mode when no saved preference', () => {
    mockMatchMedia(false)
    const { result } = renderHook(() => useTheme())
    expect(result.current.mode).toBe('auto')
    expect(result.current.isDark).toBe(false)
  })

  it('respects saved dark preference from localStorage', () => {
    localStorage.setItem('ggcode-theme', 'dark')
    mockMatchMedia(false)
    const { result } = renderHook(() => useTheme())
    expect(result.current.mode).toBe('dark')
    expect(result.current.isDark).toBe(true)
  })

  it('respects saved light preference overriding system dark', () => {
    localStorage.setItem('ggcode-theme', 'light')
    mockMatchMedia(true)
    const { result } = renderHook(() => useTheme())
    expect(result.current.mode).toBe('light')
    expect(result.current.isDark).toBe(false)
  })

  it('applies dark class to <html> when dark', () => {
    localStorage.setItem('ggcode-theme', 'dark')
    mockMatchMedia(false)
    renderHook(() => useTheme())
    expect(document.documentElement.classList.contains('dark')).toBe(true)
  })

  it('setMode persists to localStorage and updates state', () => {
    mockMatchMedia(false)
    const { result } = renderHook(() => useTheme())
    act(() => result.current.setMode('dark'))
    expect(result.current.mode).toBe('dark')
    expect(localStorage.getItem('ggcode-theme')).toBe('dark')
    expect(result.current.isDark).toBe(true)
  })

  it('setMode auto removes localStorage entry', () => {
    localStorage.setItem('ggcode-theme', 'dark')
    mockMatchMedia(false)
    const { result } = renderHook(() => useTheme())
    act(() => result.current.setMode('auto'))
    expect(result.current.mode).toBe('auto')
    expect(localStorage.getItem('ggcode-theme')).toBeNull()
  })

  it('toggle switches between light and dark', () => {
    mockMatchMedia(false)
    const { result } = renderHook(() => useTheme())
    // auto + system light => isDark=false, toggle => dark
    act(() => {
      const next = result.current.toggle()
      expect(next).toBe('dark')
    })
    expect(result.current.isDark).toBe(true)
    // now dark => toggle => light
    act(() => {
      const next = result.current.toggle()
      expect(next).toBe('light')
    })
    expect(result.current.isDark).toBe(false)
  })

  it('reacts to real-time system theme change in auto mode', () => {
    const { listeners } = mockMatchMedia(false)
    const { result } = renderHook(() => useTheme())
    expect(result.current.isDark).toBe(false)
    // Simulate OS switching to dark mode
    act(() => {
      listeners.forEach((cb) =>
        cb({ matches: true, media: '(prefers-color-scheme: dark)' } as MediaQueryListEvent)
      )
    })
    expect(result.current.isDark).toBe(true)
    expect(document.documentElement.classList.contains('dark')).toBe(true)
  })

  it('does not react to system change when mode is explicit', () => {
    localStorage.setItem('ggcode-theme', 'light')
    const { listeners } = mockMatchMedia(false)
    const { result } = renderHook(() => useTheme())
    expect(result.current.isDark).toBe(false)
    // System switches to dark, but explicit light stays light
    act(() => {
      listeners.forEach((cb) =>
        cb({ matches: true, media: '(prefers-color-scheme: dark)' } as MediaQueryListEvent)
      )
    })
    expect(result.current.mode).toBe('light')
    expect(result.current.isDark).toBe(false)
  })
})
