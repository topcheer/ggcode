import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { renderHook, act } from '@testing-library/react'
import { useOnlineStatus } from './useOnlineStatus'

describe('useOnlineStatus', () => {
  beforeEach(() => {
    vi.stubGlobal('navigator', { onLine: true })
  })

  afterEach(() => {
    vi.restoreAllMocks()
    vi.unstubAllGlobals()
  })

  it('returns true when navigator.onLine is true', () => {
    const { result } = renderHook(() => useOnlineStatus())
    expect(result.current.isOnline).toBe(true)
  })

  it('returns false when navigator.onLine is false', () => {
    vi.stubGlobal('navigator', { onLine: false })
    const { result } = renderHook(() => useOnlineStatus())
    expect(result.current.isOnline).toBe(false)
  })

  it('updates when offline event fires', () => {
    const { result } = renderHook(() => useOnlineStatus())
    expect(result.current.isOnline).toBe(true)

    act(() => {
      window.dispatchEvent(new Event('offline'))
    })

    expect(result.current.isOnline).toBe(false)
  })

  it('updates when online event fires after offline', () => {
    vi.stubGlobal('navigator', { onLine: false })
    const { result } = renderHook(() => useOnlineStatus())
    expect(result.current.isOnline).toBe(false)

    act(() => {
      window.dispatchEvent(new Event('online'))
    })

    expect(result.current.isOnline).toBe(true)
  })

  it('cleans up event listeners on unmount', () => {
    const addSpy = vi.spyOn(window, 'addEventListener')
    const removeSpy = vi.spyOn(window, 'removeEventListener')

    const { unmount } = renderHook(() => useOnlineStatus())
    unmount()

    // Each event (online, offline) should have been added and removed
    const onlineAdd = addSpy.mock.calls.find(c => c[0] === 'online')
    const offlineAdd = addSpy.mock.calls.find(c => c[0] === 'offline')
    const onlineRemove = removeSpy.mock.calls.find(c => c[0] === 'online')
    const offlineRemove = removeSpy.mock.calls.find(c => c[0] === 'offline')

    expect(onlineAdd).toBeDefined()
    expect(offlineAdd).toBeDefined()
    expect(onlineRemove).toBeDefined()
    expect(offlineRemove).toBeDefined()
  })
})
