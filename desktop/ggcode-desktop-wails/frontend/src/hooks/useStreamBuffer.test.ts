// @vitest-environment jsdom
import { describe, expect, it, vi } from 'vitest'
import { renderHook, act } from '@testing-library/react'
import { useStreamBuffer } from './useStreamBuffer'

describe('useStreamBuffer', () => {
  it('flushes immediately in passthrough mode (enabled=false)', () => {
    const onFlush = vi.fn()
    const { result } = renderHook(() => useStreamBuffer(onFlush, false))

    act(() => {
      result.current.buffer({ type: 'text', content: 'hello' })
    })

    expect(onFlush).toHaveBeenCalledTimes(1)
    expect(onFlush).toHaveBeenCalledWith([{ type: 'text', content: 'hello' }])
  })

  it('accumulates buffer when enabled', () => {
    const onFlush = vi.fn()
    const { result } = renderHook(() => useStreamBuffer(onFlush, true))

    act(() => {
      result.current.buffer({ type: 'text', content: 'a' })
      result.current.buffer({ type: 'text', content: 'b' })
      result.current.buffer({ type: 'reasoning', content: 'thinking' })
    })

    expect(result.current.hasPending()).toBe(true)
  })

  it('flush() drains all pending chunks', () => {
    const onFlush = vi.fn()
    const { result } = renderHook(() => useStreamBuffer(onFlush, true))

    act(() => {
      result.current.buffer({ type: 'text', content: 'a' })
      result.current.buffer({ type: 'text', content: 'b' })
    })

    expect(result.current.hasPending()).toBe(true)

    act(() => {
      result.current.flush()
    })

    expect(onFlush).toHaveBeenCalledTimes(1)
    expect(onFlush).toHaveBeenCalledWith([
      { type: 'text', content: 'a' },
      { type: 'text', content: 'b' },
    ])
    expect(result.current.hasPending()).toBe(false)
  })

  it('hasPending() returns false when buffer is empty', () => {
    const onFlush = vi.fn()
    const { result } = renderHook(() => useStreamBuffer(onFlush, false))
    expect(result.current.hasPending()).toBe(false)
  })

  it('handles messageID in buffered chunks', () => {
    const onFlush = vi.fn()
    const { result } = renderHook(() => useStreamBuffer(onFlush, true))

    act(() => {
      result.current.buffer({ type: 'text', content: 'hello', messageID: 'msg-1' })
      result.current.flush()
    })

    expect(onFlush).toHaveBeenCalledWith([
      { type: 'text', content: 'hello', messageID: 'msg-1' },
    ])
  })

  it('coalesces many deltas into one flush per interval (fake timers)', () => {
    vi.useFakeTimers()
    try {
      const onFlush = vi.fn()
      const { result } = renderHook(() => useStreamBuffer(onFlush, true, 30))
      act(() => {
        for (let i = 0; i < 50; i++) result.current.buffer({ type: 'text', content: 'x' })
      })
      // Nothing rendered yet, exactly one timer armed.
      expect(onFlush).not.toHaveBeenCalled()
      expect(vi.getTimerCount()).toBe(1)
      act(() => { vi.advanceTimersByTime(29) })
      expect(onFlush).not.toHaveBeenCalled()
      act(() => { vi.advanceTimersByTime(1) })
      expect(onFlush).toHaveBeenCalledTimes(1)
      expect(onFlush.mock.calls[0][0]).toHaveLength(50)
      // Idle buffer -> no timer running (the old rAF loop ran forever).
      expect(vi.getTimerCount()).toBe(0)
      expect(result.current.hasPending()).toBe(false)
    } finally {
      vi.useRealTimers()
    }
  })

  it('flushes the pending tail when the document becomes hidden', () => {
    const onFlush = vi.fn()
    const { result } = renderHook(() => useStreamBuffer(onFlush, true, 30))
    act(() => { result.current.buffer({ type: 'text', content: 'tail' }) })
    expect(onFlush).not.toHaveBeenCalled()
    Object.defineProperty(document, 'hidden', { configurable: true, get: () => true })
    act(() => { document.dispatchEvent(new Event('visibilitychange')) })
    expect(onFlush).toHaveBeenCalledTimes(1)
    expect(onFlush).toHaveBeenCalledWith([{ type: 'text', content: 'tail' }])
    Object.defineProperty(document, 'hidden', { configurable: true, get: () => false })
  })
})
