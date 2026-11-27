// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { renderHook, act } from '@testing-library/react'

const localStorageMock = (() => {
  let store: Record<string, string> = {}
  return {
    getItem: vi.fn((key: string) => store[key] || null),
    setItem: vi.fn((key: string, value: string) => { store[key] = value }),
    clear: () => { store = {} },
  }
})()
Object.defineProperty(window, 'localStorage', { value: localStorageMock })

const styleMock: Record<string, string> = {}
Object.defineProperty(document.documentElement, 'style', {
  value: {
    setProperty: vi.fn((key: string, value: string) => { styleMock[key] = value }),
    fontSize: '',
  },
})

const { useZoom } = await import('./useZoom')

describe('useZoom', () => {
  beforeEach(() => {
    localStorageMock.clear()
    vi.clearAllMocks()
  })

  it('defaults to zoom 1', () => {
    const { result } = renderHook(() => useZoom())
    expect(result.current.zoom).toBe(1)
  })

  it('zoomIn increases zoom by 0.1', () => {
    const { result } = renderHook(() => useZoom())
    act(() => result.current.zoomIn())
    expect(result.current.zoom).toBe(1.1)
  })

  it('zoomOut decreases zoom by 0.1', () => {
    const { result } = renderHook(() => useZoom())
    act(() => result.current.zoomOut())
    expect(result.current.zoom).toBe(0.9)
  })

  it('resetZoom sets zoom back to 1', () => {
    const { result } = renderHook(() => useZoom())
    act(() => result.current.zoomIn())
    act(() => result.current.zoomIn())
    act(() => result.current.resetZoom())
    expect(result.current.zoom).toBe(1)
  })

  it('clamps zoom to max 1.8', () => {
    const { result } = renderHook(() => useZoom())
    act(() => {
      for (let i = 0; i < 20; i++) result.current.zoomIn()
    })
    expect(result.current.zoom).toBe(1.8)
  })

  it('clamps zoom to min 0.7', () => {
    const { result } = renderHook(() => useZoom())
    act(() => {
      for (let i = 0; i < 20; i++) result.current.zoomOut()
    })
    expect(result.current.zoom).toBe(0.7)
  })

  it('applies the zoom to <body> (html font-size alone scaled nothing)', () => {
    const bodyZoom: string[] = []
    Object.defineProperty(document.body.style, 'zoom', { configurable: true, get: () => bodyZoom[0] ?? '', set: (v: string) => { bodyZoom[0] = v } })
    const { result } = renderHook(() => useZoom())
    act(() => { result.current.zoomIn() })
    expect(result.current.zoom).toBe(1.1)
    expect(bodyZoom[0]).toBe('1.1')
    act(() => { result.current.resetZoom() })
    expect(bodyZoom[0]).toBe('1')
  })
})
